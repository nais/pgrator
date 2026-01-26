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

type createOrUpdate struct {
	action
}

func (a *createOrUpdate) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("CreateOrUpdate %s", typeName(a.obj)))

	existing, err := scheme.New(a.obj.GetObjectKind().GroupVersionKind())
	if err != nil {
		return fmt.Errorf("internal error: %w", err)
	}

	key := client.ObjectKeyFromObject(a.obj)
	if err = c.Get(ctx, key, existing.(client.Object)); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		if err = c.Create(ctx, a.obj); err != nil {
			return err
		}
		a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Created", "Created %s", describeObj(a.obj))
		return nil
	}

	if err = copyMeta(a.obj, existing); err != nil {
		return fmt.Errorf("copying metadata: %w", err)
	}

	if err = c.Update(ctx, a.obj); err != nil {
		return err
	}
	// Ensure GVK is set for condition getters that rely on GetObjectKind().GroupVersionKind()
	a.ensureGVK()
	a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Updated", "Updated %s", describeObj(a.obj))

	status := a.owner.GetStatus()
	if status.Conditions == nil {
		status.Conditions = new([]meta_v1.Condition)
	}

	for _, condition := range a.conditionGetter(a.obj) {
		meta.SetStatusCondition(status.Conditions, condition)
	}

	return nil
}

func CreateOrUpdate(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &createOrUpdate{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
			gvk:             obj.GetObjectKind().GroupVersionKind(),
		},
	}
}
