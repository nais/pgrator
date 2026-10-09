package controller

import (
	"testing"

	"github.com/nais/pgrator/internal/initscheme"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestBranchDeletionPolicy(t *testing.T) {
	testScheme := runtime.NewScheme()
	initscheme.InitScheme(testScheme)
	for _, tt := range []struct {
		name, target, requested, observed, selected string
		parentDeleting, parentGone, onlyBranch      bool
		otherDeleting, blocked                      bool
	}{
		{name: "explicit inactive branch", target: "pr-123", observed: "main", selected: "pr-123"},
		{name: "explicit main after activation", target: "main", requested: "restored", observed: "restored", selected: "main"},
		{name: "observed active during pending activation", target: "main", requested: "restored", observed: "main", blocked: true},
		{name: "pending activation branch", target: "restored", requested: "restored", observed: "main", blocked: true},
		{name: "unreferenced inactive branch", target: "pr-123", observed: "main", selected: "other"},
		{name: "last inactive branch", target: "pr-123", observed: "missing", onlyBranch: true, blocked: true},
		{name: "other branch is already deleting", target: "pr-123", observed: "missing", otherDeleting: true, blocked: true},
		{name: "whole Postgres deletion", target: "pr-123", observed: "main", selected: "pr-123", parentDeleting: true, onlyBranch: true},
		{name: "Postgres is gone", target: "pr-123", parentGone: true, onlyBranch: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveBranch: tt.requested}, Status: &v1.PostgresStatus{ActiveBranch: tt.observed}}
			if tt.parentDeleting {
				postgres.Finalizers = []string{"postgres.nais.io"}
				postgres.DeletionTimestamp = new(metav1.Now())
			}
			branch := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", tt.target), Namespace: "team", Finalizers: []string{"postgresbranch.nais.io"}, DeletionTimestamp: new(metav1.Now())}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: tt.target}}
			binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: "team"}, Spec: v1.PostgresBindingSpec{Postgres: "orders", Branch: tt.selected}}
			objects := []client.Object{branch, binding}
			if !tt.parentGone {
				objects = append(objects, postgres)
			}
			if !tt.onlyBranch {
				other := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "other"), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "other"}}
				if tt.otherDeleting {
					other.Finalizers = []string{"postgresbranch.nais.io"}
					other.DeletionTimestamp = new(metav1.Now())
				}
				objects = append(objects, other)
			}
			r := &PostgresBranchReconciler{Recorder: recorder}
			builder := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(objects...)
			for _, index := range r.Indexes() {
				builder.WithIndex(index.Object, index.Field, index.ExtractValue)
			}
			prepared, _, err := r.Prepare(t.Context(), builder.Build(), branch)
			requireNoError(t, err)
			_, result, err := r.Delete(branch, prepared, nil)
			requireNoError(t, err)
			requireEqual(t, result.RequeueAfter > 0, tt.blocked, "branch deletion policy")
		})
	}
}
