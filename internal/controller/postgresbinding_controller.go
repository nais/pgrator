package controller

import (
	"context"
	"fmt"
	"reflect"

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

// PostgresBindingPreparedData contains the selected instance and, when all CNPG
// source material is present, an internally consistent credential snapshot.
type PostgresBindingPreparedData struct {
	Instance string
	Snapshot *bindingSnapshot
}

type bindingSnapshot struct {
	CACertificate []byte
	Credentials   map[v1.PostgresBindingCredential]rcbinding.CredentialMaterial
}

func (r *PostgresBindingReconciler) Name() string {
	return "postgresbinding.nais.io"
}

func (r *PostgresBindingReconciler) New() *v1.PostgresBinding {
	return &v1.PostgresBinding{}
}

func (r *PostgresBindingReconciler) OwnedTypes() []reconciler.OwnedType {
	return []reconciler.OwnedType{
		{
			Type: &cnpgv1.DatabaseRole{},
			AdditionalPredicate: predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
				oldRole, oldOK := e.ObjectOld.(*cnpgv1.DatabaseRole)
				newRole, newOK := e.ObjectNew.(*cnpgv1.DatabaseRole)
				return oldOK && newOK && !reflect.DeepEqual(oldRole.Status, newRole.Status)
			}},
		},
		{Type: &core_v1.Secret{}},
		{Type: &networking_v1.NetworkPolicy{}},
	}
}

func (r *PostgresBindingReconciler) AdditionalTypes() []client.Object {
	return nil
}

const postgresBindingPostgresIndex = "spec.postgres"

func (r *PostgresBindingReconciler) Indexes() []reconciler.Index {
	return []reconciler.Index{{
		Object: &v1.PostgresBinding{},
		Field:  postgresBindingPostgresIndex,
		ExtractValue: func(object client.Object) []string {
			binding, ok := object.(*v1.PostgresBinding)
			if !ok || binding.Spec.Postgres == "" {
				return nil
			}
			return []string{binding.Spec.Postgres}
		},
	}}
}

func (r *PostgresBindingReconciler) RelationshipWatches() []reconciler.RelationshipWatch {
	return []reconciler.RelationshipWatch{
		{
			Type:      &v1.Postgres{},
			Map:       r.bindingsForPostgres,
			Predicate: predicate.GenerationChangedPredicate{},
		},
		{
			Type:      &core_v1.Secret{},
			Map:       r.bindingsForSourceSecret,
			Predicate: sourceSecretEventFilter(),
		},
	}
}

func sourceSecretEventFilter() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		DeleteFunc: func(event.DeleteEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldSecret, oldOK := e.ObjectOld.(*core_v1.Secret)
			newSecret, newOK := e.ObjectNew.(*core_v1.Secret)
			return oldOK && newOK && !reflect.DeepEqual(oldSecret.Data, newSecret.Data)
		},
	}
}

func (r *PostgresBindingReconciler) bindingsForPostgres(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
	postgres, ok := object.(*v1.Postgres)
	if !ok {
		return nil, nil
	}
	bindings := &v1.PostgresBindingList{}
	if err := reader.List(ctx, bindings, client.InNamespace(postgres.GetNamespace()), client.MatchingFields{postgresBindingPostgresIndex: postgres.GetName()}); err != nil {
		return nil, fmt.Errorf("listing PostgresBindings for Postgres %q: %w", postgres.GetName(), err)
	}
	return bindingRequests(bindings.Items), nil
}

func (r *PostgresBindingReconciler) bindingsForSourceSecret(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
	secret, ok := object.(*core_v1.Secret)
	if !ok {
		return nil, nil
	}
	bindings := &v1.PostgresBindingList{}
	if err := reader.List(ctx, bindings, client.InNamespace(secret.GetNamespace())); err != nil {
		return nil, fmt.Errorf("listing PostgresBindings for Secret %q: %w", secret.GetName(), err)
	}

	requests := make([]reconcile.Request, 0)
	for i := range bindings.Items {
		binding := &bindings.Items[i]
		sourceNames, err := bindingSourceSecretNames(ctx, reader, binding)
		if err != nil {
			return nil, err
		}
		for _, sourceName := range sourceNames {
			if secret.GetName() == sourceName {
				requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(binding)})
				break
			}
		}
	}
	return requests, nil
}

