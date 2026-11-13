package controller

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/google/go-cmp/cmp"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestDirectOwnershipPreservesResourcesWithoutBranchAPI(t *testing.T) {
	for _, wal := range []bool{false, true} {
		t.Run(fmt.Sprintf("wal=%t", wal), func(t *testing.T) {
			r, c, pg, _, before := directOwnershipFixture(t, wal)
			guard := withoutBranchAPI(t, c)
			for range 3 {
				s := synchronizer.NewSynchronizer(guard, scheme.Scheme, r, recorder)
				_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
				requireNoError(t, err)
			}
			for _, original := range before {
				got := original.DeepCopyObject().(client.Object)
				requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(got), got))
				assertSameFunctionalResource(t, original, got)
			}
			requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(pg), pg))
			if pg.Status.Ownership.Phase != ownershipManaged {
				t.Fatal("lost managed ownership checkpoint")
			}
		})
	}
}

func TestDirectOwnershipRejectsIncompleteMigration(t *testing.T) {
	for _, state := range []string{"no checkpoint", "adopting", "missing cluster", "replaced cluster"} {
		t.Run(state, func(t *testing.T) {
			r, c, pg, _, _ := directOwnershipFixture(t, false)
			switch state {
			case "no checkpoint":
				pg.Status.Ownership = nil
				requireNoError(t, c.Status().Update(t.Context(), pg))
			case "adopting":
				pg.Status.Ownership.Phase = ownershipAdopting
				requireNoError(t, c.Status().Update(t.Context(), pg))
			default:
				cluster := &cnpgv1.Cluster{}
				requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: pg.Status.ClusterName}, cluster))
				requireNoError(t, c.Delete(t.Context(), cluster))
				if state == "replaced cluster" {
					cluster.UID = "foreign-cluster"
					cluster.ResourceVersion = ""
					requireNoError(t, c.Create(t.Context(), cluster))
				}
			}
			s := synchronizer.NewSynchronizer(withoutBranchAPI(t, c), scheme.Scheme, r, recorder)
			if _, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)}); err == nil {
				t.Fatal("accepted unsafe/incomplete migration")
			}
		})
	}
}

func TestDirectOwnershipRequiresDurableCheckpoint(t *testing.T) {
	r, c, pg, _, children := directOwnershipFixture(t, false)
	failing := interceptor.NewClient(c.(client.WithWatch), interceptor.Funcs{SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
		return errors.New("status write interrupted")
	}})
	s := synchronizer.NewSynchronizer(failing, scheme.Scheme, r, recorder)
	if _, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)}); err == nil {
		t.Fatal("continued without durable checkpoint")
	}
	for _, original := range children {
		got := original.DeepCopyObject().(client.Object)
		requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(got), got))
		assertSameFunctionalResource(t, original, got)
	}
}

func TestDirectOwnershipDeletionWithoutBranchAPI(t *testing.T) {
	r, c, pg, _, children := directOwnershipFixture(t, true)
	requireNoError(t, c.Delete(t.Context(), pg))
	for range 3 {
		s := synchronizer.NewSynchronizer(withoutBranchAPI(t, c), scheme.Scheme, r, recorder)
		_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
		requireNoError(t, err)
	}
	for _, child := range append(children, pg) {
		if err := c.Get(t.Context(), client.ObjectKeyFromObject(child), child); !apierrors.IsNotFound(err) {
			t.Fatalf("deletion incomplete for %T %s: %v", child, child.GetName(), err)
		}
	}
}

