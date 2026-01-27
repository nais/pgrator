package action

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/object"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type create struct {
	action
}

func (a *create) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("Create %s", typeName(a.obj)))

	var conditions []meta_v1.Condition

	// Create the new resource
	if err := c.Create(ctx, a.obj); err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}
	conditions = a.conditionGetter(a.obj, scheme)
	a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Created", "Created %s", describeObj(a.obj))

	status := a.owner.GetStatus()
	if status.Conditions == nil {
		status.Conditions = new([]meta_v1.Condition)
	}

	for _, condition := range conditions {
		meta.SetStatusCondition(status.Conditions, condition)
	}

	return nil
}

func Create(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &create{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
		},
	}
}
