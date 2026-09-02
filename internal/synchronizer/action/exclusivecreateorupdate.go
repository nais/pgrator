package action

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/pkg/api"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

type exclusiveCreateOrUpdate struct {
	action
}

// Do atomically creates an unclaimed object or updates an object controlled by
// the same owner. It never adopts ownerless objects or objects controlled by a
// different resource.
func (a *exclusiveCreateOrUpdate) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme, _ ownership.OwnerManager) error {
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
	created := false
	if err = c.Get(ctx, key, existingObj); apierrors.IsNotFound(err) {
		err = c.Create(ctx, a.obj)
		if err == nil {
			created = true
		} else if apierrors.IsAlreadyExists(err) {
			err = c.Get(ctx, key, existingObj)
		}
	}
	if err != nil {
		return err
	}

	if created {
		a.recorder.RecordEvent(a.owner, corev1.EventTypeNormal, "Created", "Created %s", describeObj(a.obj))
	} else {
		if !metav1.IsControlledBy(existingObj, a.owner) {
			controller := metav1.GetControllerOfNoCopy(existingObj)
			if controller == nil {
				return fmt.Errorf("%s already exists without a controller owner", describeObj(a.obj))
			}
			return fmt.Errorf("%s is already claimed by %q", describeObj(a.obj), controller.Name)
		}
		if err = copyMeta(a.obj, existingObj); err != nil {
			return fmt.Errorf("copying metadata: %w", err)
		}
		if err = c.Update(ctx, a.obj); err != nil {
			return err
		}
		a.recorder.RecordEvent(a.owner, corev1.EventTypeNormal, "Updated", "Updated %s", describeObj(a.obj))
	}

	conditionSource := existingObj
	if created {
		conditionSource = a.obj
	}
	for _, condition := range a.conditionGetter(conditionSource, scheme) {
		a.owner.GetStatus().SetCondition(condition)
	}
	return nil
}

// ExclusiveCreateOrUpdate creates obj if it is unclaimed and subsequently only
// updates it while owner remains its controller owner.
func ExclusiveCreateOrUpdate(obj client.Object, owner api.NaisObject, conditionGetter ConditionGetter, recorder events.Recorder) Action {
	return &exclusiveCreateOrUpdate{action: action{
		obj:             obj,
		owner:           owner,
		conditionGetter: conditionGetter,
		recorder:        recorder,
	}}
}