func TestDeletingPostgresRejectsPartiallyAdoptedInventory(t *testing.T) {
	r, c, pg, identity, children := directOwnershipFixture(t, false)
	pg.Status.Ownership.Phase = ownershipAdopting
	requireNoError(t, c.Status().Update(t.Context(), pg))
	branch := identity.DeepCopy()
	branch.UID = types.UID(pg.Status.Ownership.LegacyBranchUID)
	branch.Finalizers = []string{"postgresbranch.nais.io"}
	requireNoError(t, c.Create(t.Context(), branch))
	// Simulate a crash after the cluster transfer but before another child was
	// transferred. That child is absent from the direct owner's live inventory.
	remaining := children[0].DeepCopyObject().(client.Object)
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(remaining), remaining))
	remaining.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: v1.GroupVersion.String(), Kind: "PostgresBranch", Name: branch.Name, UID: branch.UID, Controller: new(true)}})
	owners := ownership.NewOwnerManager("postgres.nais.io/owner")
	owners.RemoveOwnerAnnotation(remaining, pg)
	requireNoError(t, c.Update(t.Context(), remaining))
	requireNoError(t, c.Delete(t.Context(), pg))
	s := synchronizer.NewSynchronizer(withoutBranchAPI(t, c), scheme.Scheme, r, recorder)
	if _, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)}); err == nil {
		t.Fatal("started deletion while adoption was unfinished")
	}
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(pg), pg))
	if !slices.Contains(pg.Finalizers, "postgres.nais.io") {
		t.Fatal("released parent finalizer during unfinished adoption")
	}
	for i, original := range children {
		if i == 0 {
			original = remaining
		}
		got := original.DeepCopyObject().(client.Object)
		requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(got), got))
		assertSameFunctionalResource(t, original, got)
	}
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(branch), branch))
	if !slices.Contains(branch.Finalizers, "postgresbranch.nais.io") {
		t.Fatal("removed legacy branch finalizer")
	}
}

func TestDirectOwnershipRetainsArchivesWhenWALDisabled(t *testing.T) {
	r, c, pg, _, before := directOwnershipFixture(t, true)
	r.Config.CNPG.WalBucketPrefix = ""
	s := synchronizer.NewSynchronizer(withoutBranchAPI(t, c), scheme.Scheme, r, recorder)
	_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
	requireNoError(t, err)
	for _, original := range before {
		got := original.DeepCopyObject().(client.Object)
		requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(got), got))
		if got.GetUID() != original.GetUID() {
			t.Fatal("replaced an existing resource")
		}
	}
}

func TestPostgresDirectProvisioningCreatesNoBranch(t *testing.T) {
	r, _, _, _, _ := directOwnershipFixture(t, false)
	c := k8sClient
	pg := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "fresh-direct-ownership", Namespace: "default"}, Spec: v1.PostgresSpec{MajorVersion: "18"}}
	requireNoError(t, c.Create(t.Context(), pg))
	for range 3 {
		s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
		_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
		requireNoError(t, err)
	}
	cluster := &cnpgv1.Cluster{}
	requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: v1.CNPGClusterName(v1.PostgresPhysicalName(pg.Name))}, cluster))
	if !physicalControlledBy(cluster, pg) {
		t.Fatal("cluster not directly owned")
	}
	if err := c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: v1.PostgresPhysicalName(pg.Name)}, &v1.PostgresBranch{}); !apierrors.IsNotFound(err) {
		t.Fatalf("created a synthetic branch: %v", err)
	}
}

func withoutBranchAPI(t *testing.T, c client.Client) client.Client {
	t.Helper()
	return interceptor.NewClient(c.(client.WithWatch), interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, ok := obj.(*v1.PostgresBranch); ok {
				t.Error("read retired branch API")
				return errors.New("branch CRD is not installed")
			}
			return c.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*v1.PostgresBranchList); ok {
				t.Error("listed retired branch API")
				return errors.New("branch CRD is not installed")
			}
			return c.List(ctx, list, opts...)
		},
	})
}

func assertSameFunctionalResource(t *testing.T, before, after client.Object) {
	t.Helper()
	want, err := runtime.DefaultUnstructuredConverter.ToUnstructured(before)
	requireNoError(t, err)
	got, err := runtime.DefaultUnstructuredConverter.ToUnstructured(after)
	requireNoError(t, err)
	for _, object := range []map[string]any{want, got} {
		delete(object, "apiVersion")
		delete(object, "kind")
		metadata := object["metadata"].(map[string]any)
		delete(metadata, "resourceVersion")
		delete(metadata, "managedFields")
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("functional resource changed %T %s: %s", before, before.GetName(), diff)
	}
}

