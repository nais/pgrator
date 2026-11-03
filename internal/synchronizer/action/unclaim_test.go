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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestUnclaimRejectsReplacementResource(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "controller only"
		if shared {
			name = "shared annotations"
		}
		t.Run(name, func(t *testing.T) {
			scheme := newExclusiveCreateOrUpdateScheme(t)
			owner := newExclusiveOwner("owner")
			other := newExclusiveOwner("other")
			manager := ownership.NewOwnerManager("test/owner")
			planned := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "child", Namespace: owner.Namespace, UID: "original-uid"}}
			if err := controllerutil.SetControllerReference(owner, planned, scheme); err != nil {
				t.Fatal(err)
			}
			replacement := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: planned.Name, Namespace: planned.Namespace, UID: "replacement-uid"}}
			if err := controllerutil.SetControllerReference(other, replacement, scheme); err != nil {
				t.Fatal(err)
			}
			if shared {
				manager.AddOwnerAnnotation(replacement, owner)
				manager.AddOwnerAnnotation(replacement, other)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(replacement).Build()
			err := Unclaim(planned, owner, noConditions, &claimRecorder{}).Do(t.Context(), c, scheme, manager)
			if err == nil {
				t.Error("unclaim accepted a replacement resource")
			}
			stored := &corev1.ConfigMap{}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(replacement), stored); err != nil {
				t.Fatalf("replacement resource was deleted: %v", err)
			}
			if stored.ResourceVersion != replacement.ResourceVersion {
				t.Fatal("replacement resource was modified")
			}
		})
	}
}

func TestUnclaimUsesObservedUIDForDeletion(t *testing.T) {
	scheme := newExclusiveCreateOrUpdateScheme(t)
	owner := newExclusiveOwner("owner")
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "child", Namespace: owner.Namespace, UID: "child-uid"}}
	deleted := false
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, actual client.Object, opts ...client.DeleteOption) error {
			options := (&client.DeleteOptions{}).ApplyOptions(opts)
			if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != obj.UID {
				t.Fatal("unclaim delete omitted the observed UID precondition")
			}
			if options.Preconditions.ResourceVersion == nil || *options.Preconditions.ResourceVersion != actual.GetResourceVersion() {
				t.Fatal("unclaim delete omitted the observed resource version precondition")
			}
			deleted = true
			return c.Delete(ctx, actual, opts...)
		},
	}).Build()
	if err := Unclaim(obj, owner, noConditions, &claimRecorder{}).Do(t.Context(), c, scheme, ownership.NewOwnerManager("test/owner")); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("unclaim did not delete the last owner's resource")
	}
}

func TestUnclaimRejectsConcurrentOwnershipChanges(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "last owner deletion"
		if shared {
			name = "shared annotation patch"
		}
		t.Run(name, func(t *testing.T) {
			scheme := newExclusiveCreateOrUpdateScheme(t)
			owner := newExclusiveOwner("owner")
			other := newExclusiveOwner("other")
			newOwner := newExclusiveOwner("new-owner")
			manager := ownership.NewOwnerManager("test/owner")
			obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "child", Namespace: owner.Namespace, UID: "child-uid"}}
			manager.AddOwnerAnnotation(obj, owner)
			if shared {
				manager.AddOwnerAnnotation(obj, other)
			}
			claimBeforeWrite := func(ctx context.Context, c client.WithWatch) {
				t.Helper()
				live := &corev1.ConfigMap{}
				if err := c.Get(ctx, client.ObjectKeyFromObject(obj), live); err != nil {
					t.Fatal(err)
				}
				manager.AddOwnerAnnotation(live, newOwner)
				if err := c.Update(ctx, live); err != nil {
					t.Fatal(err)
				}
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, actual client.Object, opts ...client.DeleteOption) error {
					claimBeforeWrite(ctx, c)
					return c.Delete(ctx, actual, opts...)
				},
				Patch: func(ctx context.Context, c client.WithWatch, actual client.Object, patch client.Patch, opts ...client.PatchOption) error {
					claimBeforeWrite(ctx, c)
					return c.Patch(ctx, actual, patch, opts...)
				},
			}).Build()
			if err := Unclaim(obj, owner, noConditions, &claimRecorder{}).Do(t.Context(), c, scheme, manager); err == nil {
				t.Fatal("unclaim accepted a concurrent ownership change")
			}
			stored := &corev1.ConfigMap{}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(obj), stored); err != nil {
				t.Fatalf("concurrently claimed resource was deleted: %v", err)
			}
			if !manager.HasOwnerAnnotation(stored, owner) || !manager.HasOwnerAnnotation(stored, newOwner) ||
				(shared && !manager.HasOwnerAnnotation(stored, other)) {
				t.Fatalf("concurrent ownership change was overwritten: %v", stored.Annotations)
			}
		})
	}
}

func TestUnclaimPreservesSharedResource(t *testing.T) {
	scheme := newExclusiveCreateOrUpdateScheme(t)
	owner := newExclusiveOwner("owner")
	other := newExclusiveOwner("other")
	manager := ownership.NewOwnerManager("test/owner")
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: owner.Namespace, UID: "child-uid"}}
	manager.AddOwnerAnnotation(obj, owner)
	manager.AddOwnerAnnotation(obj, other)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	if err := Unclaim(obj, owner, noConditions, &claimRecorder{}).Do(t.Context(), c, scheme, manager); err != nil {
		t.Fatal(err)
	}
	stored := &corev1.ConfigMap{}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(obj), stored); err != nil {
		t.Fatal(err)
	}
	if manager.HasOwnerAnnotation(stored, owner) || !manager.HasOwnerAnnotation(stored, other) {
		t.Fatalf("unexpected shared ownership after unclaim: %v", stored.Annotations)
	}
}
