package controller

import (
	"context"
	"fmt"
	"reflect"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rcaccess "github.com/nais/pgrator/internal/resourcecreator/access"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// PostgresAccessReconciler establishes the durable database identity behind a
// personal access. Session credentials, privileges and transport are added by
// later PostgresAccess reconciliation steps.
type PostgresAccessReconciler struct {
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

var _ reconciler.Reconciler[*v1.PostgresAccess, PostgresAccessPreparedData] = &PostgresAccessReconciler{}

type PostgresAccessPreparedData struct {
	Branch         *v1.PostgresBranch         `yaml:"branch"`
	Cluster        *cnpgv1.Cluster            `yaml:"-"`
	Password       string                     `yaml:"-"`
	Role           *cnpgv1.DatabaseRole       `yaml:"-"`
	Token          string                     `yaml:"-"`
	TokenPersisted bool                       `yaml:"-"`
	RelayAccess    *unstructured.Unstructured `yaml:"-"`
	Expired        bool                       `yaml:"-"`
}

const postgresAccessBranchIndex = "spec.postgresBranch"
const readyCondition = "Ready"
const maximumPostgresAccessLifetime = time.Hour

func (r *PostgresAccessReconciler) Name() string { return "postgresaccess.nais.io" }

func (r *PostgresAccessReconciler) New() *v1.PostgresAccess { return &v1.PostgresAccess{} }

// PostgresAccess owns its declarative access resources. The DatabaseRole uses
// Retain so CNPG preserves the PostgreSQL role and objects it owns after the
// access and DatabaseRole CR are deleted.
func (r *PostgresAccessReconciler) OwnedTypes() []reconciler.OwnedType {
	return []reconciler.OwnedType{
		{Type: &corev1.Secret{}, AdditionalPredicate: predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
			oldSecret, oldOK := e.ObjectOld.(*corev1.Secret)
			newSecret, newOK := e.ObjectNew.(*corev1.Secret)
			return oldOK && newOK && (!reflect.DeepEqual(oldSecret.Data, newSecret.Data) ||
				(oldSecret.DeletionTimestamp == nil) != (newSecret.DeletionTimestamp == nil))
		}}},
		{
			Type: &cnpgv1.DatabaseRole{},
			AdditionalPredicate: predicate.Funcs{
				CreateFunc: func(event.CreateEvent) bool { return true },
				DeleteFunc: func(event.DeleteEvent) bool { return true },
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldRole, oldOK := e.ObjectOld.(*cnpgv1.DatabaseRole)
					newRole, newOK := e.ObjectNew.(*cnpgv1.DatabaseRole)
					return oldOK && newOK && !reflect.DeepEqual(oldRole.Status, newRole.Status)
				},
			},
		},
		{Type: relayAccessObject(), AdditionalPredicate: predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
			oldRelay, oldOK := e.ObjectOld.(*unstructured.Unstructured)
			newRelay, newOK := e.ObjectNew.(*unstructured.Unstructured)
			return oldOK && newOK && (!reflect.DeepEqual(oldRelay.Object["status"], newRelay.Object["status"]) ||
				(oldRelay.GetDeletionTimestamp() == nil) != (newRelay.GetDeletionTimestamp() == nil))
		}}},
		{Type: &networkingv1.NetworkPolicy{}},
	}
}

func (r *PostgresAccessReconciler) AdditionalTypes() []client.Object { return nil }

func relayAccessObject() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(rcaccess.RelayAccessGVK)
	return obj
}

func (r *PostgresAccessReconciler) Indexes() []reconciler.Index {
	return []reconciler.Index{{
		Object: &v1.PostgresAccess{},
		Field:  postgresAccessBranchIndex,
		ExtractValue: func(object client.Object) []string {
			access, ok := object.(*v1.PostgresAccess)
			if !ok || access.Spec.PostgresBranch == "" {
				return nil
			}
			return []string{access.Spec.PostgresBranch}
		},
	}}
}

func (r *PostgresAccessReconciler) RelationshipWatches() []reconciler.RelationshipWatch {
	return []reconciler.RelationshipWatch{{
		Type: &v1.PostgresBranch{},
		Map:  r.accessesForBranch,
		Predicate: predicate.Funcs{
			CreateFunc: func(event.CreateEvent) bool { return true },
			DeleteFunc: func(event.DeleteEvent) bool { return true },
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldInstance, oldOK := e.ObjectOld.(*v1.PostgresBranch)
				newInstance, newOK := e.ObjectNew.(*v1.PostgresBranch)
				return oldOK && newOK && oldInstance.GetGeneration() != newInstance.GetGeneration()
			},
		},
	}, {
		Type: &cnpgv1.Cluster{},
		Map:  r.accessesForCluster,
		Predicate: predicate.Funcs{
			CreateFunc: func(event.CreateEvent) bool { return true },
			DeleteFunc: func(event.DeleteEvent) bool { return true },
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldCluster, oldOK := e.ObjectOld.(*cnpgv1.Cluster)
				newCluster, newOK := e.ObjectNew.(*cnpgv1.Cluster)
				return oldOK && newOK && (recoveryComplete(oldCluster) != recoveryComplete(newCluster) || rccnpg.ReadWriteCreateCapable(oldCluster) != rccnpg.ReadWriteCreateCapable(newCluster))
			},
		},
	}}
}

