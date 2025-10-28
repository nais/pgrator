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

type ConditionConfig struct {
	Type          string
	AvailablePath string
}

type ConditionGetter func(obj client.Object) []meta_v1.Condition

type Action interface {
	Do(context.Context, client.Client, *runtime.Scheme) error
}

type DoFunc func(context.Context, client.Client, *runtime.Scheme) error

var _ Action = DoFunc(nil)

func (d DoFunc) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
	return d(ctx, c, scheme)
}

func CreateOrUpdate(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter) Action {
	return DoFunc(func(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
		log := logf.FromContext(ctx)
		log.Info(fmt.Sprintf("CreateOrUpdate %s", liberator_scheme.TypeName(obj)))

		existing, err := scheme.New(obj.GetObjectKind().GroupVersionKind())
		if err != nil {
			return fmt.Errorf("internal error: %w", err)
		}

		key := client.ObjectKeyFromObject(obj)
		if err = c.Get(ctx, key, existing.(client.Object)); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}

			if err = c.Create(ctx, obj); err != nil {
				return err
			}
			return nil
		}

		if err = copyMeta(obj, existing); err != nil {
			return fmt.Errorf("copying metadata: %w", err)
		}

		if err = c.Update(ctx, obj); err != nil {
			return err
		}

		status := owner.GetStatus()
		if status.Conditions == nil {
			status.Conditions = new([]meta_v1.Condition)
		}

		for _, condition := range conditionGetter(obj) {
			meta.SetStatusCondition(status.Conditions, condition)
		}

		return nil
	})
}

func DeleteIfExists(obj client.Object, owner object.NaisObject, conditionGetter ConditionGetter) Action {
	return DoFunc(func(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
		log := logf.FromContext(ctx)
		log.Info(fmt.Sprintf("DeleteIfExists %s", liberator_scheme.TypeName(obj)))

		err := c.Delete(ctx, obj)
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}

		status := owner.GetStatus()
		if status.Conditions == nil {
			status.Conditions = new([]meta_v1.Condition)
		}

		for _, condition := range conditionGetter(obj) {
			meta.SetStatusCondition(status.Conditions, condition)
		}

		return nil
	})
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
