package action

import (
	"context"
	"reflect"
	"testing"

	"github.com/nais/pgrator/internal/synchronizer/ownership"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func adoptionFixture(t *testing.T) (*runtime.Scheme, *v1.PostgresBranch, *v1.Postgres, *corev1.ConfigMap) {
	t.Helper()
	scheme := newExclusiveCreateOrUpdateScheme(t)
	from := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: "orders-main", Namespace: "team", UID: "branch-uid"}}
	to := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", UID: "postgres-uid"}}
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: "physical-resource", Namespace: "team", UID: "child-uid", Finalizers: []string{"test/retain"},
		Labels: map[string]string{"keep": "label"}, Annotations: map[string]string{"keep": "annotation"},
	}, Data: map[string]string{"keep": "existing data"}}
	if err := controllerutil.SetControllerReference(from, obj, scheme); err != nil {
		t.Fatal(err)
	}
	obj.OwnerReferences = append(obj.OwnerReferences, metav1.OwnerReference{APIVersion: "v1", Kind: "ConfigMap", Name: "other-owner", UID: "other-uid"})
	ownership.NewOwnerManager("postgresbranch.nais.io/owner").AddOwnerAnnotation(obj, from)
	return scheme, from, to, obj
}

func TestAdoptOnlyChangesOwnershipAndIsIdempotent(t *testing.T) {
	scheme, from, to, obj := adoptionFixture(t)
	patches := 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(from, to, obj).WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			patches++
			return c.Patch(ctx, obj, patch, opts...)
		},
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			t.Fatal("adoption attempted create")
			return nil
		},
		Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
			t.Fatal("adoption attempted full update")
			return nil
		},
		Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
			t.Fatal("adoption attempted delete")
			return nil
		},
	}).Build()
	oldOwners := ownership.NewOwnerManager("postgresbranch.nais.io/owner")
	newOwners := ownership.NewOwnerManager("postgres.nais.io/owner")
	action := Adopt(obj, from, to, oldOwners, noConditions, &mockRecorder{})
	for range 3 {
		if err := action.Do(t.Context(), c, scheme, newOwners); err != nil {
			t.Fatal(err)
		}
	}
	stored := &corev1.ConfigMap{}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(obj), stored); err != nil {
		t.Fatal(err)
	}
	if patches != 1 || !metav1.IsControlledBy(stored, to) || !newOwners.HasOwnerAnnotation(stored, to) || oldOwners.HasOwnerAnnotation(stored, from) {
		t.Fatalf("ownership transfer was incomplete or not idempotent: patches=%d metadata=%+v", patches, stored.ObjectMeta)
	}
	if stored.UID != obj.UID || !reflect.DeepEqual(stored.Data, obj.Data) || !reflect.DeepEqual(stored.Labels, obj.Labels) || !reflect.DeepEqual(stored.Finalizers, obj.Finalizers) || stored.Annotations["keep"] != "annotation" {
		t.Fatal("adoption changed non-ownership data or metadata")
	}
	if len(stored.OwnerReferences) != 2 || stored.OwnerReferences[0].UID != "other-uid" {
		t.Error("adoption discarded an unrelated owner reference")
	}
}

func TestAdoptRefusesUnverifiedResources(t *testing.T) {
	for _, state := range []string{"changed UID", "no UID", "other controller", "no controller", "wrong controller kind", "wrong controller API", "deleting resource", "deleting owner", "replaced owner", "replaced previous owner", "deleting previous owner", "cross namespace", "missing resource"} {
		t.Run(state, func(t *testing.T) {
			scheme, from, to, planned := adoptionFixture(t)
			stored := planned.DeepCopy()
			actualOwner := to.DeepCopy()
			actualPrevious := from.DeepCopy()
			switch state {
			case "changed UID":
				stored.UID = "replacement-uid"
			case "no UID":
				planned.UID = ""
			case "other controller":
				stored.OwnerReferences[0].UID = "foreign-uid"
			case "no controller":
				stored.OwnerReferences = nil
			case "wrong controller kind":
				stored.OwnerReferences[0].Kind = "Secret"
			case "wrong controller API":
				stored.OwnerReferences[0].APIVersion = "foreign.example/v1"
			case "deleting resource":
				stored.DeletionTimestamp = new(metav1.Now())
			case "deleting owner":
				actualOwner.DeletionTimestamp = new(metav1.Now())
				actualOwner.Finalizers = []string{"test/retain"}
			case "replaced owner":
				actualOwner.UID = "replacement-owner-uid"
			case "replaced previous owner":
				actualPrevious.UID = "replacement-branch-uid"
			case "deleting previous owner":
				actualPrevious.DeletionTimestamp = new(metav1.Now())
				actualPrevious.Finalizers = []string{"test/retain"}
			case "cross namespace":
				to.Namespace = "foreign"
			}
			objects := []client.Object{actualPrevious, actualOwner}
			if state != "missing resource" {
				objects = append(objects, stored)
			}
			patches := 0
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
					patches++
					return nil
				},
			}).Build()
			err := Adopt(planned, from, to, ownership.NewOwnerManager("postgresbranch.nais.io/owner"), noConditions, &mockRecorder{}).Do(t.Context(), c, scheme, ownership.NewOwnerManager("postgres.nais.io/owner"))
			if err == nil || patches != 0 {
				t.Fatalf("unsafe adoption accepted: error=%v patches=%d", err, patches)
			}
		})
	}
}

func TestAdoptDoesNotOverwriteConcurrentMetadataChanges(t *testing.T) {
	scheme, from, to, obj := adoptionFixture(t)
	oldOwners := ownership.NewOwnerManager("postgresbranch.nais.io/owner")
	newOwners := ownership.NewOwnerManager("postgres.nais.io/owner")
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(from, to, obj).WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, planned client.Object, patch client.Patch, opts ...client.PatchOption) error {
			concurrent := &corev1.ConfigMap{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(planned), concurrent); err != nil {
				return err
			}
			concurrent.Labels["concurrent"] = "preserved"
			if err := c.Update(ctx, concurrent); err != nil {
				return err
			}
			return c.Patch(ctx, planned, patch, opts...)
		},
	}).Build()
	err := Adopt(obj, from, to, oldOwners, noConditions, &mockRecorder{}).Do(t.Context(), c, scheme, newOwners)
	if !apierrors.IsConflict(err) {
		t.Fatalf("concurrent update did not conflict: %v", err)
	}
	stored := &corev1.ConfigMap{}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(obj), stored); err != nil {
		t.Fatal(err)
	}
	if !metav1.IsControlledBy(stored, from) || !oldOwners.HasOwnerAnnotation(stored, from) || newOwners.HasOwnerAnnotation(stored, to) || stored.Labels["concurrent"] != "preserved" {
		t.Fatal("conflicting adoption partially changed ownership or overwrote metadata")
	}
}
