package action

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/pkg/api"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type claim struct {
	action
}

func (a *claim) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme, ownerManager ownership.OwnerManager) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("Claim %s", typeName(a.obj)))

	gvk, err := apiutil.GVKForObject(a.obj, scheme)
	if err != nil {
		panic(fmt.Sprintf("Programmer Error: Unable to find GVK for object %v: %v", a.obj, err))
	}
	existing, err := scheme.New(gvk)
	if err != nil {
		return fmt.Errorf("internal error: %w", err)
	}
	existing.GetObjectKind().SetGroupVersionKind(gvk)

	key := client.ObjectKeyFromObject(a.obj)
	if err = c.Get(ctx, key, existing.(client.Object)); err != nil {
		return err
	}

	original := existing.DeepCopyObject().(client.Object)
	patchSource := client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})

	modified := existing.(client.Object)
	if a.obj.GetUID() != "" && modified.GetUID() != a.obj.GetUID() {
		return fmt.Errorf("refusing to claim replaced resource %s", key)
	}
	// Shared claims only add an annotation. If the planned object explicitly
	// makes this owner its controller, persist that reference as well, while
	// refusing to take over a resource controlled by somebody else.
	if metav1.IsControlledBy(a.obj, a.owner) {
		if controller := metav1.GetControllerOf(modified); controller != nil && controller.UID != a.owner.GetUID() {
			return fmt.Errorf("refusing to replace controller UID %s on resource %s", controller.UID, key)
		}
		if err := controllerutil.SetControllerReference(a.owner, modified, scheme); err != nil {
			return err
		}
	}
	if !ownerManager.HasOwnerAnnotation(modified, a.owner) {
		ownerManager.AddOwnerAnnotation(modified, a.owner)
	}
	if !equality.Semantic.DeepEqual(original.GetOwnerReferences(), modified.GetOwnerReferences()) ||
		!equality.Semantic.DeepEqual(original.GetAnnotations(), modified.GetAnnotations()) {
		if err = c.Patch(ctx, modified, patchSource); err != nil {
			return err
		}
		a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Claimed", "Claimed ownership of %s", describeObj(a.obj))
	}

	for _, condition := range a.conditionGetter(modified, scheme) {
		a.owner.GetStatus().SetCondition(condition)
	}

	return nil
}

func Claim(obj client.Object, owner api.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &claim{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
		},
	}
}