func directOwnershipFixture(t *testing.T, wal bool) (*PostgresClusterReconciler, client.Client, *v1.Postgres, *v1.PostgresBranch, []client.Object) {
	t.Helper()
	cfg := &config.Config{APIServerIP: "172.16.0.2/32", GoogleProjectID: "cluster-project", Google: config.Google{Location: "europe-north1"}, CNPG: config.CNPG{ImageCatalogName: "postgresql", StorageClass: "hyperdisk-balanced"}}
	if wal {
		cfg.CNPG.WalBucketPrefix = "nais-wal-dev"
	}
	pg := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "test", UID: "postgres-uid", Finalizers: []string{"postgres.nais.io"}}, Spec: v1.PostgresSpec{MajorVersion: "18"}, Status: &v1.PostgresStatus{ActiveBranch: "main", ClusterName: v1.CNPGClusterName(v1.PostgresPhysicalName("orders")), Ownership: &v1.PostgresOwnership{Phase: ownershipManaged, LegacyBranchUID: "historical-branch-uid"}}}
	// This is only an in-memory physical-name adapter for the shared renderer.
	// There is no Kubernetes branch object in the client or fixture.
	identity := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresPhysicalName(pg.Name), Namespace: pg.Namespace}, Spec: v1.PostgresBranchSpec{Postgres: pg.Name, BranchName: "main"}}
	r := &PostgresClusterReconciler{Config: cfg, Scheme: scheme.Scheme, Recorder: recorder}
	prep := PostgresBranchPreparedData{PostgresUID: pg.UID, PostgresName: pg.Name, PostgresSpec: pg.Spec, TeamGoogleProjectID: "team-project"}
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	planned, _, err := r.legacy().physicalActions(identity, pg, prep, related)
	requireNoError(t, err)
	for _, a := range planned {
		if cluster, ok := a.GetObject().(*cnpgv1.Cluster); ok {
			cluster.Status.Conditions = []metav1.Condition{{Type: string(cnpgv1.ConditionContinuousArchiving), Status: metav1.ConditionTrue}}
		}
		related.Insert(a.GetObject())
	}
	planned, _, err = r.legacy().physicalActions(identity, pg, prep, related)
	requireNoError(t, err)
	objects := make([]client.Object, 0, len(planned)+2)
	children := make([]client.Object, 0, len(planned))
	for i, a := range planned {
		obj := a.GetObject()
		obj.SetUID(types.UID(fmt.Sprintf("child-%d", i)))
		obj.SetResourceVersion("")
		gvk, err := apiutil.GVKForObject(obj, scheme.Scheme)
		requireNoError(t, err)
		obj.GetObjectKind().SetGroupVersionKind(gvk)
		annotations := maps.Clone(obj.GetAnnotations())
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations["test/functional"] = "preserve"
		obj.SetAnnotations(annotations)
		ownership.NewOwnerManager("postgres.nais.io/owner").AddOwnerAnnotation(obj, pg)
		if cluster, ok := obj.(*cnpgv1.Cluster); ok {
			pg.Status.Ownership.ClusterUID = string(cluster.UID)
			cluster.Status.Conditions = []metav1.Condition{{Type: string(cnpgv1.ConditionContinuousArchiving), Status: metav1.ConditionTrue}}
		}
		children = append(children, obj.DeepCopyObject().(client.Object))
		objects = append(objects, obj)
	}
	requireNoError(t, r.checkInventory(pg.Status.Ownership, children))
	objects = append(objects, pg, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: pg.Namespace, Labels: map[string]string{ProjectIDLabel: "team-project"}}})
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithStatusSubresource(&v1.Postgres{}, &v1.PostgresBinding{}, &cnpgv1.Cluster{}).WithObjects(objects...).Build()
	return r, c, pg, identity, children
}
