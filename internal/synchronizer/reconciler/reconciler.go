package reconciler

import (
	"context"

	"github.com/nais/pgrator/internal/synchronizer/action"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Reconciler[T client.Object, P any] interface {
	// Name returns a string identifying this reconciler
	// The name is used to create a suitable finalizer, and prefix annotations
	Name() string

	// New creates a new instance of the type being reconciled
	New() T

	// Prepare returns an object that can be used when doing updates
	Prepare(context.Context, client.Reader, T) (P, ctrl.Result, error)

	// Update returns the actions needed to handle an update of the reconciled object
	// This includes the first time the object is seen (aka Create)
	Update(T, P) ([]action.Action, ctrl.Result, error)

	// Delete returns the actions needed to handle the reconciled object being deleted
	Delete(T) ([]action.Action, ctrl.Result, error)
}
