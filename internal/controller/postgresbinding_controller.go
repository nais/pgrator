package controller

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rcbinding "github.com/nais/pgrator/internal/resourcecreator/binding"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	core_v1 "k8s.io/api/core/v1"
	networking_v1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PostgresBindingReconciler reconciles a nais.io/v1 PostgresBinding into a
// CloudNativePG DatabaseRole with an operator-issued client certificate, a
// connection Secret, and NetworkPolicies that open the path to the connection
// pooler.
type PostgresBindingReconciler struct {
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

var _ reconciler.Reconciler[*v1.PostgresBinding, PostgresBindingPreparedData] = &PostgresBindingReconciler{}

// PostgresBindingPreparedData contains data prepared during the Prepare phase.
type PostgresBindingPreparedData struct{}

func (r *PostgresBindingReconciler) Name() string {
	return "postgresbinding.nais.io"
}

func (r *PostgresBindingReconciler) New() *v1.PostgresBinding {
	return &v1.PostgresBinding{}
}

func (r *PostgresBindingReconciler) OwnedTypes() []reconciler.OwnedType {
	return []reconciler.OwnedType{
		{Type: &cnpgv1.DatabaseRole{}},
		{Type: &core_v1.ConfigMap{}},
		{Type: &core_v1.Secret{}},
		{Type: &networking_v1.NetworkPolicy{}},
	}
}

func (r *PostgresBindingReconciler) AdditionalTypes() []client.Object {
	return nil
}

// Prepare verifies that the referenced Postgres exists in the same namespace.
//
// Bindings are namespace-local by design: the namespace is the team boundary, so
// resolving the Postgres by name in the binding's own namespace is what enforces
// that a team cannot bind to another team's database.
func (r *PostgresBindingReconciler) Prepare(ctx context.Context, reader client.Reader, obj *v1.PostgresBinding) (PostgresBindingPreparedData, ctrl.Result, error) {
	postgres := &v1.Postgres{}
	key := client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.Spec.Postgres}
	if err := reader.Get(ctx, key, postgres); err != nil {
		if apierrors.IsNotFound(err) {
			if !obj.GetDeletionTimestamp().IsZero() {
				return PostgresBindingPreparedData{}, ctrl.Result{}, nil
			}
			return PostgresBindingPreparedData{}, ctrl.Result{}, fmt.Errorf(
				"no Postgres named %q in namespace %q", obj.Spec.Postgres, obj.GetNamespace())
		}
		return PostgresBindingPreparedData{}, ctrl.Result{}, fmt.Errorf("getting Postgres %q: %w", obj.Spec.Postgres, err)
	}

	return PostgresBindingPreparedData{}, ctrl.Result{}, nil
}

func (r *PostgresBindingReconciler) Update(obj *v1.PostgresBinding, _ PostgresBindingPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	lock, err := rcbinding.CreateRoleLock(r.Scheme, obj)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating role lock: %w", err)
	}
	actions := []action.Action{action.ExclusiveCreate(lock, obj)}

	role, err := rcbinding.CreateDatabaseRole(r.Scheme, obj)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating DatabaseRole spec: %w", err)
	}
	actions = append(actions, action.ExclusiveCreateOrUpdate(role, obj, existsConditionGetter, r.Recorder))

	configSecret, err := rcbinding.CreateConfigSecret(r.Scheme, obj)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating config Secret spec: %w", err)
	}
	actions = append(actions, action.ExclusiveCreateOrUpdate(configSecret, obj, existsConditionGetter, r.Recorder))

	netpol, err := rcbinding.CreateNetworkPolicy(r.Scheme, obj)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating NetworkPolicy spec: %w", err)
	}
	actions = append(actions, action.ExclusiveCreateOrUpdate(netpol, obj, existsConditionGetter, r.Recorder))

	egressNetpol, err := rcbinding.CreateEgressNetworkPolicy(r.Scheme, obj)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating egress NetworkPolicy spec: %w", err)
	}
	actions = append(actions, action.ExclusiveCreateOrUpdate(egressNetpol, obj, existsConditionGetter, r.Recorder))

	return actions, ctrl.Result{}, nil
}

// Delete relies on ownerReference garbage collection. Everything a binding creates
// lives in the binding's own namespace, so there is nothing to clean up by hand.
//
// This is destructive by design: read and readwrite DatabaseRoles carry
// databaseRoleReclaimPolicy: delete, so removing a binding runs DROP ROLE and
// revokes that workload's access immediately. That is the intent — a binding is the
// grant, so withdrawing it must withdraw the access.
//
// Admin bindings retain the durable owner role when their DatabaseRole is deleted,
// while CloudNativePG garbage-collects the client certificate.
//
// DROP ROLE fails if the role still owns objects. Read and readwrite roles never
// create objects, so this does not bite today, but a future writable role that owns
// tables would need ownership reassigned before its binding can be deleted.
func (r *PostgresBindingReconciler) Delete(_ *v1.PostgresBinding, _ PostgresBindingPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	return nil, ctrl.Result{}, nil
}
