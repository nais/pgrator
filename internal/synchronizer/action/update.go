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

type update struct {
	action
}

func (a *update) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme, _ ownership.OwnerManager) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("Update %s", typeName(a.obj)))

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

	if err = copyMeta(a.obj, existing); err != nil {
		return fmt.Errorf("copying metadata: %w", err)
	}

	if err = c.Update(ctx, a.obj); err != nil {
		return err
	}
	a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Updated", "Updated %s", describeObj(a.obj))

	for _, condition := range a.conditionGetter(a.obj, scheme) {
		a.owner.GetStatus().SetCondition(condition)
	}

	return nil
}

func Update(obj client.Object, owner api.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &update{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
		},
	}
}
