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
