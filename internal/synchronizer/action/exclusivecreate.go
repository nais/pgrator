package action

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/pkg/api"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type exclusiveCreate struct {
	action
}

// Do creates an object only when no other owner has claimed its deterministic
// name. Kubernetes Create makes this check atomic across concurrent reconcilers.
func (a *exclusiveCreate) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme, _ ownership.OwnerManager) error {
	gvk, err := apiutil.GVKForObject(a.obj, scheme)
	if err != nil {
		return fmt.Errorf("looking up GVK: %w", err)
	}
	existing, err := scheme.New(gvk)
	if err != nil {
		return fmt.Errorf("creating object for lookup: %w", err)
	}

	existingObj := existing.(client.Object)
	key := client.ObjectKeyFromObject(a.obj)
	err = c.Get(ctx, key, existingObj)
	if apierrors.IsNotFound(err) {
		err = c.Create(ctx, a.obj)
		if err == nil {
			return nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		if err = c.Get(ctx, key, existingObj); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if metav1.IsControlledBy(existingObj, a.owner) {
		return nil
	}
	controller := metav1.GetControllerOfNoCopy(existingObj)
	if controller == nil {
		return fmt.Errorf("%s already exists without a controller owner", describeObj(a.obj))
	}
	return fmt.Errorf("%s is already claimed by %q", describeObj(a.obj), controller.Name)
}

// ExclusiveCreate creates obj if its deterministic name has not already been
// claimed. obj must have a controller owner reference to owner.
func ExclusiveCreate(obj client.Object, owner api.NaisObject) Action {
	return &exclusiveCreate{action: action{obj: obj, owner: owner}}
}
