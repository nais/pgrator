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
}

func (a *action) GetObject() client.Object {
	return a.obj
}

func (a *action) GetOwner() object.NaisObject {
	return a.owner
}

type createIfNotExists struct {
	action
}

func (a *createIfNotExists) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("CreateIfNotExists %s", typeName(a.obj)))

	var conditions []meta_v1.Condition

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
		conditions = a.conditionGetter(a.obj)
		a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Created", "Created %s %s", describeObj(a.obj))
	} else {
		conditions = a.conditionGetter(existing.(client.Object))
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
	a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Updated", "Updated %s", describeObj(a.obj))

	for _, condition := range a.conditionGetter(a.obj) {
		a.owner.GetStatus().SetCondition(condition)
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
		},
	}
}

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

	a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Deleted", "Deleted %s", describeObj(a.obj))

	for _, condition := range a.conditionGetter(a.obj) {
		a.owner.GetStatus().SetCondition(condition)
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
		},
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
		},
	}
}

type createOrRecreate struct {
	action
}

func (a *createOrRecreate) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("CreateOrRecreate %s", typeName(a.obj)))

	var conditions []meta_v1.Condition

	existing, err := scheme.New(a.obj.GetObjectKind().GroupVersionKind())
	if err != nil {
		return fmt.Errorf("internal error: %w", err)
	}

	key := client.ObjectKeyFromObject(a.obj)
	if err = c.Get(ctx, key, existing.(client.Object)); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}

		// Resource doesn't exist, create it
		if err = c.Create(ctx, a.obj); err != nil {
			return err
		}
		conditions = a.conditionGetter(a.obj)
		a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Created", "Created %s %s", describeObj(a.obj))
	} else {
		// Resource exists, delete and recreate it
		if err = c.Delete(ctx, existing.(client.Object)); err != nil {
			return fmt.Errorf("failed to delete existing resource: %w", err)
		}
		a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Deleted", "Deleted %s for recreation", describeObj(a.obj))

		// Create the new resource
		if err = c.Create(ctx, a.obj); err != nil {
			return fmt.Errorf("failed to recreate resource: %w", err)
		}
		conditions = a.conditionGetter(a.obj)
		a.recorder.RecordEvent(a.owner, v1.EventTypeNormal, "Recreated", "Recreated %s", describeObj(a.obj))
	}

	status := a.owner.GetStatus()
	if status.Conditions == nil {
		status.Conditions = new([]meta_v1.Condition)
	}

	for _, condition := range conditions {
		meta.SetStatusCondition(status.Conditions, condition)
	}

	return nil
}

func CreateOrRecreate(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &createOrRecreate{
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
