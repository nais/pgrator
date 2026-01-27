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

type update struct {
	action
}

func (a *update) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("Update %s", typeName(a.obj)))

	if err := c.Update(ctx, a.obj); err != nil {
		return err
	}
	a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Updated", "Updated %s", describeObj(a.obj))

	status := a.owner.GetStatus()
	if status.Conditions == nil {
		status.Conditions = new([]meta_v1.Condition)
	}

	for _, condition := range a.conditionGetter(a.obj, scheme) {
		meta.SetStatusCondition(status.Conditions, condition)
	}

	return nil
}

func Update(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &update{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
		},
	}
}
