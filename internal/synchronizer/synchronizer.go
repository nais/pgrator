package synchronizer

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/object"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
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

	updateStatus := func() error {
		err = s.client.Status().Update(ctx, obj)
		if err != nil {
			logger.Error(err, "failed to update status")
			return err
		}
		status = obj.GetStatus()
		return nil
	}

	defer func() {
		if err := updateStatus(); err != nil {
			logger.Error(err, "deferred update of status failed")
		}
	}()

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
	if err = updateStatus(); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	prep, result, err := s.reconciler.Prepare(ctx, s.client, obj)
	if err != nil {
		logger.Error(err, "failed preparation stage")
		return result, err
	}

	status.ReconcilePhase = "Updating"
	if err = updateStatus(); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	actions, result, err = s.reconciler.Update(obj, prep)
	if err != nil {
		logger.Error(err, "failed to calculate update actions")
		return result, err
	}

	status.ReconcilePhase = "PerformingActions"
	if err = updateStatus(); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}
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
	opts := controller.Options{
		ReconciliationTimeout: 60 * time.Second,
	}
	builder := ctrl.NewControllerManagedBy(mgr).
		For(s.reconciler.New()).
		WithOptions(opts).
		WithEventFilter(predicate.Or(
			GenerationChangedPredicate{
				Scheme:   mgr.GetScheme(),
				MainKind: findKind(s.reconciler.New(), mgr.GetScheme()),
			},
			predicate.AnnotationChangedPredicate{},
			predicate.LabelChangedPredicate{},
		)).
		Named(s.reconciler.Name())
	for _, t := range s.reconciler.OwnedTypes() {
		builder = builder.Owns(t)
	}

	annotation := fmt.Sprintf("%s/owner", s.reconciler.Name())
	for _, t := range s.reconciler.AdditionalTypes() {
		builder = builder.Watches(t, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
			if value, ok := object.GetAnnotations()[annotation]; ok {
				parts := strings.Split(value, ":")
				if len(parts) != 2 {
					return nil
				}
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

type GenerationChangedPredicate struct {
	predicate.TypedFuncs[client.Object]
	Scheme   *runtime.Scheme
	MainKind string
}

// Update allows events for secondary kinds while only accepting generational changes for main kind
func (p GenerationChangedPredicate) Update(e event.TypedUpdateEvent[client.Object]) bool {
	if isNil(e.ObjectOld) {
		return false
	}
	if isNil(e.ObjectNew) {
		return false
	}

	objKind := findKind(e.ObjectNew, p.Scheme)
	if objKind != p.MainKind {
		return true
	}

	return e.ObjectNew.GetGeneration() != e.ObjectOld.GetGeneration()
}

func findKind(obj client.Object, scheme *runtime.Scheme) string {
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil {
		return ""
	}

	for _, gvk := range gvks {
		if gvk.Kind != "" {
			return gvk.Kind
		}
	}

	return ""
}

func isNil(arg any) bool {
	if v := reflect.ValueOf(arg); !v.IsValid() || ((v.Kind() == reflect.Ptr ||
		v.Kind() == reflect.Interface ||
		v.Kind() == reflect.Slice ||
		v.Kind() == reflect.Map ||
		v.Kind() == reflect.Chan ||
		v.Kind() == reflect.Func) && v.IsNil()) {
		return true
	}
	return false
}
