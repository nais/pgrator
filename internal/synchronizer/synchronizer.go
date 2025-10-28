package synchronizer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/object"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type MutateFn func()

type Synchronizer[T object.NaisObject, P any] struct {
	client     client.Client
	scheme     *runtime.Scheme
	reconciler reconciler.Reconciler[T, P]
}

func NewSynchronizer[T object.NaisObject, P any](k8sClient client.Client, scheme *runtime.Scheme, r reconciler.Reconciler[T, P]) *Synchronizer[T, P] {
	return &Synchronizer[T, P]{
		client:     k8sClient,
		scheme:     scheme,
		reconciler: r,
	}
}

func (s *Synchronizer[T, P]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

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
		err = s.client.Status().Update(ctx, obj)
		if err != nil {
			logger.Error(err, "failed to update status")
		}
		status = obj.GetStatus()
	}

	defer updateStatus()

	var actions []action.Action
	var result ctrl.Result

	deletionTimestamp := obj.GetDeletionTimestamp()
	finalizer := s.reconciler.Name()
	finalizers := obj.GetFinalizers()
	if deletionTimestamp != nil {
		if len(finalizers) > 0 && finalizers[0] == finalizer {
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
			return result, err
		}
	}

	status.ReconcilePhase = "Preparing"
	updateStatus()
	prep, result, err := s.reconciler.Prepare(ctx, s.client, obj)
	if err != nil {
		logger.Error(err, "failed preparation stage")
		return result, err
	}

	status.ReconcilePhase = "Updating"
	updateStatus()
	actions, result, err = s.reconciler.Update(obj, prep)
	if err != nil {
		logger.Error(err, "failed to calculate update actions")
		return result, err
	}

	status.ReconcilePhase = "PerformingActions"
	updateStatus()
	result, err = s.PerformActions(ctx, actions)
	if err != nil {
		logger.Error(err, "failed to perform reconciliation")
		return result, err
	}

	status.ReconcilePhase = "Completed"
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
	builder := ctrl.NewControllerManagedBy(mgr).
		For(s.reconciler.New()).
		Named("postgres")
	for _, t := range s.reconciler.OwnedTypes() {
		builder = builder.Owns(t)
	}

	annotation := fmt.Sprintf("%s/owner", s.reconciler.Name())
	for _, t := range s.reconciler.AdditionalTypes() {
		builder = builder.Watches(t, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
			if value, ok := object.GetAnnotations()[annotation]; ok {
				parts := strings.Split(value, ":")
				return []reconcile.Request{
					{
						NamespacedName: types.NamespacedName{
							Namespace: parts[0],
							Name:      parts[1],
						},
					},
				}
			}
			return nil
		}))
	}
	return builder.
		Complete(s)
}
