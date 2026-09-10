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
		name string
		spec string
		want string
	}{
		{name: "uses explicitly selected instance", spec: "super-restore", want: "super-restore"},
		{name: "defaults to original instance", want: "super-postgres"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			reconciler := &PostgresReconciler{Config: &config.Config{}, Scheme: scheme}
			postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "super-postgres", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveInstance: tt.spec}}

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
