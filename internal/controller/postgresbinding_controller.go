package controller

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rcbinding "github.com/nais/pgrator/internal/resourcecreator/binding"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
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
// CloudNativePG DatabaseRole with an operator-issued client certificate, the two
// Secrets a workload needs in order to connect, and a NetworkPolicy that opens the
// path to the connection pooler.
type PostgresBindingReconciler struct {
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

var _ reconciler.Reconciler[*v1.PostgresBinding, PostgresBindingPreparedData] = &PostgresBindingReconciler{}

// PostgresBindingPreparedData contains data prepared during the Prepare phase.
type PostgresBindingPreparedData struct {
	// CACert is the cluster CA certificate, copied out of the CloudNativePG CA
	// Secret so it can be handed to the workload without the CA private key.
	CACert []byte
}

func (r *PostgresBindingReconciler) Name() string {
	return "postgresbinding.nais.io"
}

func (r *PostgresBindingReconciler) New() *v1.PostgresBinding {
	return &v1.PostgresBinding{}
}

func (r *PostgresBindingReconciler) OwnedTypes() []reconciler.OwnedType {
	return []reconciler.OwnedType{
		{Type: &cnpgv1.DatabaseRole{}},
		{Type: &core_v1.Secret{}},
		{Type: &networking_v1.NetworkPolicy{}},
	}
}

func (r *PostgresBindingReconciler) AdditionalTypes() []client.Object {
	return nil
}

// Prepare verifies that the referenced Postgres exists in the same namespace and
// reads the cluster CA certificate.
//
// Bindings are namespace-local by design: the namespace is the team boundary, so
// resolving the Postgres by name in the binding's own namespace is what enforces
// that a team cannot bind to another team's database.
func (r *PostgresBindingReconciler) Prepare(ctx context.Context, reader client.Reader, obj *v1.PostgresBinding) (PostgresBindingPreparedData, ctrl.Result, error) {
	postgres := &v1.Postgres{}
	key := client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.Spec.Postgres}
	if err := reader.Get(ctx, key, postgres); err != nil {
		if apierrors.IsNotFound(err) {
			return PostgresBindingPreparedData{}, ctrl.Result{}, fmt.Errorf(
				"no Postgres named %q in namespace %q", obj.Spec.Postgres, obj.GetNamespace())
		}
		return PostgresBindingPreparedData{}, ctrl.Result{}, fmt.Errorf("getting Postgres %q: %w", obj.Spec.Postgres, err)
	}

	caSecret := &core_v1.Secret{}
	caKey := client.ObjectKey{
		Namespace: obj.GetNamespace(),
		Name:      rccnpg.CASecretNameFor(obj.Spec.Postgres),
	}
	if err := reader.Get(ctx, caKey, caSecret); err != nil {
		if apierrors.IsNotFound(err) {
			// The operator has not finished provisioning the cluster yet. Wait
			// rather than fail: this is the normal state right after creation.
			return PostgresBindingPreparedData{}, ctrl.Result{Requeue: true}, nil
		}
		return PostgresBindingPreparedData{}, ctrl.Result{}, fmt.Errorf("getting CA Secret %q: %w", caKey.Name, err)
	}

	caCert, ok := caSecret.Data["ca.crt"]
	if !ok || len(caCert) == 0 {
		return PostgresBindingPreparedData{}, ctrl.Result{}, fmt.Errorf("CA Secret %q has no ca.crt", caKey.Name)
	}

	return PostgresBindingPreparedData{CACert: caCert}, ctrl.Result{}, nil
}

func (r *PostgresBindingReconciler) Update(obj *v1.PostgresBinding, preparedData PostgresBindingPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	var actions []action.Action

	// Admin bindings deliberately produce no DatabaseRole: the owner role is
	// created and retained by the Postgres resource, and must survive the binding.
	role, err := rcbinding.CreateDatabaseRole(r.Scheme, obj)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating DatabaseRole spec: %w", err)
	}
	if role != nil {
		actions = append(actions, action.CreateOrUpdate(role, obj, existsConditionGetter, r.Recorder))
	}

	configSecret, err := rcbinding.CreateConfigSecret(r.Scheme, obj)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating config Secret spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(configSecret, obj, existsConditionGetter, r.Recorder))

	caSecret, err := rcbinding.CreateCASecret(r.Scheme, obj, preparedData.CACert)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating CA Secret spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(caSecret, obj, existsConditionGetter, r.Recorder))

	netpol, err := rcbinding.CreateNetworkPolicy(r.Scheme, obj)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating NetworkPolicy spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(netpol, obj, existsConditionGetter, r.Recorder))

	return actions, ctrl.Result{}, nil
}

// Delete relies on ownerReference garbage collection. Everything a binding creates
// lives in the binding's own namespace, so there is nothing to clean up by hand.
//
// Note that deleting the DatabaseRole only drops the database role; any objects it
// owns are left behind, and CloudNativePG will fail the DROP ROLE if the role still
// owns anything. Read and readwrite roles never own objects, so this is fine today.
func (r *PostgresBindingReconciler) Delete(_ *v1.PostgresBinding, _ PostgresBindingPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	return nil, ctrl.Result{}, nil
}
