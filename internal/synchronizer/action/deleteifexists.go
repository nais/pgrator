package action

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/pkg/api"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type deleteIfExists struct {
	action
	options []client.DeleteOption
}

func (a *deleteIfExists) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme, _ ownership.OwnerManager) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("DeleteIfExists %s", typeName(a.obj)))

	opts := (&client.DeleteOptions{}).ApplyOptions(a.options)
	if uid := a.obj.GetUID(); uid != "" {
		// A cached object may have been replaced at the same name since the
		// deletion was planned. Never delete that replacement.
		if opts.Preconditions == nil {
			opts.Preconditions = &metav1.Preconditions{}
		} else {
			opts.Preconditions = opts.Preconditions.DeepCopy()
		}
		opts.Preconditions.UID = &uid
	}
	err := c.Delete(ctx, a.obj, opts)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Deleted", "Deleted %s", describeObj(a.obj))

	for _, condition := range a.conditionGetter(a.obj, scheme) {
		a.owner.GetStatus().SetCondition(condition)
	}

	return nil
}

func DeleteIfExists(obj client.Object, owner api.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder, options ...client.DeleteOption) Action {
	return &deleteIfExists{
		options: options,
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
		},
	}
}
