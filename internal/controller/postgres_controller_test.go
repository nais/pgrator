package controller

import (
	"context"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/initscheme"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestClusterConditionGetter(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	for _, testcase := range []struct {
		name        string
		phase       string
		phaseReason string
		ready       *metav1.Condition
		wantStatus  metav1.ConditionStatus
		wantReason  string
		wantMessage string
	}{
		{
			name: "ready cluster", phase: "Cluster in healthy state",
			ready:      &metav1.Condition{Status: metav1.ConditionTrue, Reason: "ClusterIsReady"},
			wantStatus: metav1.ConditionTrue, wantReason: "ClusterIsReady",
			wantMessage: "Cluster is in phase: Cluster in healthy state",
		},
		{
			name: "invalid image catalog", phase: "Cluster has incomplete or invalid image catalog",
			phaseReason: `extension "dblink" found in the Cluster Spec but no ImageVolumeSource.Reference is defined`,
			ready:       &metav1.Condition{Status: metav1.ConditionFalse, Reason: "ClusterIsNotReady", Message: "Cluster Is Not Ready"},
			wantStatus:  metav1.ConditionFalse, wantReason: "ClusterIsNotReady",
			wantMessage: `Cluster is in phase: Cluster has incomplete or invalid image catalog: extension "dblink" found in the Cluster Spec but no ImageVolumeSource.Reference is defined`,
		},
		{
			name: "unknown readiness",
			ready:      &metav1.Condition{Status: metav1.ConditionUnknown, Reason: "Checking", Message: "Checking cluster readiness"},
			wantStatus: metav1.ConditionUnknown, wantReason: "Checking", wantMessage: "Checking cluster readiness",
		},
		{
			name: "missing readiness with phase", phase: "Setting up primary",
			wantStatus: metav1.ConditionUnknown, wantReason: "Unknown", wantMessage: "Cluster is in phase: Setting up primary",
		},
		{
			name: "missing status",
			wantStatus: metav1.ConditionUnknown, wantReason: "Unknown", wantMessage: "No readiness status available on source resource",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 3},
				Status:     cnpgv1.ClusterStatus{Phase: testcase.phase, PhaseReason: testcase.phaseReason},
			}
			if testcase.ready != nil {
				ready := *testcase.ready
				ready.Type = string(cnpgv1.ConditionClusterReady)
				cluster.Status.Conditions = []metav1.Condition{ready}
			}
			conditions := clusterConditionGetter(cluster, scheme)
			if len(conditions) != 1 {
				t.Fatalf("got %d conditions, want 1", len(conditions))
			}
			condition := conditions[0]
			if condition.Type != "cluster.postgresql.cnpg.io/ObservedState" || condition.ObservedGeneration != cluster.Generation {
				t.Errorf("unexpected condition metadata: %+v", condition)
			}
			if condition.Status != testcase.wantStatus || condition.Reason != testcase.wantReason || condition.Message != testcase.wantMessage {
				t.Errorf("got condition %+v, want status %q, reason %q, message %q", condition, testcase.wantStatus, testcase.wantReason, testcase.wantMessage)
			}
		})
	}
}

func TestUpdateSetsActiveBranchStatus(t *testing.T) {
	tests := []struct {
		name           string
		spec           string
		status         string
		requestedReady bool
		want           string
		wantErr        bool
	}{
		{name: "uses explicitly selected instance", spec: "super-restore", requestedReady: true, want: "super-restore"},
		{name: "retains previously selected instance when spec is removed", status: "super-restore", want: "super-restore"},
		{name: "defaults to main", want: v1.DefaultBranchName},
		{name: "requested instance not ready leaves status unchanged", spec: "super-restore", status: "super-postgres", want: "super-postgres", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			reconciler := &PostgresReconciler{Config: &config.Config{}, Recorder: recorder, Scheme: scheme}
			postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "super-postgres", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveBranch: tt.spec}, Status: &v1.PostgresStatus{ActiveBranch: tt.status}}
			prepared := PostgresPreparedData{RequestedBranch: tt.spec, RequestedReady: tt.requestedReady}

			_, _, err := reconciler.Update(postgres, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Update() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := postgres.Status.ActiveBranch; got != tt.want {
				t.Errorf("status.activeBranch = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestActivationWaitsForReadyBranch(t *testing.T) {
	for _, tt := range []struct {
		name            string
		branchDeleting  bool
		clusterDeleting bool
		clusterReady    bool
		recoveryFailed  bool
		wantActive      string
	}{
		{name: "ready", clusterReady: true, wantActive: "restore"},
		{name: "cluster not ready"},
		{name: "branch deleting", clusterReady: true, branchDeleting: true},
		{name: "cluster deleting", clusterReady: true, clusterDeleting: true},
		{name: "terminal recovery cannot be activated", clusterReady: true, recoveryFailed: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveBranch: "restore"}, Status: &v1.PostgresStatus{ActiveBranch: "main"}}
			branchName := v1.PostgresBranchObjectName("orders", "restore")
			branch := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: branchName, Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "restore"}}
			if tt.recoveryFailed {
				branch.GetStatus().SetCondition(metav1.Condition{Type: recoveryFailedCondition, Status: metav1.ConditionTrue, Reason: "Unrecoverable"})
			}
			cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: v1.CNPGClusterName(branchName), Namespace: "team"}, Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{Type: string(cnpgv1.ConditionInitialized), Status: metav1.ConditionTrue}}}}
			if tt.clusterReady {
				cluster.Status.Conditions = append(cluster.Status.Conditions, metav1.Condition{Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionTrue})
			}
			if tt.branchDeleting {
				now := metav1.Now()
				branch.DeletionTimestamp = &now
				branch.Finalizers = []string{"postgresbranch.nais.io"}
			}
			if tt.clusterDeleting {
				now := metav1.Now()
				cluster.DeletionTimestamp = &now
				cluster.Finalizers = []string{"postgresql.cnpg.io/finalizer"}
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(branch, cluster).Build()
			reconciler := &PostgresReconciler{Config: &config.Config{}, Recorder: recorder, Scheme: scheme}
			prepared, _, err := reconciler.Prepare(context.Background(), reader, postgres)
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}
			_, _, err = reconciler.Update(postgres, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme))
			if (err == nil) != (tt.wantActive != "") {
				t.Fatalf("Update() error = %v, want active branch %q", err, tt.wantActive)
			}
			want := tt.wantActive
			if want == "" {
				want = "main"
			}
			if postgres.Status.ActiveBranch != want {
				t.Errorf("status.activeBranch = %q, want %q", postgres.Status.ActiveBranch, want)
			}
		})
	}
}

