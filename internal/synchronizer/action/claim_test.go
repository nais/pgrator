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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type claimRecorder struct{ claims int }

func (r *claimRecorder) RecordEvent(_ api.NaisObject, _, reason, _ string, _ ...any) {
	if reason == "Claimed" {
		r.claims++
	}
}

func TestClaimPersistsPlannedControllerReference(t *testing.T) {
	ctx := t.Context()
	scheme := newExclusiveCreateOrUpdateScheme(t)
	owner := newExclusiveOwner("owner")
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "child", Namespace: owner.Namespace}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	manager := ownership.NewOwnerManager("test/owner")
	// An existing annotation must not prevent repairing the controller reference.
	manager.AddOwnerAnnotation(obj, owner)
	if err := c.Update(ctx, obj); err != nil {
		t.Fatal(err)
	}
	planned := obj.DeepCopy()
	if err := controllerutil.SetControllerReference(owner, planned, scheme); err != nil {
		t.Fatal(err)
	}
	get := func(client.Object, *runtime.Scheme) []metav1.Condition { return nil }
	if err := Claim(planned, owner, get, &claimRecorder{}).Do(ctx, c, scheme, manager); err != nil {
		t.Fatal(err)
	}
	stored := &corev1.ConfigMap{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), stored); err != nil {
		t.Fatal(err)
	}
	if !metav1.IsControlledBy(stored, owner) {
		t.Fatalf("controller reference was not persisted: %v", stored.OwnerReferences)
	}
}

func TestClaimRejectsChangedOwnership(t *testing.T) {
	for _, state := range []string{"another controller", "previous controller UID", "replaced object"} {
		t.Run(state, func(t *testing.T) {
			scheme := newExclusiveCreateOrUpdateScheme(t)
			owner := newExclusiveOwner("owner")
			planned := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "child", Namespace: owner.Namespace, UID: "child-uid"}}
			stored := planned.DeepCopy()
			if err := controllerutil.SetControllerReference(owner, planned, scheme); err != nil {
				t.Fatal(err)
			}
			if state != "replaced object" {
				other := newExclusiveOwner("other")
				if state == "previous controller UID" {
					other.Name = owner.Name
				}
				if err := controllerutil.SetControllerReference(other, stored, scheme); err != nil {
					t.Fatal(err)
				}
			} else {
				stored.UID = "replacement-uid"
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stored).Build()
			manager := ownership.NewOwnerManager("test/owner")
			if err := Claim(planned, owner, noConditions, &claimRecorder{}).Do(t.Context(), c, scheme, manager); err == nil {
				t.Fatal("claim accepted changed resource ownership")
			}
			actual := &corev1.ConfigMap{}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(stored), actual); err != nil {
				t.Fatal(err)
			}
			if manager.HasOwnerAnnotation(actual, owner) || metav1.IsControlledBy(actual, owner) {
				t.Fatal("claim changed ownership despite refusing the resource")
			}
		})
	}
}

func TestSharedClaimPreservesOtherController(t *testing.T) {
	scheme := newExclusiveCreateOrUpdateScheme(t)
	owner := newExclusiveOwner("owner")
	controller := newExclusiveOwner("controller")
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: owner.Namespace}}
	if err := controllerutil.SetControllerReference(controller, obj, scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	manager := ownership.NewOwnerManager("test/owner")
	if err := Claim(obj, owner, noConditions, &claimRecorder{}).Do(t.Context(), c, scheme, manager); err != nil {
		t.Fatal(err)
	}
	stored := &corev1.ConfigMap{}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(obj), stored); err != nil {
		t.Fatal(err)
	}
	if !metav1.IsControlledBy(stored, controller) || !manager.HasOwnerAnnotation(stored, owner) {
		t.Fatalf("shared claim changed the controller or omitted the annotation: %+v", stored.ObjectMeta)
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
