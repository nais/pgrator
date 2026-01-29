package reconciler

import (
	"context"

	"github.com/nais/pgrator/internal/synchronizer/action"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type RelatedObjects interface {
	GetMatching(obj client.Object) client.Object
}

type Reconciler[T client.Object, P any] interface {
	// Name returns a string identifying this reconciler
	// The name is used to create a suitable finalizer, and prefix annotations
	Name() string

	// New creates a new instance of the type being reconciled
	New() T

	// OwnedTypes returns a list of types this reconciler owns
	// Such objects must reside in the same namespace as the main object, and have ownerReference set.
	// The main object will be requeued if any of these owned objects change, if the generation changes or
	// if the AdditionalPredicate (if any) returns true.
	OwnedTypes() []OwnedType

	// AdditionalTypes returns a list of additional types to watch
	// Such object must have the annotation "<name>/owner" set to "<namespace>:<name>" of the owning object.
	// They can reside in any namespace
	AdditionalTypes() []client.Object

	// Prepare returns an object that can be used when doing updates
	Prepare(context.Context, client.Reader, T) (P, ctrl.Result, error)

	// Update returns the actions needed to handle an update of the reconciled object
	// This includes the first time the object is seen (aka Create)
	Update(T, P, RelatedObjects) ([]action.Action, ctrl.Result, error)

	// Delete returns the actions needed to handle the reconciled object being deleted
	Delete(T, P, RelatedObjects) ([]action.Action, ctrl.Result, error)
}

type FinalizerNamer interface {
	// FinalizerName returns the finalizer name to use for this reconciler
	FinalizerName() string
}

type OwnedType struct {
	Type                client.Object
	AdditionalPredicate predicate.Predicate
}
