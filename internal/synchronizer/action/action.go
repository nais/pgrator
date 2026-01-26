package action

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/object"
	"k8s.io/apimachinery/pkg/api/meta"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ConditionGetter func(obj client.Object) []meta_v1.Condition

type Action interface {
	Do(context.Context, client.Client, *runtime.Scheme) error
	GetObject() client.Object
	GetOwner() object.NaisObject
}

type action struct {
	obj             client.Object
	owner           object.NaisObject
	conditionGetter ConditionGetter
	recorder        events.Recorder
	gvk             schema.GroupVersionKind // Store GVK to restore before calling conditionGetter
}

func (a *action) GetObject() client.Object {
	return a.obj
}

func (a *action) GetOwner() object.NaisObject {
	return a.owner
}

// ensureGVK restores the GroupVersionKind on the object before it's used by condition getters.
// The Kubernetes API server doesn't return TypeMeta in responses, so we store the GVK
// at action construction time and restore it when needed.
func (a *action) ensureGVK() {
	if !a.gvk.Empty() {
		a.obj.GetObjectKind().SetGroupVersionKind(a.gvk)
	}
}

// ensureGVKFor restores the stored GroupVersionKind on the target object.
// Use this when the condition getter is called with a different object than a.obj
// (e.g., an existing object fetched from the API).
func (a *action) ensureGVKFor(obj client.Object) {
	if !a.gvk.Empty() {
		obj.GetObjectKind().SetGroupVersionKind(a.gvk)
	}
}

type noOp struct {
	action
}

func (n *noOp) Do(_ context.Context, _ client.Client, _ *runtime.Scheme) error { return nil }

func NoOp(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &noOp{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
			recorder:        recorder,
			gvk:             obj.GetObjectKind().GroupVersionKind(),
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
