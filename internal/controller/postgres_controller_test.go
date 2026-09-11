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

func TestUpdateSetsActiveInstanceStatus(t *testing.T) {
	tests := []struct {
		name   string
		spec   string
		status string
		want   string
	}{
		{name: "uses explicitly selected instance", spec: "super-restore", want: "super-restore"},
		{name: "retains previously selected instance when spec is removed", status: "super-restore", want: "super-restore"},
		{name: "defaults to original instance", want: "super-postgres"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			reconciler := &PostgresReconciler{Config: &config.Config{}, Scheme: scheme}
			postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "super-postgres", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveInstance: tt.spec}, Status: &v1.PostgresStatus{ActiveInstance: tt.status}}

			_, _, err := reconciler.Update(postgres, PostgresPreparedData{}, relatedobjectsmap.NewRelatedObjectsMap(scheme))
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}
			if got := postgres.Status.ActiveInstance; got != tt.want {
				t.Errorf("status.activeInstance = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUpdateRetainsOriginalInstanceWhenActiveInstanceChanges(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	reconciler := &PostgresReconciler{Config: &config.Config{}, Scheme: scheme}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "super-postgres", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveInstance: "super-restore"}}
	relatedObjects := relatedobjectsmap.NewRelatedObjectsMap(scheme)
	relatedObjects.Insert(&v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: postgres.Name, Namespace: postgres.Namespace}})

	actions, _, err := reconciler.Update(postgres, PostgresPreparedData{}, relatedObjects)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("Update() actions = %d, want 1", len(actions))
	}
	instance, ok := actions[0].GetObject().(*v1.PostgresInstance)
	if !ok {
		t.Fatalf("action object = %T, want PostgresInstance", actions[0].GetObject())
	}
	if instance.GetName() != postgres.GetName() {
		t.Errorf("instance name = %q, want %q", instance.GetName(), postgres.GetName())
	}
}

func TestUpdateKeepsAllInstancesWhenActiveInstanceChanges(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	reconciler := &PostgresReconciler{Config: &config.Config{}, Scheme: scheme}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveInstance: "orders-restored"}}
	relatedObjects := relatedobjectsmap.NewRelatedObjectsMap(scheme)
	relatedObjects.Insert(&v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresInstanceSpec{Postgres: "orders"}})
	relatedObjects.Insert(&v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders-restored", Namespace: "team"}, Spec: v1.PostgresInstanceSpec{Postgres: "orders"}})
	relatedObjects.Insert(&v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "other-team"}, Spec: v1.PostgresInstanceSpec{Postgres: "orders"}})

	actions, _, err := reconciler.Update(postgres, PostgresPreparedData{}, relatedObjects)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if len(actions) != 2 {
		t.Fatalf("Update() actions = %d, want 2", len(actions))
	}

	claimed := map[string]bool{}
	for _, action := range actions {
		instance, ok := action.GetObject().(*v1.PostgresInstance)
		if !ok {
			t.Fatalf("action object = %T, want PostgresInstance", action.GetObject())
		}
		claimed[instance.Namespace+"/"+instance.Name] = true
	}
	for _, name := range []string{"orders", "orders-restored"} {
		if !claimed["team/"+name] {
			t.Errorf("PostgresInstance %q was not kept referenced", name)
		}
	}
	if claimed["other-team/orders"] {
		t.Error("PostgresInstance in another namespace was kept referenced")
	}
}