func TestUpdateFailsActivationWhenRequestedBranchMissing(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	reconciler := &PostgresReconciler{Config: &config.Config{}, Recorder: recorder, Scheme: scheme}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "super-postgres", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveBranch: "missing"}, Status: &v1.PostgresStatus{ActiveBranch: "super-postgres"}}
	prepared := PostgresPreparedData{RequestedBranch: "missing", RequestedReady: false}

	_, _, err := reconciler.Update(postgres, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme))
	if err == nil {
		t.Fatal("Update() expected error, got nil")
	}
	if postgres.Status.ActiveBranch != "super-postgres" {
		t.Errorf("status.activeBranch = %q, want %q", postgres.Status.ActiveBranch, "super-postgres")
	}
}

func TestUpdateRetainsOriginalInstanceWhenActiveBranchChanges(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	reconciler := &PostgresReconciler{Config: &config.Config{}, Recorder: recorder, Scheme: scheme}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "super-postgres", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveBranch: "super-restore"}}
	relatedObjects := relatedobjectsmap.NewRelatedObjectsMap(scheme)
	relatedObjects.Insert(&v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName(postgres.Name, v1.DefaultBranchName), Namespace: postgres.Namespace}, Spec: v1.PostgresBranchSpec{Postgres: postgres.Name, BranchName: v1.DefaultBranchName}})

	actions, _, err := reconciler.Update(postgres, PostgresPreparedData{RequestedBranch: "super-restore", RequestedReady: true}, relatedObjects)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("Update() actions = %d, want 1", len(actions))
	}
	instance, ok := actions[0].GetObject().(*v1.PostgresBranch)
	if !ok {
		t.Fatalf("action object = %T, want PostgresBranch", actions[0].GetObject())
	}
	if instance.GetName() != v1.PostgresBranchObjectName(postgres.Name, v1.DefaultBranchName) {
		t.Errorf("instance name = %q, want derived main branch name", instance.GetName())
	}
}

func TestUpdateKeepsAllInstancesWhenActiveBranchChanges(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	reconciler := &PostgresReconciler{Config: &config.Config{}, Recorder: recorder, Scheme: scheme}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveBranch: "restored"}}
	relatedObjects := relatedobjectsmap.NewRelatedObjectsMap(scheme)
	relatedObjects.Insert(&v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "main"), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"}})
	relatedObjects.Insert(&v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "restored"), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "restored"}})
	relatedObjects.Insert(&v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "main"), Namespace: "other-team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"}})

	actions, _, err := reconciler.Update(postgres, PostgresPreparedData{RequestedBranch: "restored", RequestedReady: true}, relatedObjects)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(actions) != 2 {
		t.Fatalf("Update() actions = %d, want 2", len(actions))
	}

	claimed := map[string]bool{}
	for _, action := range actions {
		instance, ok := action.GetObject().(*v1.PostgresBranch)
		if !ok {
			t.Fatalf("action object = %T, want PostgresBranch", action.GetObject())
		}
		claimed[instance.Namespace+"/"+instance.Name] = true
	}
	for _, name := range []string{v1.PostgresBranchObjectName("orders", "main"), v1.PostgresBranchObjectName("orders", "restored")} {
		if !claimed["team/"+name] {
			t.Errorf("PostgresBranch %q was not kept referenced", name)
		}
	}
	if claimed["other-team/"+v1.PostgresBranchObjectName("orders", "main")] {
		t.Error("PostgresBranch in another namespace was kept referenced")
	}
}