func bindingSourceSecretNames(ctx context.Context, reader client.Reader, binding *v1.PostgresBinding) ([]string, error) {
	postgres := &v1.Postgres{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: binding.GetNamespace(), Name: binding.Spec.Postgres}, postgres); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting Postgres %q: %w", binding.Spec.Postgres, err)
	}
	activeInstance := postgres.Spec.ActiveInstance
	if activeInstance == "" {
		activeInstance = postgres.GetName()
	}
	instance := &v1.PostgresInstance{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: binding.GetNamespace(), Name: activeInstance}, instance); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting active PostgresInstance %q: %w", activeInstance, err)
	}
	if instance.Spec.Postgres != postgres.GetName() {
		return nil, nil
	}
	cluster := &cnpgv1.Cluster{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: binding.GetNamespace(), Name: rccnpg.ClusterNameFor(instance.GetName())}, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting CNPG Cluster for PostgresInstance %q: %w", instance.GetName(), err)
	}

	names := []string{cluster.GetClientCASecretName()}
	for _, credential := range binding.Spec.Credentials {
		roleName, err := certificateRoleName(ctx, reader, binding, instance.GetName(), cluster.GetName(), credential)
		if err != nil {
			return nil, err
		}
		if roleName != "" {
			names = append(names, (&cnpgv1.DatabaseRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}}).GetClientCertSecretName())
		}
	}
	return names, nil
}

func bindingRequests(bindings []v1.PostgresBinding) []reconcile.Request {
	requests := make([]reconcile.Request, 0, len(bindings))
	for i := range bindings {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&bindings[i])})
	}
	return requests
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

	activeInstance := postgres.Spec.ActiveInstance
	if activeInstance == "" {
		activeInstance = postgres.GetName()
	}

	instance := &v1.PostgresInstance{}
	instanceKey := client.ObjectKey{Namespace: obj.GetNamespace(), Name: activeInstance}
	if err := reader.Get(ctx, instanceKey, instance); err != nil {
		return PostgresBindingPreparedData{}, ctrl.Result{}, fmt.Errorf("getting active PostgresInstance %q: %w", activeInstance, err)
	}
	if instance.Spec.Postgres != postgres.GetName() {
		return PostgresBindingPreparedData{}, ctrl.Result{}, fmt.Errorf("PostgresInstance %q belongs to Postgres %q, not %q", instance.GetName(), instance.Spec.Postgres, postgres.GetName())
	}

	prepared := PostgresBindingPreparedData{Instance: instance.GetName()}
	snapshot, err := readBindingSnapshot(ctx, reader, obj, instance.GetName())
	if err != nil {
		return PostgresBindingPreparedData{}, ctrl.Result{}, err
	}
	prepared.Snapshot = snapshot
	return prepared, ctrl.Result{}, nil
}