func (r *PostgresAccessReconciler) accessesForBranch(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
	instance, ok := object.(*v1.PostgresBranch)
	if !ok {
		return nil, nil
	}
	return r.accessesForBranchName(ctx, reader, instance.Namespace, instance.Name)
}

func (r *PostgresAccessReconciler) accessesForCluster(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
	cluster, ok := object.(*cnpgv1.Cluster)
	if !ok {
		return nil, nil
	}
	accesses := &v1.PostgresAccessList{}
	if err := reader.List(ctx, accesses, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, fmt.Errorf("listing PostgresAccess resources for Cluster %q: %w", cluster.Name, err)
	}
	requests := make([]reconcile.Request, 0)
	for _, access := range accesses.Items {
		if rccnpg.ClusterNameFor(access.Spec.PostgresBranch) == cluster.Name {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&access)})
		}
	}
	return requests, nil
}

func (r *PostgresAccessReconciler) accessesForBranchName(ctx context.Context, reader client.Reader, namespace, instance string) ([]reconcile.Request, error) {
	accesses := &v1.PostgresAccessList{}
	if err := reader.List(ctx, accesses, client.InNamespace(namespace), client.MatchingFields{postgresAccessBranchIndex: instance}); err != nil {
		return nil, fmt.Errorf("listing PostgresAccess resources for PostgresBranch %q: %w", instance, err)
	}
	requests := make([]reconcile.Request, 0, len(accesses.Items))
	for _, access := range accesses.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&access)})
	}
	return requests, nil
}

func (r *PostgresAccessReconciler) Prepare(ctx context.Context, reader client.Reader, access *v1.PostgresAccess) (PostgresAccessPreparedData, ctrl.Result, error) {
	if !access.GetDeletionTimestamp().IsZero() {
		return PostgresAccessPreparedData{}, ctrl.Result{}, nil
	}
	access.GetStatus().SetCondition(metav1.Condition{Type: readyCondition, Status: metav1.ConditionFalse, Reason: "Pending", Message: "access is reconciling"})
	access.GetStatus().(*v1.PostgresAccessStatus).RelayEndpoint = ""
	now := time.Now()
	if !now.Before(access.Spec.ExpiresAt.Time) {
		return PostgresAccessPreparedData{Expired: true}, ctrl.Result{}, nil
	}
	start := access.GetCreationTimestamp().Time
	if start.IsZero() {
		start = now
	}
	if access.Spec.ExpiresAt.After(start.Add(maximumPostgresAccessLifetime)) {
		return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("expiresAt must be at most %s from creation", maximumPostgresAccessLifetime)
	}
	instance := &v1.PostgresBranch{}
	key := client.ObjectKey{Namespace: access.Namespace, Name: access.Spec.PostgresBranch}
	if err := reader.Get(ctx, key, instance); err != nil {
		return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("getting PostgresBranch %q: %w", access.Spec.PostgresBranch, err)
	}
	if !validBranchIdentity(instance) || instance.Name != access.Spec.PostgresBranch {
		return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("PostgresBranch %q has invalid identity", instance.Name)
	}
	postgres := &v1.Postgres{}
	postgresKey := client.ObjectKey{Namespace: access.Namespace, Name: instance.Spec.Postgres}
	if err := reader.Get(ctx, postgresKey, postgres); err != nil {
		return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("getting Postgres for PostgresBranch %q: %w", instance.Name, err)
	}
	cluster := &cnpgv1.Cluster{}
	clusterKey := client.ObjectKey{Namespace: access.Namespace, Name: rccnpg.ClusterNameFor(instance.Name)}
	if err := reader.Get(ctx, clusterKey, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("CNPG Cluster for PostgresBranch %q is not yet available", instance.Name)
		}
		return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("getting CNPG Cluster for PostgresBranch %q: %w", instance.Name, err)
	}
	if !recoveryComplete(cluster) {
		return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("CNPG Cluster for PostgresBranch %q is not ready", instance.Name)
	}

	prep := PostgresAccessPreparedData{Branch: instance, Cluster: cluster}

	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{Namespace: access.Namespace, Name: rcaccess.CredentialSecretName(access)}
	if err := reader.Get(ctx, secretKey, secret); err != nil {
		if !apierrors.IsNotFound(err) {
			return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("getting credential Secret: %w", err)
		}
		password, err := rcaccess.NewPassword()
		if err != nil {
			return PostgresAccessPreparedData{}, ctrl.Result{}, err
		}
		prep.Password = password
	} else {
		password, ok := secret.Data[corev1.BasicAuthPasswordKey]
		if !ok {
			return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("credential Secret %q has no password", secret.Name)
		}
		prep.Password = string(password)
	}

	role := &cnpgv1.DatabaseRole{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: access.Namespace, Name: rcaccess.DatabaseRoleName(access.Spec.Username, access.Spec.PostgresBranch)}, role); err == nil {
		if !metav1.IsControlledBy(role, access) {
			return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("personal DatabaseRole is not controlled by PostgresAccess")
		}
		prep.Role = role
	} else if !apierrors.IsNotFound(err) {
		return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("getting personal DatabaseRole: %w", err)
	}

	relay := relayAccessObject()
	if err := reader.Get(ctx, client.ObjectKey{Namespace: access.Namespace, Name: rcaccess.RelayAccessName(access)}, relay); err == nil {
		if !relay.GetDeletionTimestamp().IsZero() {
			return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("RelayAccess %q is terminating", relay.GetName())
		}
		prep.RelayAccess = relay
	} else if !apierrors.IsNotFound(err) {
		return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("getting RelayAccess: %w", err)
	}
	tokenSecret := &corev1.Secret{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: access.Namespace, Name: rcaccess.TokenSecretName(access)}, tokenSecret); err == nil {
		if !tokenSecret.GetDeletionTimestamp().IsZero() {
			return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("relay token Secret %q is terminating", tokenSecret.GetName())
		}
		if !metav1.IsControlledBy(tokenSecret, access) {
			return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("relay token Secret is not controlled by PostgresAccess")
		}
		prep.Token = string(tokenSecret.Data[rcaccess.TokenKey])
		if _, err := rcaccess.TokenDigest(prep.Token); err != nil {
			return PostgresAccessPreparedData{}, ctrl.Result{}, err
		}
		prep.TokenPersisted = true
	} else if apierrors.IsNotFound(err) {
		if prep.RelayAccess != nil || access.GetStatus().(*v1.PostgresAccessStatus).TokenSecret != "" {
			return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("relay token Secret is missing after access provisioning; refusing token rotation")
		}
		prep.Token, err = rcaccess.NewToken()
		if err != nil {
			return PostgresAccessPreparedData{}, ctrl.Result{}, err
		}
	} else {
		return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("getting relay token Secret: %w", err)
	}
	return prep, ctrl.Result{}, nil
}

