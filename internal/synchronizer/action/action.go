package action

import (
	"context"
	"fmt"

	liberator_scheme "github.com/nais/liberator/pkg/scheme"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Action interface {
	Do(context.Context, client.Client, *runtime.Scheme) error
}

type DoFunc func(context.Context, client.Client, *runtime.Scheme) error

var _ Action = DoFunc(nil)

func (d DoFunc) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
	return d(ctx, c, scheme)
}

func CreateOrUpdate(obj client.Object) Action {
	return DoFunc(func(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
		log := logf.FromContext(ctx)
		log.Info(fmt.Sprintf("CreateOrUpdate %s", liberator_scheme.TypeName(obj)))

		existing, err := scheme.New(obj.GetObjectKind().GroupVersionKind())
		if err != nil {
			return fmt.Errorf("internal error: %w", err)
		}

		key := client.ObjectKeyFromObject(obj)
		if err := c.Get(ctx, key, existing.(client.Object)); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}

			if err := c.Create(ctx, obj); err != nil {
				return err
			}
			return nil
		}

		if err := c.Update(ctx, obj); err != nil {
			return err
		}
		return nil
	})
}

func DeleteIfExists(obj client.Object) Action {
	return DoFunc(func(ctx context.Context, c client.Client, scheme *runtime.Scheme) error {
		log := logf.FromContext(ctx)
		log.Info(fmt.Sprintf("DeleteIfExists %s", liberator_scheme.TypeName(obj)))

		err := c.Delete(ctx, obj)
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		return nil
	})
}