func readBindingSnapshot(ctx context.Context, reader client.Reader, binding *v1.PostgresBinding, instance string) (*bindingSnapshot, error) {
	cluster := &cnpgv1.Cluster{}
	clusterKey := client.ObjectKey{Namespace: binding.GetNamespace(), Name: rccnpg.ClusterNameFor(instance)}
	if err := reader.Get(ctx, clusterKey, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting CNPG Cluster %q: %w", clusterKey.Name, err)
	}

	ca, ok, err := readSecretData(ctx, reader, client.ObjectKey{Namespace: binding.GetNamespace(), Name: cluster.GetClientCASecretName()}, "ca.crt")
	if err != nil || !ok {
		return nil, err
	}

	snapshot := &bindingSnapshot{CACertificate: ca["ca.crt"], Credentials: make(map[v1.PostgresBindingCredential]rcbinding.CredentialMaterial, len(binding.Spec.Credentials))}
	for _, credential := range binding.Spec.Credentials {
		roleName, err := certificateRoleName(ctx, reader, binding, instance, cluster.GetName(), credential)
		if err != nil {
			return nil, err
		}
		if roleName == "" {
			return nil, nil
		}
		certificate, ok, err := readSecretData(ctx, reader, client.ObjectKey{Namespace: binding.GetNamespace(), Name: (&cnpgv1.DatabaseRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}}).GetClientCertSecretName()}, "tls.crt", "tls.key")
		if err != nil || !ok {
			return nil, err
		}
		snapshot.Credentials[credential] = rcbinding.CredentialMaterial{Certificate: certificate["tls.crt"], PrivateKey: certificate["tls.key"]}
	}
	return snapshot, nil
}

func certificateRoleName(ctx context.Context, reader client.Reader, binding *v1.PostgresBinding, instance, cluster string, credential v1.PostgresBindingCredential) (string, error) {
	if credential != v1.PostgresBindingCredentialAdmin {
		return rcbinding.DatabaseRoleName(binding, instance, credential), nil
	}

	roles := &cnpgv1.DatabaseRoleList{}
	if err := reader.List(ctx, roles, client.InNamespace(binding.GetNamespace())); err != nil {
		return "", fmt.Errorf("listing DatabaseRoles for admin certificate: %w", err)
	}
	for _, role := range roles.Items {
		if role.Spec.ClusterRef.Name == cluster && role.Spec.Name == rccnpg.OwnerRole && role.IsClientCertificateEnabled() {
			return role.GetName(), nil
		}
	}
	return "", nil
}

func readSecretData(ctx context.Context, reader client.Reader, key client.ObjectKey, keys ...string) (map[string][]byte, bool, error) {
	secret := &core_v1.Secret{}
	if err := reader.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("getting Secret %q: %w", key.Name, err)
	}
	data := make(map[string][]byte, len(keys))
	for _, key := range keys {
		value, ok := secret.Data[key]
		if !ok || len(value) == 0 {
			return nil, false, nil
		}
		data[key] = value
	}
	return data, true, nil
}

func (r *PostgresBindingReconciler) Update(obj *v1.PostgresBinding, prepared PostgresBindingPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	actions := make([]action.Action, 0, len(obj.Spec.Credentials)+3)
	for _, credential := range obj.Spec.Credentials {
		// app is the durable owner identity. The Postgres instance owns its
		// DatabaseRole and certificate; bindings only consume it.
		if credential == v1.PostgresBindingCredentialAdmin {
			continue
		}
		role, err := rcbinding.CreateDatabaseRole(r.Scheme, obj, prepared.Instance, credential)
		if err != nil {
			return nil, ctrl.Result{}, fmt.Errorf("creating DatabaseRole spec: %w", err)
		}
		actions = append(actions, action.ExclusiveCreateOrUpdate(role, obj, existsConditionGetter, r.Recorder))
	}

	if prepared.Snapshot != nil {
		configSecret, err := rcbinding.CreateConfigSecret(r.Scheme, obj, prepared.Instance, prepared.Snapshot.CACertificate, prepared.Snapshot.Credentials)
		if err != nil {
			return nil, ctrl.Result{}, fmt.Errorf("creating config Secret spec: %w", err)
		}
		actions = append(actions, action.ExclusiveCreateOrUpdate(configSecret, obj, existsConditionGetter, r.Recorder))
	}

	netpol, err := rcbinding.CreateNetworkPolicy(r.Scheme, obj, prepared.Instance)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating NetworkPolicy spec: %w", err)
	}
	actions = append(actions, action.ExclusiveCreateOrUpdate(netpol, obj, existsConditionGetter, r.Recorder))

	egressNetpol, err := rcbinding.CreateEgressNetworkPolicy(r.Scheme, obj, prepared.Instance)
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
