package synchronizer

import (
	"context"
	"time"

	"github.com/nais/pgrator/internal/metrics"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type MutateFn func()

type Synchronizer[T NaisObject, P any] struct {
	client     client.Client
	scheme     *runtime.Scheme
	reconciler reconciler.Reconciler[T, P]
}

func NewSynchronizer[T NaisObject, P any](k8sClient client.Client, scheme *runtime.Scheme, r reconciler.Reconciler[T, P]) *Synchronizer[T, P] {
	return &Synchronizer[T, P]{
		client:     k8sClient,
		scheme:     scheme,
		reconciler: r,
	}
}

func (s *Synchronizer[T, P]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	start := time.Now()
	resourceType := s.reconciler.Name()
	metrics.ReconcileTotal.WithLabelValues(resourceType).Inc()

	logger := logf.FromContext(ctx)

	obj := s.reconciler.New()
	err := s.client.Get(ctx, req.NamespacedName, obj)
	if err != nil {
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	status := obj.GetStatus()
	status.ReconcileTime = ptr.To(meta_v1.NewTime(time.Now()))
	status.ObservedGeneration = obj.GetGeneration()
	status.CorrelationID = obj.GetCorrelationId()

	updateStatus := func() {
		if err = s.client.Status().Update(ctx, obj); err != nil {
			logger.Error(err, "failed to update status")
		}
	}

	var reconcileErr error
	defer func() {
		metrics.ReconcileDuration.WithLabelValues(resourceType).Observe(time.Since(start).Seconds())

		if reconcileErr != nil {
			metrics.ReconcileErrors.WithLabelValues(resourceType).Inc()
			status.RolloutStatus = "Failed"
			status.ReconcilePhase = "Error"
			updateStatus()
		}
	}()

	// this will always be run, even if we already updated Completed status
	// and overwrites whatever status.ReconcilePhase we had before, or what's in the cache
	// defer updateStatus()

	var actions []action.Action
	var result ctrl.Result

	deletionTimestamp := obj.GetDeletionTimestamp()
	finalizer := s.reconciler.Name()
	finalizers := obj.GetFinalizers()
	if deletionTimestamp != nil {
		if len(finalizers) > 0 && controllerutil.ContainsFinalizer(obj, finalizer) {
			actions, result, err = s.reconciler.Delete(obj)
			if err != nil {
				logger.Error(err, "failed to calculate delete actions")
				return result, err
			}
			result, err = s.PerformActions(ctx, actions)
			if err != nil {
				logger.Error(err, "failed to perform delete actions")
				return result, err
			}
			if controllerutil.RemoveFinalizer(obj, finalizer) {
				err = s.client.Update(ctx, obj)
				if err != nil {
					logger.Error(err, "failed to remove finalizer")
					return ctrl.Result{}, err
				}
			}
		}
		return result, nil
	}

	if controllerutil.AddFinalizer(obj, finalizer) {
		err = s.client.Update(ctx, obj)
		if err != nil {
			logger.Error(err, "failed to add finalizer")
			reconcileErr = err
			return result, err
		}
	}

	status.ReconcilePhase = "Preparing"
	updateStatus()
	prep, result, err := s.reconciler.Prepare(ctx, s.client, obj)
	if err != nil {
		logger.Error(err, "failed preparation stage")
		reconcileErr = err
		return result, err
	}

	status.ReconcilePhase = "Updating"
	updateStatus()
	actions, result, err = s.reconciler.Update(obj, prep)
	if err != nil {
		logger.Error(err, "failed to calculate update actions")
		reconcileErr = err
		return result, err
	}

	status.ReconcilePhase = "PerformingActions"
	updateStatus()
	result, err = s.PerformActions(ctx, actions)
	if err != nil {
		logger.Error(err, "failed to perform reconciliation")
		reconcileErr = err
		return result, err
	}

	status.ReconcilePhase = "Completed"
	status.RolloutStatus = "Succeeded"
	metrics.ReconcileSuccess.WithLabelValues(resourceType).Inc()
	updateStatus()
	return result, nil
}

func (s *Synchronizer[T, P]) PerformActions(ctx context.Context, actions []action.Action) (ctrl.Result, error) {
	var err error
	for _, a := range actions {
		err = a.Do(ctx, s.client, s.scheme)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (s *Synchronizer[T, P]) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(s.reconciler.New()).
		Named("postgres").
		Complete(s)
}
