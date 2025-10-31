package action

import (
	"context"
	"fmt"

	liberator_scheme "github.com/nais/liberator/pkg/scheme"
	"github.com/nais/pgrator/internal/synchronizer/object"
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
	log.Info(fmt.Sprintf("CreateIfNotExists %s", liberator_scheme.TypeName(a.obj)))

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
	} else {
		conditions = a.conditionGetter(existing.(client.Object))
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

func CreateIfNotExists(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter) Action {
	return &createIfNotExists{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
		},
	}
}

type createOrUpdate struct {
	action
}

func (a *createOrUpdate) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("CreateOrUpdate %s", liberator_scheme.TypeName(a.obj)))

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
		return nil
	}

	if err = copyMeta(a.obj, existing); err != nil {
		return fmt.Errorf("copying metadata: %w", err)
	}

	if err = c.Update(ctx, a.obj); err != nil {
		return err
	}

	status := a.owner.GetStatus()
	if status.Conditions == nil {
		status.Conditions = new([]meta_v1.Condition)
	}

	for _, condition := range a.conditionGetter(a.obj) {
		meta.SetStatusCondition(status.Conditions, condition)
	}

	return nil
}

func CreateOrUpdate(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter) Action {
	return &createOrUpdate{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
		},
	}
}

type deleteIfExists struct {
	action
}

func (a *deleteIfExists) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
	log := logf.FromContext(ctx)
	log.Info(fmt.Sprintf("DeleteIfExists %s", liberator_scheme.TypeName(a.obj)))

	err := c.Delete(ctx, a.obj)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	status := a.owner.GetStatus()
	if status.Conditions == nil {
		status.Conditions = new([]meta_v1.Condition)
	}

	for _, condition := range a.conditionGetter(a.obj) {
		meta.SetStatusCondition(status.Conditions, condition)
	}

	return nil
}

func DeleteIfExists(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter) Action {
	return &deleteIfExists{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
		},
	}
}

type noOp struct {
	action
}

func (n *noOp) Do(_ context.Context, _ client.Client, _ *runtime.Scheme) error {
	return nil
}

func NoOp(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter) Action {
	return &noOp{
		action: action{
			obj:             obj,
			owner:           owner,
			conditionGetter: conditionGetter,
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
