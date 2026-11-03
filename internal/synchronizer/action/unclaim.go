package action

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/pkg/api"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type unclaim struct {
	action
}

func (a *unclaim) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme, ownerManager ownership.OwnerManager) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("Unclaim %s", typeName(a.obj)))

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
		return fmt.Errorf("refusing to unclaim replaced resource %s", key)
	}
	ownerManager.RemoveOwnerAnnotation(modified, a.owner)
	if len(ownerManager.GetOwnerAnnotations(modified)) == 0 {
		preconditions := client.Preconditions{}
		if uid := modified.GetUID(); uid != "" {
			preconditions.UID = &uid
		}
		if version := modified.GetResourceVersion(); version != "" {
			preconditions.ResourceVersion = &version
		}
		if err = c.Delete(ctx, modified, preconditions); err != nil {
			return err
		}
		a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Deleted", "Deleted %s", describeObj(a.obj))
	} else {
		if err = c.Patch(ctx, modified, patchSource); err != nil {
			return err
		}
		a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Unclaimed", "Unclaimed ownership of %s", describeObj(a.obj))
	}

	for _, condition := range a.conditionGetter(modified, scheme) {
		a.owner.GetStatus().SetCondition(condition)
	}

	return nil
}

func Unclaim(obj client.Object, owner api.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &unclaim{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
		},
	}
}