func (r *PostgresAccessReconciler) Update(access *v1.PostgresAccess, prepared PostgresAccessPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	if prepared.Expired {
		status := access.GetStatus().(*v1.PostgresAccessStatus)
		status.RelayAccess = ""
		status.RelayEndpoint = ""
		status.TokenSecret = ""
		status.ServerName = ""
		status.ServerCASecret = ""
		status.SetCondition(metav1.Condition{Type: readyCondition, Status: metav1.ConditionFalse, Reason: "Expired", Message: "access has expired"})
		return nil, ctrl.Result{}, nil
	}
	if access.Spec.AccessLevel == v1.PostgresAccessLevelReadWriteCreate && !rccnpg.ReadWriteCreateCapable(prepared.Cluster) {
		// The instance's cluster was initialized without the app_readwritecreate
		// group role, so CNPG could never apply the DatabaseRole. The access spec
		// is immutable; fail the access instead of reconciling it forever.
		r.Recorder.RecordEvent(access, corev1.EventTypeWarning, "UnsupportedAccessLevel",
			"accessLevel readwritecreate is not available on PostgresBranch %q: its cluster was initialized without the %s group role",
			access.Spec.PostgresBranch, rccnpg.ReadWriteCreateRole)
		access.GetStatus().(*v1.PostgresAccessStatus).SetCondition(metav1.Condition{
			Type:               readyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             "UnsupportedAccessLevel",
			Message:            fmt.Sprintf("readwritecreate requires an instance initialized with the %s group role; %q predates it", rccnpg.ReadWriteCreateRole, access.Spec.PostgresBranch),
			ObservedGeneration: access.GetGeneration(),
		})
		return nil, ctrl.Result{}, nil
	}

	secret, err := rcaccess.CreateCredentialSecret(r.Scheme, access, prepared.Password)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	role, err := rcaccess.CreateDatabaseRole(r.Scheme, access, true)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating DatabaseRole spec: %w", err)
	}
	access.GetStatus().(*v1.PostgresAccessStatus).DatabaseRole = role.Spec.Name

	status := access.GetStatus().(*v1.PostgresAccessStatus)
	status.RelayAccess = rcaccess.RelayAccessName(access)
	status.RelayEndpoint = ""
	status.ServerName = prepared.Cluster.Name + "-rw." + access.Namespace + ".svc.cluster.local"
	status.ServerCASecret = prepared.Cluster.GetServerCASecretName()
	if prepared.TokenPersisted {
		status.TokenSecret = rcaccess.TokenSecretName(access)
	}
	digest, err := rcaccess.TokenDigest(prepared.Token)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	desired, err := rcaccess.CreateRelayAccess(r.Scheme, access, digest)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	netpol, err := rcaccess.CreateRelayNetworkPolicy(r.Scheme, access)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	tokenSecret, err := rcaccess.CreateTokenSecret(r.Scheme, access, prepared.Token)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	// An immutable mapping cannot be repaired by updating its spec. Refuse
	// mismatches (including a recreated access with the same name) instead.
	var relayAction action.Action
	if prepared.RelayAccess != nil {
		if !metav1.IsControlledBy(prepared.RelayAccess, access) ||
			!reflect.DeepEqual(prepared.RelayAccess.Object["spec"], desired.Object["spec"]) {
			return nil, ctrl.Result{}, fmt.Errorf("RelayAccess %q has a different owner or immutable spec", desired.GetName())
		}
		endpoint, found, err := unstructured.NestedString(prepared.RelayAccess.Object, "status", "endpoint")
		if err != nil {
			return nil, ctrl.Result{}, fmt.Errorf("reading owned RelayAccess endpoint: %w", err)
		}
		if found {
			status.RelayEndpoint = endpoint
		}
		relayAction = action.Claim(desired, access, existsConditionGetter, r.Recorder)
	} else {
		relayAction = action.Create(desired, access, existsConditionGetter, r.Recorder)
	}
	roleReady := databaseRoleIsReady(prepared.Role) && prepared.Role.Spec.PasswordSecret != nil &&
		prepared.Role.Spec.PasswordSecret.Name == rcaccess.CredentialSecretName(access) &&
		prepared.Role.Spec.Login && prepared.Role.Spec.ValidUntil != nil &&
		prepared.Role.Spec.ValidUntil.Time.Truncate(time.Second).Equal(access.Spec.ExpiresAt.Truncate(time.Second)) &&
		reflect.DeepEqual(prepared.Role.Spec.InRoles, role.Spec.InRoles)
	setPostgresAccessReadyCondition(status, roleReady && prepared.TokenPersisted && status.RelayEndpoint != "")
	return []action.Action{
		action.ExclusiveCreateOrUpdate(secret, access, existsConditionGetter, r.Recorder),
		action.CreateOrUpdate(role, access, databaseRoleConditionGetter, r.Recorder),
		action.ExclusiveCreateOrUpdate(tokenSecret, access, existsConditionGetter, r.Recorder),
		relayAction,
		action.ExclusiveCreateOrUpdate(netpol, access, existsConditionGetter, r.Recorder),
	}, ctrl.Result{RequeueAfter: time.Until(access.Spec.ExpiresAt.Time)}, nil
}

