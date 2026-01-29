package action

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/object"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type createIfNotExists struct {
	action
}

func (a *createIfNotExists) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("CreateIfNotExists %s", typeName(a.obj)))

	var conditions []meta_v1.Condition

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
		if !apierrors.IsNotFound(err) {
			return err
		}

		if err = c.Create(ctx, a.obj); err != nil {
			return err
		}
		conditions = a.conditionGetter(a.obj, scheme)
		a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Created", "Created %s %s", describeObj(a.obj))
	} else {
		// Restore GVK on the retrieved object since the Kubernetes API server doesn't return TypeMeta.
		// This is necessary for condition getters that rely on GetObjectKind().GroupVersionKind().
		existing.(client.Object).GetObjectKind().SetGroupVersionKind(gvk)

		conditions = a.conditionGetter(existing.(client.Object), scheme)
		a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Exists", "%s already exists", describeObj(a.obj))
	}

	for _, condition := range conditions {
		a.owner.GetStatus().SetCondition(condition)
	}

	return nil
}

func CreateIfNotExists(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &createIfNotExists{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
		},
	}
}
