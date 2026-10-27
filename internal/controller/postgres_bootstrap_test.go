package controller

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/initscheme"
	"github.com/nais/pgrator/internal/synchronizer"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestPostgresBootstrapMain(t *testing.T) {
	for _, tt := range []struct {
		name   string
		spec   string
		status string
	}{
		{name: "implicit main"},
		{name: "explicit main", spec: v1.DefaultBranchName},
		{name: "implicit main still provisioning", status: v1.DefaultBranchName},
		{name: "explicit main still provisioning", spec: v1.DefaultBranchName, status: v1.DefaultBranchName},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			postgres := &v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", UID: "postgres-uid"},
				Spec:       v1.PostgresSpec{ActiveBranch: tt.spec},
				Status:     &v1.PostgresStatus{ActiveBranch: tt.status},
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(postgres).WithObjects(postgres).Build()
			r := &PostgresReconciler{Scheme: scheme, Recorder: recorder}
			sync := synchronizer.NewSynchronizer(reader, scheme, r, recorder)
			for range 2 {
				_, err := sync.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(postgres)})
				requireNoError(t, err)

				branch := &v1.PostgresBranch{}
				key := client.ObjectKey{Namespace: postgres.Namespace, Name: v1.PostgresBranchObjectName(postgres.Name, v1.DefaultBranchName)}
				requireNoError(t, reader.Get(t.Context(), key, branch))
				if branch.Spec.Postgres != postgres.Name || branch.Spec.BranchName != v1.DefaultBranchName {
					t.Fatalf("created unexpected branch: %+v", branch.Spec)
				}
				owner := metav1.GetControllerOf(branch)
				if owner == nil || owner.UID != postgres.UID {
					t.Fatalf("branch controller = %v, want Postgres %q", owner, postgres.UID)
				}
				requireNoError(t, reader.Get(t.Context(), client.ObjectKeyFromObject(postgres), postgres))
				if postgres.Status.ActiveBranch != v1.DefaultBranchName {
					t.Fatalf("active branch = %q, want main", postgres.Status.ActiveBranch)
				}
			}
		})
	}
}

func TestPostgresSwitchBackToMain(t *testing.T) {
	for _, tt := range []struct {
		name         string
		branchExists bool
		clusterReady bool
		wantErr      bool
	}{
		{name: "missing main is not recreated", wantErr: true},
		{name: "main not ready", branchExists: true, wantErr: true},
		{name: "main ready", branchExists: true, clusterReady: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			postgres := &v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", UID: "postgres-uid"},
				Spec:       v1.PostgresSpec{ActiveBranch: v1.DefaultBranchName},
				Status:     &v1.PostgresStatus{ActiveBranch: "restored"},
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(postgres).WithObjects(postgres).Build()
			branch := &v1.PostgresBranch{
				ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName(postgres.Name, v1.DefaultBranchName), Namespace: postgres.Namespace},
				Spec:       v1.PostgresBranchSpec{Postgres: postgres.Name, BranchName: v1.DefaultBranchName},
			}
			if tt.branchExists {
				requireNoError(t, reader.Create(t.Context(), branch))
				cluster := &cnpgv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{Name: v1.CNPGClusterName(branch.Name), Namespace: postgres.Namespace},
					Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{
						{Type: string(cnpgv1.ConditionInitialized), Status: metav1.ConditionTrue},
					}},
				}
				if tt.clusterReady {
					cluster.Status.Conditions = append(cluster.Status.Conditions, metav1.Condition{Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionTrue})
				}
				requireNoError(t, reader.Create(t.Context(), cluster))
			}
			r := &PostgresReconciler{Scheme: scheme, Recorder: recorder}
			sync := synchronizer.NewSynchronizer(reader, scheme, r, recorder)
			_, err := sync.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(postgres)})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Reconcile() error = %v, wantErr %v", err, tt.wantErr)
			}
			requireNoError(t, reader.Get(t.Context(), client.ObjectKeyFromObject(postgres), postgres))
			wantActive := "restored"
			if tt.clusterReady {
				wantActive = v1.DefaultBranchName
			}
			if postgres.Status.ActiveBranch != wantActive {
				t.Errorf("active branch = %q, want %q", postgres.Status.ActiveBranch, wantActive)
			}
			if !tt.branchExists {
				if err := reader.Get(t.Context(), client.ObjectKeyFromObject(branch), &v1.PostgresBranch{}); !apierrors.IsNotFound(err) {
					t.Fatalf("missing main was recreated: %v", err)
				}
			}
		})
	}
}

func TestPostgresBootstrapRejectsConflictingBranch(t *testing.T) {
	for _, activeBranch := range []string{"", v1.DefaultBranchName} {
		t.Run("activeBranch="+activeBranch, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			postgres := &v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", UID: "postgres-uid"},
				Spec:       v1.PostgresSpec{ActiveBranch: activeBranch},
			}
			branch := &v1.PostgresBranch{
				ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName(postgres.Name, v1.DefaultBranchName), Namespace: postgres.Namespace},
				Spec:       v1.PostgresBranchSpec{Postgres: "another-database", BranchName: v1.DefaultBranchName},
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(postgres).WithObjects(postgres, branch).Build()
			r := &PostgresReconciler{Scheme: scheme, Recorder: recorder}
			sync := synchronizer.NewSynchronizer(reader, scheme, r, recorder)
			_, err := sync.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(postgres)})
			if err == nil {
				t.Fatal("expected conflicting branch to prevent bootstrap")
			}
			requireNoError(t, reader.Get(t.Context(), client.ObjectKeyFromObject(branch), branch))
			if branch.Spec.Postgres != "another-database" {
				t.Fatalf("conflicting branch was overwritten: %+v", branch.Spec)
			}
		})
	}
}
