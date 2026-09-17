package controller

import (
	"context"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	tunnelv1alpha1 "github.com/nais/tunnel-operator/api/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func makePostgresAccessReconciler() *PostgresAccessReconciler {
	return &PostgresAccessReconciler{Recorder: recorder, Scheme: scheme.Scheme}
}

func TestPostgresAccessReconcilesDatabaseRoleAndTunnel(t *testing.T) {
	access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{
		Username: "frode.sundby@nav.no", PostgresInstance: "orders-restore", AccessLevel: v1.PostgresAccessLevelReadWrite, ExpiresAt: metav1.NewTime(time.Now().Add(time.Hour)),
	}}
	instance := &v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders-restore", Namespace: "team"}, Spec: v1.PostgresInstanceSpec{Postgres: "orders"}}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}}
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg-orders-restore", Namespace: "team"}, Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{Type: string(cnpgv1.ConditionInitialized), Status: metav1.ConditionTrue}, {Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionTrue}}}}
	r := makePostgresAccessReconciler()
	prepared, _, err := r.Prepare(context.Background(), fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(instance, postgres, cluster).Build(), access)
	requireNoError(t, err)
	if prepared.Instance == nil || prepared.Instance.Name != instance.Name {
		t.Fatal("Prepare() did not return referenced instance")
	}

	actions, _, err := r.Update(access, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	if len(actions) != 4 {
		t.Fatalf("Update() actions = %d, want 4", len(actions))
	}
	role, ok := actions[1].GetObject().(*cnpgv1.DatabaseRole)
	if !ok {
		t.Fatalf("action object = %T, want DatabaseRole", actions[1].GetObject())
	}
	if role.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimRetain {
		t.Errorf("reclaim policy = %q, want retain", role.Spec.ReclaimPolicy)
	}
	owner := metav1.GetControllerOf(role)
	if owner == nil || owner.Kind != "PostgresAccess" || owner.Name != access.Name {
		t.Error("DatabaseRole must be controlled by PostgresAccess")
	}
	if !role.Spec.Login || role.Spec.PasswordSecret == nil || len(role.Spec.InRoles) != 1 || role.Spec.InRoles[0] != "app_readwrite" {
		t.Error("active DatabaseRole is missing its credential or readwrite membership")
	}
	if access.Status.DatabaseRole != role.Spec.Name {
		t.Errorf("status database role = %q, want %q", access.Status.DatabaseRole, role.Spec.Name)
	}
	tunnel, ok := actions[2].GetObject().(*tunnelv1alpha1.Tunnel)
	if !ok {
		t.Fatalf("action object = %T, want Tunnel", actions[2].GetObject())
	}
	if tunnel.Spec.Target.Host != "pg-orders-restore-rw.team.svc.cluster.local" {
		t.Errorf("tunnel target host = %q, want %q", tunnel.Spec.Target.Host, "pg-orders-restore-rw.team.svc.cluster.local")
	}
	if tunnel.Spec.Target.Port != 5432 {
		t.Errorf("tunnel target port = %d, want 5432", tunnel.Spec.Target.Port)
	}
	if tunnel.Spec.Target.PodSelector == nil || tunnel.Spec.Target.PodSelector.MatchLabels["cnpg.io/cluster"] != "pg-orders-restore" || tunnel.Spec.Target.PodSelector.MatchLabels["cnpg.io/instanceRole"] != "primary" {
		t.Error("tunnel target podSelector must select the CNPG primary")
	}
	netpol, ok := actions[3].GetObject().(*networkingv1.NetworkPolicy)
	if !ok {
		t.Fatalf("action object = %T, want NetworkPolicy", actions[3].GetObject())
	}
	if netpol.Spec.PodSelector.MatchLabels["cnpg.io/cluster"] != "pg-orders-restore" {
		t.Error("network policy must select the CNPG primary")
	}
}

func TestPostgresAccessPrepareRejectsMissingInstance(t *testing.T) {
	access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{PostgresInstance: "missing", ExpiresAt: metav1.NewTime(time.Now().Add(time.Hour))}}
	_, _, err := (&PostgresAccessReconciler{}).Prepare(context.Background(), fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(), access)
	if err == nil {
		t.Fatal("Prepare() error = nil, want missing PostgresInstance error")
	}
}

func TestPostgresAccessPrepareRejectsInvalidExpiry(t *testing.T) {
	for _, expiry := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(2 * time.Hour)} {
		access := &v1.PostgresAccess{Spec: v1.PostgresAccessSpec{ExpiresAt: metav1.NewTime(expiry)}}
		_, _, err := (&PostgresAccessReconciler{}).Prepare(context.Background(), fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(), access)
		if err == nil {
			t.Error("Prepare() error = nil, want expiry validation error")
		}
	}
}

