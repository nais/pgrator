package action

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/object"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
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

	key := client.ObjectKeyFromObject(a.obj)
	if err = c.Get(ctx, key, existing.(client.Object)); err != nil {
		return err
	}

	original := existing.DeepCopyObject().(client.Object)
	patchSource := client.MergeFrom(original)

	modified := existing.(client.Object)
	ownerManager.AddOwnerAnnotation(modified, a.owner)

	if err = c.Patch(ctx, modified, patchSource); err != nil {
		return err
	}
	a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Claimed", "Claimed ownership of %s", describeObj(a.obj))

	for _, condition := range a.conditionGetter(a.obj, scheme) {
		a.owner.GetStatus().SetCondition(condition)
	}

	return nil
}

func Claim(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &claim{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
		},
	}
}
