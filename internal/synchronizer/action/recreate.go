package action

import (
	"context"
	"fmt"
	"time"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/object"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type recreate struct {
	action
}

func (a *recreate) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme, _ ownership.OwnerManager) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("Recreate %s", typeName(a.obj)))

	key := client.ObjectKeyFromObject(a.obj)
	// Resource exists, delete and recreate it
	if err := c.Delete(ctx, a.obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete existing resource: %w", err)
	}
	a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Deleted", "Deleted %s for recreation", describeObj(a.obj))

	// Wait for deletion to complete
	for {
		gvk, err := apiutil.GVKForObject(a.obj, scheme)
		if err != nil {
			panic(fmt.Sprintf("Programmer Error: Unable to find GVK for object %v: %v", a.obj, err))
		}
		shouldBeDeleted, err := scheme.New(gvk)
		if err != nil {
			return fmt.Errorf("internal error: %w", err)
		}

		err = c.Get(ctx, key, shouldBeDeleted.(client.Object))
		if apierrors.IsNotFound(err) {
			break
		} else if err != nil {
			return fmt.Errorf("failed to get existing resource: %w", err)
		}

		// wait before we try again
		time.Sleep(200 * time.Millisecond)
	}

	// Create the new resource
	if err := c.Create(ctx, a.obj); err != nil {
		return fmt.Errorf("failed to recreate resource: %w", err)
	}
	a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Recreated", "Recreated %s", describeObj(a.obj))

	for _, condition := range a.conditionGetter(a.obj, scheme) {
		a.owner.GetStatus().SetCondition(condition)
	}

	return nil
}

func Recreate(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &recreate{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
		},
	}
}
