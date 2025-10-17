package synchronizer

import (
	"context"
	"time"

	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type MutateFn func()

type Synchronizer struct {
	client     client.Client
	reconciler reconciler.Reconciler[NaisObject, any]
}

func NewSynchronizer(client client.Client, reconciler reconciler.Reconciler[NaisObject, any]) *Synchronizer {
	return &Synchronizer{
		client:     client,
		reconciler: reconciler,
	}
}

func (s *Synchronizer) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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

	var actions []action.Action
	var result ctrl.Result

	deletionTimestamp := obj.GetDeletionTimestamp()
	finalizer := s.reconciler.Name()
	finalizers := obj.GetFinalizers()
	if deletionTimestamp != nil {
		if len(finalizers) > 0 && finalizers[0] == finalizer {
			actions, result, err = s.reconciler.Delete(ctx, obj)
			if err != nil {
				logger.Error(err, "failed to calculate delete actions")
				return result, err
			}
			result, err = s.PerformActions(actions)
			if err != nil {
				logger.Error(err, "failed to perform delete actions")
				return result, err
			}
			if controllerutil.RemoveFinalizer(obj, finalizer) {
				err = s.client.Update(ctx, obj)
				if err != nil {
					logger.Error(err, "failed to remove finalizer")
					return ctrl.Result{
						RequeueAfter: 10 * time.Second,
					}, err
				}
			}
		}
		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(obj, finalizer) {
		err = s.client.Update(ctx, obj)
		if err != nil {
			logger.Error(err, "failed to add finalizer")
			return ctrl.Result{
				RequeueAfter: 1 * time.Second,
			}, err
		}
	}

	prep, result, err := s.reconciler.Prepare(ctx, s.client, obj)
	if err != nil {
		logger.Error(err, "failed preparation stage")
		return result, err
	}

	actions, result, err = s.reconciler.Update(ctx, obj, prep)
	if err != nil {
		logger.Error(err, "failed to calculate update actions")
		return result, err
	}

	result, err = s.PerformActions(actions)
	if err != nil {
		logger.Error(err, "failed to perform reconciliation")
		return result, err
	}

	// TODO update status

	return ctrl.Result{}, nil
}

func (s *Synchronizer) PerformActions(actions []action.Action) (ctrl.Result, error) {
	//TODO implement me
	panic("implement me")
}