func TestPostgresAccessDeletionLetsGarbageCollectionRemoveOwnedResources(t *testing.T) {
	access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team"}}
	actions, result, err := (&PostgresAccessReconciler{Recorder: recorder}).Delete(access, PostgresAccessPreparedData{}, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	if len(actions) != 0 || !result.IsZero() {
		t.Error("Delete() must let garbage collection remove access-owned resources")
	}
}

func TestPostgresAccessReadyOnlyWhenRoleAndTunnelReady(t *testing.T) {
	applied := true
	role := &cnpgv1.DatabaseRole{ObjectMeta: metav1.ObjectMeta{Generation: 1}, Status: cnpgv1.DatabaseRoleStatus{Applied: &applied, ObservedGeneration: 1}}
	tunnel := &tunnelv1alpha1.Tunnel{Status: tunnelv1alpha1.TunnelStatus{Phase: tunnelv1alpha1.TunnelPhaseReady}}

	status := &v1.PostgresAccessStatus{}
	setPostgresAccessReadyCondition(status, role, tunnel)
	cond := findReadyCondition(status.Conditions)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("Ready condition = %v, want True", cond)
	}

	status.Conditions = nil
	setPostgresAccessReadyCondition(status, role, &tunnelv1alpha1.Tunnel{Status: tunnelv1alpha1.TunnelStatus{Phase: tunnelv1alpha1.TunnelPhasePending}})
	cond = findReadyCondition(status.Conditions)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("Ready condition = %v, want False while tunnel pending", cond)
	}

	status.Conditions = nil
	setPostgresAccessReadyCondition(status, nil, tunnel)
	cond = findReadyCondition(status.Conditions)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("Ready condition = %v, want False while role missing", cond)
	}

	status.Conditions = nil
	setPostgresAccessReadyCondition(status, role, &tunnelv1alpha1.Tunnel{Status: tunnelv1alpha1.TunnelStatus{Phase: tunnelv1alpha1.TunnelPhaseConnected}})
	cond = findReadyCondition(status.Conditions)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("Ready condition = %v, want False when tunnel is Connected", cond)
	}
}

func findReadyCondition(conditions []metav1.Condition) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == postgresAccessReadyCondition {
			return &conditions[i]
		}
	}
	return nil
}

// readwritecreate needs the app_readwritecreate group role, which only exists
// on clusters initialized by a pgrator version that creates it. Accesses asking
// for it on an uncapable instance must fail with an honest condition instead of
// reconciling a DatabaseRole CNPG can never apply.
func TestPostgresAccessReadWriteCreateRequiresCapableCluster(t *testing.T) {
	newAccess := func(level v1.PostgresAccessLevel) *v1.PostgresAccess {
		return &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team", Generation: 1}, Spec: v1.PostgresAccessSpec{
			Username: "frode.sundby@nav.no", PostgresInstance: "orders", AccessLevel: level, ExpiresAt: metav1.NewTime(time.Now().Add(time.Hour)),
		}}
	}
	instance := &v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}}
	capableCluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name: "pg-orders", Namespace: "team",
		Annotations: map[string]string{rccnpg.ReadWriteCreateCapableAnnotation: "true"},
	}}
	uncapableCluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg-orders", Namespace: "team"}}

	t.Run("rejected on uncapable cluster", func(t *testing.T) {
		access := newAccess(v1.PostgresAccessLevelReadWriteCreate)
		actions, _, err := makePostgresAccessReconciler().Update(access, PostgresAccessPreparedData{Instance: instance, Cluster: uncapableCluster}, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
		requireNoError(t, err)
		if len(actions) != 0 {
			t.Fatalf("Update() actions = %d, want 0 for an unsupported access level", len(actions))
		}
		cond := findReadyCondition(access.Status.Conditions)
		if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "UnsupportedAccessLevel" {
			t.Errorf("Ready condition = %v, want False/UnsupportedAccessLevel", cond)
		}
	})

	t.Run("reconciled on capable cluster", func(t *testing.T) {
		access := newAccess(v1.PostgresAccessLevelReadWriteCreate)
		actions, _, err := makePostgresAccessReconciler().Update(access, PostgresAccessPreparedData{Instance: instance, Cluster: capableCluster, Password: "secret"}, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
		requireNoError(t, err)
		if len(actions) != 4 {
			t.Fatalf("Update() actions = %d, want 4", len(actions))
		}
		role, ok := actions[1].GetObject().(*cnpgv1.DatabaseRole)
		if !ok {
			t.Fatalf("action object = %T, want DatabaseRole", actions[1].GetObject())
		}
		if len(role.Spec.InRoles) != 1 || role.Spec.InRoles[0] != rccnpg.ReadWriteCreateRole {
			t.Errorf("inRoles = %v, want [%s]", role.Spec.InRoles, rccnpg.ReadWriteCreateRole)
		}
	})

	t.Run("other levels unaffected by capability", func(t *testing.T) {
		access := newAccess(v1.PostgresAccessLevelReadWrite)
		actions, _, err := makePostgresAccessReconciler().Update(access, PostgresAccessPreparedData{Instance: instance, Cluster: uncapableCluster, Password: "secret"}, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
		requireNoError(t, err)
		if len(actions) != 4 {
			t.Fatalf("Update() actions = %d, want 4", len(actions))
		}
	})
}

// DatabaseRoleReady must not report ready from a stale CNPG status: Applied=true
// for an older generation says nothing about the current intended privileges.
func TestDatabaseRoleConditionGetterRequiresCurrentGeneration(t *testing.T) {
	applied := true
	newRole := func(generation, observed int64) *cnpgv1.DatabaseRole {
		return &cnpgv1.DatabaseRole{
			ObjectMeta: metav1.ObjectMeta{Generation: generation},
			Status:     cnpgv1.DatabaseRoleStatus{Applied: &applied, ObservedGeneration: observed},
		}
	}

	conditions := databaseRoleConditionGetter(newRole(2, 1), scheme.Scheme)
	if conditions[0].Status != metav1.ConditionFalse {
		t.Errorf("DatabaseRoleReady = %v, want False while CNPG has not applied the current generation", conditions[0].Status)
	}

	conditions = databaseRoleConditionGetter(newRole(2, 2), scheme.Scheme)
	if conditions[0].Status != metav1.ConditionTrue {
		t.Errorf("DatabaseRoleReady = %v, want True once the current generation is applied", conditions[0].Status)
	}
}
