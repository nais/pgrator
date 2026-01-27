package synchronizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/object"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	core_v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
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
	recorder   events.Recorder

	ownerAnnotationKey string
	relevantListTypes  map[schema.GroupVersionKind]reflect.Type
}

func NewSynchronizer[T object.NaisObject, P any](k8sClient client.Client, scheme *runtime.Scheme, r reconciler.Reconciler[T, P], recorder events.Recorder) *Synchronizer[T, P] {
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
		if apierrors.IsNotFound(err) {
			logger.Info("object not found, skipping reconciliation")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	obj.GetStatus().SetReconcileTime(ptr.To(meta_v1.NewTime(time.Now())))
	obj.GetStatus().SetObservedGeneration(obj.GetGeneration())
	obj.GetStatus().SetCorrelationID(obj.GetCorrelationId())

	updateStatus := func() error {
		if err := s.client.Status().Update(ctx, obj); err != nil {
			logger.Error(err, "failed to update status")
			return err
		}
		return nil
	}

	defer func() {
		if err := updateStatus(); err != nil {
			logger.Error(err, "deferred update of status failed")
		}
	}()

	s.recorder.RecordEvent(obj, core_v1.EventTypeNormal, "Reconciling", "Reconciling %s/%s", obj.GetNamespace(), obj.GetName())

	obj.GetStatus().SetReconcilePhase("Preparing")
	if err := updateStatus(); err != nil {
		if apierrors.IsConflict(err) {
			logger.Info("conflict during status update in Preparing phase, requeuing")
			return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	s.recorder.RecordEvent(obj, core_v1.EventTypeNormal, "Preparing", "Preparing resources")

	prep, result, err := s.reconciler.Prepare(ctx, s.client, obj)
	if err != nil {
		logger.Error(err, "failed preparation stage")
		s.recorder.RecordErrorEvent(obj, "Preparing", err)
		return result, err
	}

	deletionTimestamp := obj.GetDeletionTimestamp()
	finalizer := s.reconciler.Name()
	if f, ok := s.reconciler.(reconciler.FinalizerNamer); ok {
		finalizer = f.FinalizerName()
	}
	finalizerFunc := controllerutil.AddFinalizer
	var actions []action.Action
	if deletionTimestamp != nil {
		if controllerutil.ContainsFinalizer(obj, finalizer) {
			obj.GetStatus().SetReconcilePhase("EvaluatingDeletion")
			s.recorder.RecordEvent(obj, core_v1.EventTypeNormal, "EvaluatingDeletion", "Evaluating deletion of resources")
			if err = updateStatus(); err != nil {
				if apierrors.IsConflict(err) {
					logger.Info("conflict during status update in EvaluatingDeletion phase, requeuing")
					return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
				}
				return ctrl.Result{}, err
			}
			actions, result, err = s.reconciler.Delete(obj)
			if err != nil {
				logger.Error(err, "failed to calculate delete actions")
				s.recorder.RecordErrorEvent(obj, "EvaluatingDeletion", err)
				return result, err
			}
			finalizerFunc = controllerutil.RemoveFinalizer
		}
	} else {
		obj.GetStatus().SetReconcilePhase("EvaluatingUpdate")
		s.recorder.RecordEvent(obj, core_v1.EventTypeNormal, "EvaluatingUpdate", "Evaluating update of resources")
		if err = updateStatus(); err != nil {
			if apierrors.IsConflict(err) {
				logger.Info("conflict during status update in EvaluatingUpdate phase, requeuing")
				return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}
		actions, result, err = s.reconciler.Update(obj, prep)
		if err != nil {
			logger.Error(err, "failed to calculate update actions")
			s.recorder.RecordErrorEvent(obj, "EvaluatingUpdate", err)
			return result, err
		}
	}

	obj.GetStatus().SetReconcilePhase("DetectingUnreferenced")
	s.recorder.RecordEvent(obj, core_v1.EventTypeNormal, "DetectingUnreferenced", "Detecting unreferenced resources")
	if err = updateStatus(); err != nil {
		if apierrors.IsConflict(err) {
			logger.Info("conflict during status update in DetectingUnreferenced phase, requeuing")
			return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	actions, err = s.DetectUnreferenced(ctx, obj, actions)
	if err != nil {
		logger.Error(err, "unable to detect unreferenced resources")
		s.recorder.RecordErrorEvent(obj, "DetectUnreferenced", err)
		return ctrl.Result{}, err
	}

	obj.GetStatus().SetReconcilePhase("PerformingActions")
	s.recorder.RecordEvent(obj, core_v1.EventTypeNormal, "PerformingActions", "Performing %d actions", len(actions))
	if err := updateStatus(); err != nil {
		if apierrors.IsConflict(err) {
			logger.Info("conflict during status update in PerformingActions phase, requeuing")
			return ctrl.Result{RequeueAfter: 4 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	result, err = s.PerformActions(ctx, actions)
	if err != nil {
		logger.Error(err, "failed to perform reconciliation")
		s.recorder.RecordErrorEvent(obj, "PerformActions", err)
		return result, err
	}

	if finalizerFunc(obj, finalizer) {
		err := s.client.Update(ctx, obj)
		if err != nil {
			logger.Error(err, "failed to update finalizer")
			s.recorder.RecordErrorEvent(obj, "FinalizerUpdate", err)
			return ctrl.Result{}, err
		}
	}

	obj.GetStatus().SetReconcilePhase("Completed")
	s.recorder.RecordEvent(obj, core_v1.EventTypeNormal, "Completed", "Successfully synchronized %s/%s", obj.GetNamespace(), obj.GetName())
	return result, nil
}

func (s *Synchronizer[T, P]) PerformActions(ctx context.Context, actions []action.Action) (ctrl.Result, error) {
	for _, a := range actions {
		// TODO: s.addOwnerAnnotation(a)
		// Must handle IAMPolicyMember before adding owner annotation here
		err := a.Do(ctx, s.client, s.scheme)
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

	bldr := ctrl.NewControllerManagedBy(mgr).
		For(s.reconciler.New(), builder.WithPredicates(defaultEventFilter(mgr.GetScheme(), s.reconciler.New()))).
		WithOptions(opts).
		Named(s.reconciler.Name())

	for _, t := range s.reconciler.OwnedTypes() {
		bldr = bldr.Owns(t, builder.WithPredicates(ownedTypesEventFilter()))
	}

	for _, t := range s.reconciler.AdditionalTypes() {
		bldr = bldr.Watches(t,
			handler.EnqueueRequestsFromMapFunc(additionalTypesEnqueueFilter(mgr, s.ownerAnnotationKey)),
			builder.WithPredicates(defaultEventFilter(mgr.GetScheme(), s.reconciler.New())),
		)
	}
	return bldr.Complete(s)
}

func (s *Synchronizer[T, P]) DetectUnreferenced(ctx context.Context, owner T, actions []action.Action) ([]action.Action, error) {
	// TODO: can this be replaced with ApplySets?
	//  https://github.com/kubernetes/enhancements/tree/master/keps/sig-cli/3659-kubectl-apply-prune
	//  https://github.com/kubernetes-sigs/kro/blob/37ab9d6e3d1dc46bf9e7585238745462b4ab153b/pkg/applyset/applyset.go#L15-L20

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
			// TODO: this should check GVK instead of just the Go type
			//  This code is originally from Naiserator which justifies the use of Go types:
			//  https://github.com/nais/naiserator/blob/24be6dea44da7c29e9bf729334eec5afe8c2d593/pkg/synchronizer/synchronizer.go#L425-L427
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
		actions = append(actions, action.DeleteIfExists(existing, owner, func(obj client.Object) []meta_v1.Condition { return nil }, s.recorder))
	}

	return actions, nil
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

func ownedTypesEventFilter() predicate.Predicate {
	return predicate.Or(
		predicate.GenerationChangedPredicate{},
		predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
			oldStatus, err := statusBytes(e.ObjectOld)
			if err != nil {
				return false
			}
			newStatus, err := statusBytes(e.ObjectNew)
			if err != nil {
				return false
			}

			return !bytes.Equal(oldStatus, newStatus)
		}},
	)
}

func statusBytes(obj any) ([]byte, error) {
	v := struct {
		Status json.RawMessage `json:"status"`
	}{}
	b, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return v.Status, nil
}

func defaultEventFilter(scheme *runtime.Scheme, obj client.Object) predicate.Predicate {
	return predicate.Or(
		GenerationChangedPredicate{
			Scheme:   scheme,
			MainKind: findKind(obj, scheme),
		},
		predicate.AnnotationChangedPredicate{},
		predicate.LabelChangedPredicate{},
	)
}

func additionalTypesEnqueueFilter(mgr ctrl.Manager, annotationKey string) handler.MapFunc {
	return func(ctx context.Context, object client.Object) []reconcile.Request {
		if value, ok := object.GetAnnotations()[annotationKey]; ok {
			name, err := parseNamespacedName(value)
			if err != nil {
				mgr.GetLogger().Error(err, "unable to parse owner")
				return nil
			}

			return []reconcile.Request{{NamespacedName: name}}
		}
		return nil
	}
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
