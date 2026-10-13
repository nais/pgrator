package action

import (
	"context"
	"testing"

	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/pkg/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

type claimRecorder struct{ claims int }

func (r *claimRecorder) RecordEvent(_ api.NaisObject, _, reason, _ string, _ ...any) {
	if reason == "Claimed" {
		r.claims++
	}
}

func (*claimRecorder) RecordErrorEvent(api.NaisObject, string, error) {}

func TestClaimOnlyWritesWhenOwnershipChanges(t *testing.T) {
	ctx := context.Background()
	scheme := newExclusiveCreateOrUpdateScheme(t)
	owner := newExclusiveOwner("owner")
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: owner.Namespace}}
	patches := 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			patches++
			return c.Patch(ctx, obj, patch, opts...)
		},
	}).Build()
	manager := ownership.NewOwnerManager("test/owner")
	recorder := &claimRecorder{}
	observations := 0
	get := func(client.Object, *runtime.Scheme) []metav1.Condition { observations++; return nil }
	for range 3 {
		if err := Claim(obj, owner, get, recorder).Do(ctx, c, scheme, manager); err != nil {
			t.Fatal(err)
		}
	}
	if patches != 1 || recorder.claims != 1 || observations != 3 {
		t.Fatalf("patches=%d, claims=%d, observations=%d, want 1, 1, 3", patches, recorder.claims, observations)
	}
}
