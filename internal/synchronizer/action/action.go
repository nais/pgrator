package action

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/object"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"k8s.io/apimachinery/pkg/api/meta"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ConditionGetter func(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition

type Action interface {
	Do(context.Context, client.Client, *runtime.Scheme, ownership.OwnerManager) error
	GetObject() client.Object
	GetOwner() object.NaisObject
}

type action struct {
	obj             client.Object
	owner           object.NaisObject
	conditionGetter ConditionGetter
	recorder        events.Recorder
}

func (a *action) GetObject() client.Object {
	return a.obj
}

func (a *action) GetOwner() object.NaisObject {
	return a.owner
}

type noOp struct {
	action
}

func (n *noOp) Do(_ context.Context, _ client.Client, _ *runtime.Scheme, _ ownership.OwnerManager) error {
	return nil
}

func NoOp(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &noOp{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
		},
	}
}

func copyMeta(dst, src runtime.Object) error {
	srcacc, err := meta.Accessor(src)
	if err != nil {
		return err
	}

	dstacc, err := meta.Accessor(dst)
	if err != nil {
		return err
	}

	// Must always be present when updating a resource
	dstacc.SetResourceVersion(srcacc.GetResourceVersion())
	dstacc.SetUID(srcacc.GetUID())
	dstacc.SetSelfLink(srcacc.GetSelfLink())

	return err
}

func describeObj(obj client.Object) string {
	kind := obj.GetObjectKind().GroupVersionKind().Kind
	namespace := obj.GetNamespace()
	name := obj.GetName()

	return fmt.Sprintf("%s %s/%s", kind, namespace, name)
}

// Human-readable description of a Kubernetes object metadata.
func typeName(resource runtime.Object) string {
	var kind, name, namespace string
	typ, err := meta.TypeAccessor(resource)
	if err == nil {
		kind = typ.GetKind()
	}
	obj, err := meta.Accessor(resource)
	if err == nil {
		name = obj.GetName()
		namespace = obj.GetNamespace()
	}
	return fmt.Sprintf("resource '%s' named '%s' in namespace '%s'", kind, name, namespace)
}
