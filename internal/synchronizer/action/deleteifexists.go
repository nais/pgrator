package action

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/object"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type deleteIfExists struct {
	action
}

func (a *deleteIfExists) Do(ctx context.Context, c client.Client, _ *runtime.Scheme) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("DeleteIfExists %s", typeName(a.obj)))

	err := c.Delete(ctx, a.obj)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	// Ensure GVK is set for describeObj and condition getters that rely on GetObjectKind().GroupVersionKind()
	a.ensureGVK()
	a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Deleted", "Deleted %s", describeObj(a.obj))

	status := a.owner.GetStatus()
	if status.Conditions == nil {
		status.Conditions = new([]meta_v1.Condition)
	}

	for _, condition := range a.conditionGetter(a.obj) {
		meta.SetStatusCondition(status.Conditions, condition)
	}

	return nil
}

func DeleteIfExists(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &deleteIfExists{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
			gvk:             obj.GetObjectKind().GroupVersionKind(),
		},
	}
}