func (r *PostgresAccessReconciler) Delete(*v1.PostgresAccess, PostgresAccessPreparedData, reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	return nil, ctrl.Result{}, nil
}

func databaseRoleConditionGetter(object client.Object, _ *runtime.Scheme) []metav1.Condition {
	role, ok := object.(*cnpgv1.DatabaseRole)
	if !ok || role.Status.Applied == nil {
		return []metav1.Condition{{Type: "DatabaseRoleReady", Status: metav1.ConditionFalse, Reason: "Pending"}}
	}
	// CNPG may still report Applied=true for an older generation; only the
	// current generation proves the intended privileges are in effect.
	if *role.Status.Applied && role.Status.ObservedGeneration == role.GetGeneration() {
		return []metav1.Condition{{Type: "DatabaseRoleReady", Status: metav1.ConditionTrue, Reason: "Applied", Message: role.Status.Message, ObservedGeneration: role.Status.ObservedGeneration}}
	}
	return []metav1.Condition{{Type: "DatabaseRoleReady", Status: metav1.ConditionFalse, Reason: "Pending", Message: role.Status.Message, ObservedGeneration: role.Status.ObservedGeneration}}
}

func setPostgresAccessReadyCondition(status *v1.PostgresAccessStatus, ready bool) {
	condition := metav1.Condition{Type: readyCondition, Status: metav1.ConditionFalse, Reason: "Pending", Message: "waiting for applied database role, persisted relay token and operator-published endpoint"}
	if ready {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Ready"
		condition.Message = "Database role applied; relay mapping, token and egress policy persisted (relay data path not verified)"
	}
	status.SetCondition(condition)
}

func databaseRoleIsReady(role *cnpgv1.DatabaseRole) bool {
	return role != nil && role.Status.Applied != nil && *role.Status.Applied &&
		role.Status.ObservedGeneration == role.GetGeneration()
}
