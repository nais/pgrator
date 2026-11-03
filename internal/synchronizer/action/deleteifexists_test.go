package action

import (
	"context"
	"testing"

	"github.com/nais/pgrator/internal/synchronizer/ownership"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestDeleteIfExistsUsesObservedUID(t *testing.T) {
	scheme := newExclusiveCreateOrUpdateScheme(t)
	owner := newExclusiveOwner("owner")
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "child", Namespace: owner.Namespace, UID: "child-uid"}}
	deleted := false
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, actual client.Object, opts ...client.DeleteOption) error {
			options := (&client.DeleteOptions{}).ApplyOptions(opts)
			if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != obj.UID {
				t.Fatalf("delete omitted the observed UID precondition: %+v", options.Preconditions)
			}
			deleted = true
			return c.Delete(ctx, actual, opts...)
		},
	}).Build()
	if err := DeleteIfExists(obj, owner, noConditions, &claimRecorder{}).Do(t.Context(), c, scheme, ownership.NewOwnerManager("test/owner")); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("delete was not performed")
	}
}
