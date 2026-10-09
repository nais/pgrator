package controller

import (
	"testing"

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

func TestDeletedInactiveMainIsNotRecreated(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", UID: "postgres-uid"}, Status: &v1.PostgresStatus{ActiveBranch: "restored"}}
	main := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "main"), Namespace: "team", UID: "main-uid"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"}}
	restored := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "restored"), Namespace: "team", UID: "restored-uid"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "restored"}}
	r := &PostgresReconciler{Scheme: scheme, Recorder: recorder}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(postgres).WithObjects(postgres, main, restored).Build()
	sync := synchronizer.NewSynchronizer(reader, scheme, r, recorder)
	for _, branch := range []*v1.PostgresBranch{main, restored} {
		sync.GetOwnerManager().AddOwnerAnnotation(branch, postgres)
		requireNoError(t, reader.Update(t.Context(), branch))
	}
	requireNoError(t, reader.Delete(t.Context(), main))
	for range 2 {
		_, err := sync.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(postgres)})
		requireNoError(t, err)
		if err := reader.Get(t.Context(), client.ObjectKeyFromObject(main), &v1.PostgresBranch{}); !apierrors.IsNotFound(err) {
			t.Fatalf("deleted inactive main was recreated: %v", err)
		}
		requireNoError(t, reader.Get(t.Context(), client.ObjectKeyFromObject(restored), &v1.PostgresBranch{}))
	}
}
