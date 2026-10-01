package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/initscheme"
	rcaccess "github.com/nais/pgrator/internal/resourcecreator/access"
	rcbinding "github.com/nais/pgrator/internal/resourcecreator/binding"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDerivedBranchResourceNamesFitLimits(t *testing.T) {
	name := v1.PostgresBranchObjectName(strings.Repeat("p", 39), strings.Repeat("b", 63))
	branch := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team"}}
	bucket := (&PostgresBranchReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal-bucket-prefix"}}}).bucketName(branch, PostgresBranchPreparedData{PostgresUID: types.UID("feedab1e-beef-cafe-babe-700d1e100d1e")})
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: strings.Repeat("x", 63)}}
	for _, tt := range []struct {
		name, value string
		limit       int
	}{
		{"cluster", rccnpg.ClusterNameFor(name), 63},
		{"pooler", rccnpg.PoolerNameFor(name), 63},
		{"rw service", rccnpg.ClusterNameFor(name) + "-rw", 63},
		{"personal group role", rccnpg.PersonalAccessRole(name), 63},
		{"durable owner role", rccnpg.ClusterNameFor(name) + "-app", 63},
		{"personal access role", rcaccess.DatabaseRoleName("frode.sundby@nav.no", name), 63},
		{"binding role", rcbinding.DatabaseRoleName(binding, name, v1.PostgresBindingCredentialReadWrite), 241},
		{"GSA", gsaNameFor(name), 30},
		{"bucket", bucket, 63},
		{"workload identity policy", workloadIdentityPolicyNameFor(name), 253},
		{"bucket policy", storageBucketPolicyNameFor(name), 253},
	} {
		if len(tt.value) > tt.limit {
			t.Errorf("%s name %q has length %d, want <= %d", tt.name, tt.value, len(tt.value), tt.limit)
		}
	}
}

func TestBranchIdentityFailsClosed(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveBranch: "main"}}
	// The object at the expected key claims a different local name. Neither
	// activation nor a personal access may use it, even if its cluster is ready.
	key := v1.PostgresBranchObjectName("orders", "main")
	branch := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: key, Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "restore"}}
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: v1.CNPGClusterName(key), Namespace: "team"}, Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{Type: string(cnpgv1.ConditionInitialized), Status: metav1.ConditionTrue}, {Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionTrue}}}}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(postgres, branch, cluster).Build()
	prepared, _, err := (&PostgresReconciler{}).Prepare(context.Background(), reader, postgres)
	if err != nil || prepared.RequestedReady {
		t.Errorf("activation: prepared = %+v, err = %v; want not ready", prepared, err)
	}
	postgres.Spec.ActiveBranch = ""
	_, _, err = (&PostgresReconciler{}).Prepare(context.Background(), reader, postgres)
	if err == nil || !strings.Contains(err.Error(), "invalid identity") {
		t.Errorf("default branch preparation error = %v, want identity rejection", err)
	}
	_, _, err = (&PostgresBranchReconciler{Config: &config.Config{}}).Prepare(context.Background(), reader, branch)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("branch prepare error = %v, want identity rejection", err)
	}
	conditions := branch.GetStatus().GetConditions()
	if len(conditions) != 1 || conditions[0].Type != readyCondition || conditions[0].Status != metav1.ConditionFalse || conditions[0].Reason != "InvalidIdentity" {
		t.Errorf("branch conditions = %+v, want not-ready InvalidIdentity", conditions)
	}
	access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{PostgresBranch: key, ExpiresAt: metav1.NewTime(time.Now().Add(10 * time.Minute))}}
	_, _, err = (&PostgresAccessReconciler{}).Prepare(context.Background(), reader, access)
	if err == nil || !strings.Contains(err.Error(), "invalid identity") {
		t.Errorf("access prepare error = %v, want identity rejection", err)
	}
}
