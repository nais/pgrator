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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
}

var _ reconciler.Reconciler[*v1.PostgresAccess, PostgresAccessPreparedData] = &PostgresAccessReconciler{}

type PostgresAccessPreparedData struct {
	Instance *v1.PostgresInstance `yaml:"instance"`
}

const postgresAccessInstanceIndex = "spec.postgresInstance"
const maximumPostgresAccessLifetime = time.Hour

func (r *PostgresAccessReconciler) Name() string { return "postgresaccess.nais.io" }

func (r *PostgresAccessReconciler) New() *v1.PostgresAccess { return &v1.PostgresAccess{} }

// DatabaseRoles deliberately are not owned by PostgresAccess. The identity
// survives expiry so its user can return to objects it owns in the database.
func (r *PostgresAccessReconciler) OwnedTypes() []reconciler.OwnedType { return nil }

func (r *PostgresAccessReconciler) AdditionalTypes() []client.Object { return nil }

func (r *PostgresAccessReconciler) Indexes() []reconciler.Index {
	return []reconciler.Index{{
		Object: &v1.PostgresAccess{},
		Field:  postgresAccessInstanceIndex,
		ExtractValue: func(object client.Object) []string {
			access, ok := object.(*v1.PostgresAccess)
			if !ok || access.Spec.PostgresInstance == "" {
				return nil
			}
			return []string{access.Spec.PostgresInstance}
		},
	}}
}

func (r *PostgresAccessReconciler) RelationshipWatches() []reconciler.RelationshipWatch {
	return []reconciler.RelationshipWatch{{
		Type: &v1.PostgresInstance{},
		Map:  r.accessesForInstance,
		Predicate: predicate.Funcs{
			CreateFunc: func(event.CreateEvent) bool { return true },
			DeleteFunc: func(event.DeleteEvent) bool { return true },
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldInstance, oldOK := e.ObjectOld.(*v1.PostgresInstance)
				newInstance, newOK := e.ObjectNew.(*v1.PostgresInstance)
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
				return oldOK && newOK && recoveryComplete(oldCluster) != recoveryComplete(newCluster)
			},
		},
	}, {
		Type: &cnpgv1.DatabaseRole{},
		Map:  r.accessesForDatabaseRole,
		Predicate: predicate.Funcs{
			CreateFunc: func(event.CreateEvent) bool { return true },
			DeleteFunc: func(event.DeleteEvent) bool { return true },
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldRole, oldOK := e.ObjectOld.(*cnpgv1.DatabaseRole)
				newRole, newOK := e.ObjectNew.(*cnpgv1.DatabaseRole)
				return oldOK && newOK && !reflect.DeepEqual(oldRole.Status, newRole.Status)
			},
		},
	}}
}

func (r *PostgresAccessReconciler) accessesForInstance(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
	instance, ok := object.(*v1.PostgresInstance)
	if !ok {
		return nil, nil
	}
	return r.accessesForInstanceName(ctx, reader, instance.Namespace, instance.Name)
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
		if rccnpg.ClusterNameFor(access.Spec.PostgresInstance) == cluster.Name {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&access)})
		}
	}
	return requests, nil
}

func (r *PostgresAccessReconciler) accessesForInstanceName(ctx context.Context, reader client.Reader, namespace, instance string) ([]reconcile.Request, error) {
	accesses := &v1.PostgresAccessList{}
	if err := reader.List(ctx, accesses, client.InNamespace(namespace), client.MatchingFields{postgresAccessInstanceIndex: instance}); err != nil {
		return nil, fmt.Errorf("listing PostgresAccess resources for PostgresInstance %q: %w", instance, err)
	}
	requests := make([]reconcile.Request, 0, len(accesses.Items))
	for _, access := range accesses.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&access)})
	}
	return requests, nil
}

