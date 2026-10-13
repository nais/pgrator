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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type create struct {
	action
}

func (a *create) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme, _ ownership.OwnerManager) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("Create %s", typeName(a.obj)))

	// Create the new resource
	if err := c.Create(ctx, a.obj); err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}
	a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Created", "Created %s", describeObj(a.obj))

	for _, condition := range a.conditionGetter(a.obj, scheme) {
		a.owner.GetStatus().SetCondition(condition)
	}

	return nil
}

func Create(obj client.Object, owner api.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &create{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
		},
	}
}
