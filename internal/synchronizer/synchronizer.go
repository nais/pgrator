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
	core_v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
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

type Synchronizer[T object.NaisObject, P any] struct {
	client     client.Client
	scheme     *runtime.Scheme
	reconciler reconciler.Reconciler[T, P]
	recorder   events.EventRecorder

	ownerAnnotationKey string
	relevantListTypes  map[schema.GroupVersionKind]reflect.Type
}

func NewSynchronizer[T object.NaisObject, P any](k8sClient client.Client, scheme *runtime.Scheme, r reconciler.Reconciler[T, P], recorder events.EventRecorder) *Synchronizer[T, P] {
	return &Synchronizer[T, P]{
		client:     k8sClient,
		scheme:     scheme,
		reconciler: r,
		recorder:   recorder,

		ownerAnnotationKey: fmt.Sprintf("%s/owner", r.Name()),
		relevantListTypes:  findRelevantListTypes(r, scheme),
	}
}

func findRelevantListTypes[T object.NaisObject, P any](r reconciler.Reconciler[T, P], scheme *runtime.Scheme) map[schema.GroupVersionKind]reflect.Type {
	relevantTypes := make([]client.Object, 0)
	relevantTypes = append(relevantTypes, r.OwnedTypes()...)
	relevantTypes = append(relevantTypes, r.AdditionalTypes()...)

	listTypes := make(map[schema.GroupVersionKind]reflect.Type)
	for groupVersionKind, r := range scheme.AllKnownTypes() {
		for _, relevantType := range relevantTypes {
			relevantGvks, _, err := scheme.ObjectKinds(relevantType)
			if err != nil {
				return nil
			}
			for _, relevantGvk := range relevantGvks {
				if relevantGvk.Group == groupVersionKind.Group &&
					relevantGvk.Version == groupVersionKind.Version &&
					fmt.Sprintf("%sList", relevantGvk.Kind) == groupVersionKind.Kind {
					listTypes[groupVersionKind] = r
				}
			}
		}
	}
	return listTypes
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
	s.recordEvent(obj, core_v1.EventTypeNormal, "Reconciling", "Reconciling %s/%s", obj.GetNamespace(), obj.GetName())

	status.ReconcilePhase = "Preparing"
	if err = updateStatus(); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	s.recordEvent(obj, core_v1.EventTypeNormal, "Preparing", "Preparing resources")

	prep, result, err := s.reconciler.Prepare(ctx, s.client, obj)
	if err != nil {
		logger.Error(err, "failed preparation stage")
		return result, err
	}

	deletionTimestamp := obj.GetDeletionTimestamp()
	finalizer := s.reconciler.Name()
	finalizers := obj.GetFinalizers()
	finalizerFunc := controllerutil.AddFinalizer
	if deletionTimestamp != nil {
		if len(finalizers) > 0 && finalizers[0] == finalizer {
			actions, result, err = s.reconciler.Delete(obj)
			if err != nil {
				logger.Error(err, "failed to calculate delete actions")
				s.recordErrorEvent(obj, "Delete", err)
				return result, err
			}

			// If all actions are NoOp, skip deletion
			// We are not interested in cleaning up resources if deletion is not allowed
			// Then no further action is needed, until AllowDeletion is set to true and reconciliation is re-triggered
			if action.AllNoOp(actions) {
				logger.Info("Skipping deletion because AllowDeletion=false", "name", req.NamespacedName)
				s.recordEvent(obj, core_v1.EventTypeNormal, "SkippingDelete", "Skipping deletion for %s/%s because AllowDeletion=false", obj.GetNamespace(), obj.GetName())
				return result, nil
			}

			status.ReconcilePhase = "Deleting"
			if err = updateStatus(); err != nil {
				if apierrors.IsConflict(err) {
					return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
				}
				return ctrl.Result{}, err
			}

			s.recordEvent(obj, core_v1.EventTypeNormal, "Deleting", "Deleting resources")
			finalizerFunc = controllerutil.RemoveFinalizer
		}
	} else {

		status.ReconcilePhase = "Updating"
		if err = updateStatus(); err != nil {
			if apierrors.IsConflict(err) {
				return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}
		s.recordEvent(obj, core_v1.EventTypeNormal, "Updating", "Reconciling current state")

		actions, result, err = s.reconciler.Update(obj, prep)
		if err != nil {
			logger.Error(err, "failed to calculate update actions")
			s.recordErrorEvent(obj, "Update", err)
			return result, err
		}
	}

	status.ReconcilePhase = "DetectingUnreferenced"
	if err = updateStatus(); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	s.recordEvent(obj, core_v1.EventTypeNormal, "DetectingUnreferenced", "Detecting unreferenced resources")

	actions, err = s.DetectUnreferenced(ctx, obj, actions)
	if err != nil {
		logger.Error(err, "unable to detect unreferenced resources")
		s.recordErrorEvent(obj, "DetectUnreferenced", err)
		return ctrl.Result{}, err
	}

	status.ReconcilePhase = "PerformingActions"
	if err = updateStatus(); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	s.recordEvent(obj, core_v1.EventTypeNormal, "PerformingActions", "Performing %d actions", len(actions))

	result, err = s.PerformActions(ctx, actions)
	if err != nil {
		logger.Error(err, "failed to perform reconciliation")
		s.recordErrorEvent(obj, "PerformActions", err)
		return result, err
	}

	if finalizerFunc(obj, finalizer) {
		err = s.client.Update(ctx, obj)
		if err != nil {
			logger.Error(err, "failed to update finalizer")
			s.recordErrorEvent(obj, "FinalizerUpdate", err)
			return ctrl.Result{}, err
		}
	}

	s.recordEvent(obj, core_v1.EventTypeNormal, "Synchronized", "Successfully synchronized %s/%s", obj.GetNamespace(), obj.GetName())

	status.ReconcilePhase = "Completed"
	return result, nil
}

func (s *Synchronizer[T, P]) PerformActions(ctx context.Context, actions []action.Action) (ctrl.Result, error) {
	var err error
	for _, a := range actions {
		// TODO: s.addOwnerAnnotation(a)
		// Must handle IAMPolicyMember before adding owner annotation here
		err = a.Do(ctx, s.client, s.scheme)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// TODO: Must handle IAMPolicyMember before adding owner annotation
// func (s *Synchronizer[T, P]) addOwnerAnnotation(a action.Action) {
// 	obj := a.GetObject()
// 	annotations := obj.GetAnnotations()
// 	if annotations == nil {
// 		annotations = make(map[string]string)
// 		obj.SetAnnotations(annotations)
// 	}
// 	annotations[s.ownerAnnotationKey] = client.ObjectKeyFromObject(a.GetOwner()).String()
// }

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

	for _, t := range s.reconciler.AdditionalTypes() {
		builder = builder.Watches(t, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
			if value, ok := object.GetAnnotations()[s.ownerAnnotationKey]; ok {
				name, err := parseNamespacedName(value)
				if err != nil {
					mgr.GetLogger().Error(err, "unable to parse owner")
					return nil
				}

				return []reconcile.Request{
					{
						NamespacedName: name,
					},
				}
			}
			return nil
		}))
	}
	return builder.
		Complete(s)
}