func (r *PostgresAccessReconciler) accessesForDatabaseRole(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
	role, ok := object.(*cnpgv1.DatabaseRole)
	if !ok {
		return nil, nil
	}
	accesses := &v1.PostgresAccessList{}
	if err := reader.List(ctx, accesses, client.InNamespace(role.Namespace)); err != nil {
		return nil, fmt.Errorf("listing PostgresAccess resources for DatabaseRole %q: %w", role.Name, err)
	}
	requests := make([]reconcile.Request, 0)
	for _, access := range accesses.Items {
		if rcaccess.DatabaseRoleName(access.Spec.Username, access.Spec.PostgresInstance) == role.Name {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&access)})
		}
	}
	return requests, nil
}

func (r *PostgresAccessReconciler) Prepare(ctx context.Context, reader client.Reader, access *v1.PostgresAccess) (PostgresAccessPreparedData, ctrl.Result, error) {
	now := time.Now()
	if access.GetDeletionTimestamp().IsZero() {
		if access.Spec.ExpiresAt.Time.Before(now) {
			return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("expiresAt must be in the future")
		}
		if access.Spec.ExpiresAt.After(now.Add(maximumPostgresAccessLifetime)) {
			return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("expiresAt must be at most %s from now", maximumPostgresAccessLifetime)
		}
	}
	instance := &v1.PostgresInstance{}
	key := client.ObjectKey{Namespace: access.Namespace, Name: access.Spec.PostgresInstance}
	if err := reader.Get(ctx, key, instance); err != nil {
		if apierrors.IsNotFound(err) && !access.GetDeletionTimestamp().IsZero() {
			return PostgresAccessPreparedData{}, ctrl.Result{}, nil
		}
		return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("getting PostgresInstance %q: %w", access.Spec.PostgresInstance, err)
	}
	postgres := &v1.Postgres{}
	postgresKey := client.ObjectKey{Namespace: access.Namespace, Name: instance.Spec.Postgres}
	if err := reader.Get(ctx, postgresKey, postgres); err != nil {
		return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("getting Postgres for PostgresInstance %q: %w", instance.Name, err)
	}
	cluster := &cnpgv1.Cluster{}
	clusterKey := client.ObjectKey{Namespace: access.Namespace, Name: rccnpg.ClusterNameFor(instance.Name)}
	if err := reader.Get(ctx, clusterKey, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return PostgresAccessPreparedData{}, ctrl.Result{Requeue: true}, nil
		}
		return PostgresAccessPreparedData{}, ctrl.Result{}, fmt.Errorf("getting CNPG Cluster for PostgresInstance %q: %w", instance.Name, err)
	}
	if !recoveryComplete(cluster) {
		return PostgresAccessPreparedData{}, ctrl.Result{Requeue: true}, nil
	}
	return PostgresAccessPreparedData{Instance: instance}, ctrl.Result{}, nil
}

func (r *PostgresAccessReconciler) Update(access *v1.PostgresAccess, _ PostgresAccessPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	role := rcaccess.CreateDatabaseRole(access)
	access.GetStatus().(*v1.PostgresAccessStatus).DatabaseRole = role.Spec.Name
	return []action.Action{action.CreateOrUpdate(role, access, databaseRoleConditionGetter, r.Recorder)}, ctrl.Result{}, nil
}

func (r *PostgresAccessReconciler) Delete(_ *v1.PostgresAccess, _ PostgresAccessPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	return nil, ctrl.Result{}, nil
}

func databaseRoleConditionGetter(object client.Object, _ *runtime.Scheme) []metav1.Condition {
	role, ok := object.(*cnpgv1.DatabaseRole)
	if !ok || role.Status.Applied == nil {
		return []metav1.Condition{{Type: "DatabaseRoleReady", Status: metav1.ConditionFalse, Reason: "Pending"}}
	}
	status := metav1.ConditionFalse
	if *role.Status.Applied {
		status = metav1.ConditionTrue
	}
	return []metav1.Condition{{Type: "DatabaseRoleReady", Status: status, Reason: "Applied", Message: role.Status.Message, ObservedGeneration: role.Status.ObservedGeneration}}
}
