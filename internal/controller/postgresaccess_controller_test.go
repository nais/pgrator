package controller

import (
	"context"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPostgresAccessReconcilesDurableDatabaseRole(t *testing.T) {
	access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{
		Username: "frode.sundby@nav.no", PostgresInstance: "orders-restore", ExpiresAt: metav1.NewTime(time.Now().Add(time.Hour)),
	}}
	instance := &v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders-restore", Namespace: "team"}, Spec: v1.PostgresInstanceSpec{Postgres: "orders"}}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}}
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg-orders-restore", Namespace: "team"}, Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{Type: string(cnpgv1.ConditionInitialized), Status: metav1.ConditionTrue}, {Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionTrue}}}}
	r := &PostgresAccessReconciler{Recorder: recorder}
	prepared, _, err := r.Prepare(context.Background(), fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(instance, postgres, cluster).Build(), access)
	requireNoError(t, err)
	if prepared.Instance == nil || prepared.Instance.Name != instance.Name {
		t.Fatal("Prepare() did not return referenced instance")
	}

	actions, _, err := r.Update(access, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	if len(actions) != 1 {
		t.Fatalf("Update() actions = %d, want 1", len(actions))
	}
	role, ok := actions[0].GetObject().(*cnpgv1.DatabaseRole)
	if !ok {
		t.Fatalf("action object = %T, want DatabaseRole", actions[0].GetObject())
	}
	if role.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimRetain {
		t.Errorf("reclaim policy = %q, want retain", role.Spec.ReclaimPolicy)
	}
	if metav1.GetControllerOf(role) != nil {
		t.Error("durable DatabaseRole must not be controlled by PostgresAccess")
	}
	if access.Status.DatabaseRole != role.Spec.Name {
		t.Errorf("status database role = %q, want %q", access.Status.DatabaseRole, role.Spec.Name)
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
