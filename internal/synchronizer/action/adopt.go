package action

import (
	"context"
	"fmt"
	"slices"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/pkg/api"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type adopt struct {
	action
	previousOwner  api.NaisObject
	previousOwners ownership.OwnerManager
}

// Adopt transfers an existing resource's controller and ownership annotations in
// one metadata-only patch. It never creates or deletes resources or applies a
// desired spec. The caller must stop the previous reconciler's writes/pruning
// before using this action; transferring ownership is not a controller lock.
func Adopt(obj client.Object, previousOwner, owner api.NaisObject, previousOwners ownership.OwnerManager, get ConditionGetter, recorder events.Recorder) Action {
	return &adopt{
		action:        action{obj: obj, owner: owner, conditionGetter: get, recorder: recorder, skipOwnership: true},
		previousOwner: previousOwner, previousOwners: previousOwners,
	}
}

func (a *adopt) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme, owners ownership.OwnerManager) error {
	if a.obj.GetUID() == "" || a.owner.GetUID() == "" || a.previousOwner.GetUID() == "" {
		return fmt.Errorf("adoption requires resource and owner UIDs")
	}
	if a.owner.GetUID() == a.previousOwner.GetUID() || a.obj.GetNamespace() == "" ||
		a.obj.GetNamespace() != a.owner.GetNamespace() || a.obj.GetNamespace() != a.previousOwner.GetNamespace() {
		return fmt.Errorf("adoption requires distinct owners in the resource namespace")
	}
	current := a.obj.DeepCopyObject().(client.Object)
	if err := c.Get(ctx, client.ObjectKeyFromObject(a.obj), current); err != nil {
		return err
	}
	if current.GetUID() != a.obj.GetUID() || current.GetDeletionTimestamp() != nil {
		return fmt.Errorf("refusing to adopt a replaced or deleting resource %s", client.ObjectKeyFromObject(current))
	}
	previous, err := controllerReference(a.previousOwner, scheme)
	if err != nil {
		return err
	}
	next, err := controllerReference(a.owner, scheme)
	if err != nil {
		return err
	}
	controller := metav1.GetControllerOf(current)
	fromPrevious := sameController(controller, previous)
	if !fromPrevious && !sameController(controller, next) {
		return fmt.Errorf("refusing to adopt resource %s with an unexpected controller", client.ObjectKeyFromObject(current))
	}
	if err := verifyAdoptionOwner(ctx, c, a.owner); err != nil {
		return err
	}
	if fromPrevious {
		if err := verifyAdoptionOwner(ctx, c, a.previousOwner); err != nil {
			return err
		}
	}
	original := current.DeepCopyObject().(client.Object)
	if fromPrevious {
		refs := slices.DeleteFunc(slices.Clone(current.GetOwnerReferences()), func(ref metav1.OwnerReference) bool {
			return ref.Controller != nil && *ref.Controller && sameController(&ref, previous)
		})
		current.SetOwnerReferences(refs)
	}
	if err := controllerutil.SetControllerReference(a.owner, current, scheme); err != nil {
		return err
	}
	a.previousOwners.RemoveOwnerAnnotation(current, a.previousOwner)
	owners.AddOwnerAnnotation(current, a.owner)
	if !equality.Semantic.DeepEqual(original.GetOwnerReferences(), current.GetOwnerReferences()) ||
		!equality.Semantic.DeepEqual(original.GetAnnotations(), current.GetAnnotations()) {
		if err := c.Patch(ctx, current, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
		a.recorder.RecordEvent(a.owner, corev1.EventTypeNormal, "Adopted", "Adopted %s without changing its spec", describeObj(current))
	}
	for _, condition := range a.conditionGetter(current, scheme) {
		a.owner.GetStatus().SetCondition(condition)
	}
	return nil
}

func controllerReference(owner client.Object, scheme *runtime.Scheme) (metav1.OwnerReference, error) {
	gvk, err := apiutil.GVKForObject(owner, scheme)
	if err != nil {
		return metav1.OwnerReference{}, err
	}
	return metav1.OwnerReference{APIVersion: gvk.GroupVersion().String(), Kind: gvk.Kind, Name: owner.GetName(), UID: owner.GetUID()}, nil
}

func sameController(actual *metav1.OwnerReference, expected metav1.OwnerReference) bool {
	return actual != nil && actual.UID == expected.UID && actual.Name == expected.Name && actual.Kind == expected.Kind && actual.APIVersion == expected.APIVersion
}

func verifyAdoptionOwner(ctx context.Context, c client.Client, planned client.Object) error {
	current := planned.DeepCopyObject().(client.Object)
	if err := c.Get(ctx, client.ObjectKeyFromObject(planned), current); err != nil {
		return err
	}
	if current.GetUID() != planned.GetUID() || current.GetDeletionTimestamp() != nil {
		return fmt.Errorf("adoption owner %s was replaced or is deleting", client.ObjectKeyFromObject(planned))
	}
	return nil
}
