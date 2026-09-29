package controller

import (
	"testing"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/initscheme"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

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
		{name: "defaults to original instance", want: "super-postgres"},
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
	relatedObjects.Insert(&v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: postgres.Name, Namespace: postgres.Namespace}})

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
	if instance.GetName() != postgres.GetName() {
		t.Errorf("instance name = %q, want %q", instance.GetName(), postgres.GetName())
	}
}

func TestUpdateKeepsAllInstancesWhenActiveBranchChanges(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	reconciler := &PostgresReconciler{Config: &config.Config{}, Recorder: recorder, Scheme: scheme}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveBranch: "orders-restored"}}
	relatedObjects := relatedobjectsmap.NewRelatedObjectsMap(scheme)
	relatedObjects.Insert(&v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders"}})
	relatedObjects.Insert(&v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: "orders-restored", Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders"}})
	relatedObjects.Insert(&v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "other-team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders"}})

	actions, _, err := reconciler.Update(postgres, PostgresPreparedData{RequestedBranch: "orders-restored", RequestedReady: true}, relatedObjects)
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
	for _, name := range []string{"orders", "orders-restored"} {
		if !claimed["team/"+name] {
			t.Errorf("PostgresBranch %q was not kept referenced", name)
		}
	}
	if claimed["other-team/orders"] {
		t.Error("PostgresBranch in another namespace was kept referenced")
	}
}