func (s *Synchronizer[T, P]) DetectUnreferenced(ctx context.Context, owner T, actions []action.Action) ([]action.Action, error) {
	// List all resources of owned or additional types
	// Filter unrelated resources (owner annotation / owner reference)
	annotationValue := client.ObjectKeyFromObject(owner).String()
	allResources := make([]client.Object, 0)
	for _, t := range s.relevantListTypes {
		list := reflect.New(t).Interface().(client.ObjectList)
		err := s.client.List(ctx, list)
		if err != nil {
			return nil, fmt.Errorf("unable to list %s: %w", t, err)
		}
		err = meta.EachListItem(list, func(obj runtime.Object) error {
			if cObj, ok := obj.(client.Object); ok {
				annotations := cObj.GetAnnotations()
				if v, ok := annotations[s.ownerAnnotationKey]; ok {
					if v == annotationValue {
						allResources = append(allResources, cObj)
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("failed to extract items from list: %w", err)
		}
	}

	// Filter resources referenced by already existing actions
	keep := func(existing client.Object) bool {
		for _, a := range actions {
			obj := a.GetObject()
			if reflect.TypeOf(obj) == reflect.TypeOf(existing) {
				if obj.GetName() == existing.GetName() {
					return true
				}
			}
		}
		return false
	}
	unreferenced := make([]client.Object, 0)
	for _, existing := range allResources {
		if !keep(existing) {
			unreferenced = append(unreferenced, existing)
		}
	}
	// Add DeleteIfExists action for remainder
	for _, existing := range unreferenced {
		actions = append(actions, action.DeleteIfExists(existing, owner, func(obj client.Object) []meta_v1.Condition { return nil }))
	}

	return actions, nil
}

func (s *Synchronizer[T, P]) recordEvent(obj object.NaisObject, eventType string, reason string, messageFmt string, args ...any) {
	if s.recorder != nil {
		msg := fmt.Sprintf(messageFmt, args...)
		s.recorder.Eventf(obj, nil, eventType, reason, "Reconcile", "[%s] %s", obj.GetCorrelationId(), msg)
	}
}

func (s *Synchronizer[T, P]) recordErrorEvent(obj object.NaisObject, phase string, err error) {
	if s.recorder != nil {
		s.recorder.Eventf(obj, nil, core_v1.EventTypeWarning, fmt.Sprintf("%sFailed", phase), "Error", "[%s] %s phase failed for %s/%s: %v", obj.GetCorrelationId(), phase, obj.GetNamespace(), obj.GetName(), err.Error())
	}
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

func parseNamespacedName(input string) (types.NamespacedName, error) {
	parts := strings.Split(input, string(types.Separator))
	if len(parts) != 2 {
		return types.NamespacedName{}, fmt.Errorf("can not parse invalid NamespacedName, incorrect number of parts: %d", len(parts))
	}
	return types.NamespacedName{
		Namespace: parts[0],
		Name:      parts[1],
	}, nil
}
