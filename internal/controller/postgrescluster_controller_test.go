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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestPostgresOwnershipUpgradePreservesResources(t *testing.T) {
	for _, wal := range []bool{false, true} {
		t.Run(fmt.Sprintf("wal=%t", wal), func(t *testing.T) {
			r, c, pg, branch, before := legacyOwnershipFixture(t, wal)
			// CNPG descendants and workload/access-owned objects are not direct
			// branch children and must remain completely untouched.
			cluster := &cnpgv1.Cluster{}
			requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: v1.CNPGClusterName(branch.Name)}, cluster))
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "existing-client-certificate", Namespace: pg.Namespace, UID: "certificate"}, Data: map[string][]byte{"tls.key": []byte("unchanged-private-key")}}
			requireNoError(t, controllerutil.SetControllerReference(cluster, secret, scheme.Scheme))
			requireNoError(t, c.Create(t.Context(), secret))
			secretBefore := secret.DeepCopy()
			// A new synchronizer on each pass simulates process restarts. The
			// durable inventory, not in-memory preparation, drives resumption.
			for range 3 {
				s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
				_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
				requireNoError(t, err)
			}
			requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(pg), pg))
			if pg.Status.Ownership == nil || pg.Status.Ownership.Phase != ownershipManaged || pg.Status.ClusterName != cluster.Name || pg.Status.Ownership.ClusterUID != string(cluster.UID) {
				t.Fatalf("missing durable direct status: %+v", pg.Status)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(branch), &v1.PostgresBranch{}); !apierrors.IsNotFound(err) {
				t.Fatalf("legacy branch not detached: %v", err)
			}
			for _, original := range before {
				got := original.DeepCopyObject().(client.Object)
				requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(got), got))
				if !physicalControlledBy(got, pg, postgresKind) || ownership.NewOwnerManager("postgresbranch.nais.io/owner").HasOwnerAnnotation(got, branch) || !ownership.NewOwnerManager("postgres.nais.io/owner").HasOwnerAnnotation(got, pg) {
					t.Errorf("incomplete ownership transfer for %T %s", got, got.GetName())
				}
				assertSameFunctionalResource(t, original, got)
			}
			requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(secret), secret))
			if diff := cmp.Diff(secretBefore, secret); diff != "" {
				t.Fatalf("CNPG-owned credential changed (-want +got): %s", diff)
			}
		})
	}
}

func TestPostgresOwnershipRetriesPartialAdoption(t *testing.T) {
	r, c, pg, branch, before := legacyOwnershipFixture(t, true)
	p, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	// This is the durable write the synchronizer performs before the first action.
	requireNoError(t, c.Status().Update(t.Context(), pg))
	objects := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	for _, child := range before {
		objects.Insert(child)
	}
	objects.Insert(branch)
	actions, _, err := r.Update(pg, p, objects)
	requireNoError(t, err)
	owners := ownership.NewOwnerManager(r.Name() + "/owner")
	transferred := 0
	for _, a := range actions {
		requireNoError(t, a.Do(t.Context(), c, scheme.Scheme, owners))
		transferred = 0
		for _, child := range before {
			current := child.DeepCopyObject().(client.Object)
			requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(current), current))
			if physicalControlledBy(current, pg, postgresKind) {
				transferred++
			}
		}
		if transferred == 3 {
			break
		}
	}
	if transferred != 3 {
		t.Fatal("fixture did not interrupt a partially completed transfer")
	}
	for range 4 {
		s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
		_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
		requireNoError(t, err)
	}
	for _, original := range before {
		got := original.DeepCopyObject().(client.Object)
		requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(got), got))
		assertSameFunctionalResource(t, original, got)
	}
}

func TestPostgresOwnershipRejectsUnsafeTargets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, client.Client, *v1.Postgres, *v1.PostgresBranch)
	}{
		{name: "missing checkpointed cluster", change: func(t *testing.T, c client.Client, pg *v1.Postgres, b *v1.PostgresBranch) {
			requireNoError(t, c.Delete(t.Context(), &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: v1.CNPGClusterName(b.Name), Namespace: pg.Namespace}}))
		}},
		{name: "replaced checkpointed cluster", change: func(t *testing.T, c client.Client, pg *v1.Postgres, b *v1.PostgresBranch) {
			cluster := &cnpgv1.Cluster{}
			requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: v1.CNPGClusterName(b.Name)}, cluster))
			requireNoError(t, c.Delete(t.Context(), cluster))
			cluster.ResourceVersion = ""
			cluster.UID = "unexpected-cluster-uid"
			requireNoError(t, c.Create(t.Context(), cluster))
		}},
		{name: "missing checkpointed role", change: func(t *testing.T, c client.Client, pg *v1.Postgres, b *v1.PostgresBranch) {
			requireNoError(t, c.Delete(t.Context(), &cnpgv1.DatabaseRole{ObjectMeta: metav1.ObjectMeta{Name: v1.CNPGClusterName(b.Name) + "-app", Namespace: pg.Namespace}}))
		}},
		{name: "foreign cluster controller", change: func(t *testing.T, c client.Client, pg *v1.Postgres, b *v1.PostgresBranch) {
			cluster := &cnpgv1.Cluster{}
			requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: v1.CNPGClusterName(b.Name)}, cluster))
			cluster.OwnerReferences[0].UID = "foreign"
			requireNoError(t, c.Update(t.Context(), cluster))
		}},
		{name: "replaced branch", change: func(t *testing.T, c client.Client, pg *v1.Postgres, b *v1.PostgresBranch) {
			b.Finalizers = nil
			requireNoError(t, c.Update(t.Context(), b))
			requireNoError(t, c.Delete(t.Context(), b))
			b.ResourceVersion = ""
			b.UID = "unexpected-branch-uid"
			requireNoError(t, c.Create(t.Context(), b))
		}},
		{name: "non-main activation", change: func(t *testing.T, c client.Client, pg *v1.Postgres, b *v1.PostgresBranch) {
			pg.Spec.ActiveBranch = "restore"
			requireNoError(t, c.Update(t.Context(), pg))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, c, pg, b, _ := legacyOwnershipFixture(t, false)
			_, _, err := r.Prepare(t.Context(), c, pg)
			requireNoError(t, err)
			requireNoError(t, c.Status().Update(t.Context(), pg))
			tc.change(t, c, pg, b)
			s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
			if _, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)}); err == nil {
				t.Fatal("unsafe ownership transfer succeeded")
			}
		})
	}
}

func TestPostgresBranchDetachRefusesRemainingChildren(t *testing.T) {
	r, c, pg, b, _ := legacyOwnershipFixture(t, false)
	pg.Status.Ownership = &v1.PostgresOwnership{Phase: ownershipManaged, LegacyBranchUID: string(b.UID)}
	requireNoError(t, c.Status().Update(t.Context(), pg))
	a := &detachPostgresBranch{reconciler: r, postgres: pg, branch: b}
	if err := a.Do(t.Context(), c, scheme.Scheme, ownership.NewOwnerManager(r.Name()+"/owner")); err == nil {
		t.Fatal("detached branch with live children")
	}
	still := &v1.PostgresBranch{}
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(b), still))
	if !slices.Contains(still.Finalizers, "postgresbranch.nais.io") {
		t.Fatal("removed deletion guard despite remaining children")
	}
}

func TestPostgresOwnershipRequiresDurableCheckpoint(t *testing.T) {
	r, c, pg, b, children := legacyOwnershipFixture(t, false)
	failing := interceptor.NewClient(c.(client.WithWatch), interceptor.Funcs{SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
		return errors.New("status write interrupted")
	}})
	s := synchronizer.NewSynchronizer(failing, scheme.Scheme, r, recorder)
	if _, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)}); err == nil {
		t.Fatal("continued without durable checkpoint")
	}
	for _, child := range children {
		current := child.DeepCopyObject().(client.Object)
		requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(current), current))
		if !physicalControlledBy(current, b, "PostgresBranch") {
			t.Fatal("child changed before checkpoint committed")
		}
	}
}

func TestPostgresDeletionFinishesInterruptedAdoption(t *testing.T) {
	for _, adoptFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("partially-adopted=%t", adoptFirst), func(t *testing.T) {
			r, c, pg, branch, children := legacyOwnershipFixture(t, true)
			if adoptFirst {
				s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
				_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
				requireNoError(t, err)
				requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(pg), pg))
			}
			requireNoError(t, c.Delete(t.Context(), pg))
			for range 4 {
				s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
				_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
				requireNoError(t, err)
			}
			for _, child := range append(children, branch, pg) {
				if err := c.Get(t.Context(), client.ObjectKeyFromObject(child), child); !apierrors.IsNotFound(err) {
					t.Errorf("deletion did not finish for %T %s: %v", child, child.GetName(), err)
				}
			}
		})
	}
}

func TestPostgresOwnershipAdoptionIgnoresBuilderDrift(t *testing.T) {
	r, c, pg, _, before := legacyOwnershipFixture(t, true)
	// Adoption must not depend on rendering desired resources or current WAL
	// configuration, nor accidentally prune the legacy backup infrastructure.
	r.Config.CNPG.WalBucketPrefix = ""
	r.Config.CNPG.ImageCatalogName = "a-new-catalog"
	for range 2 {
		s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
		_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
		requireNoError(t, err)
	}
	for _, original := range before {
		got := original.DeepCopyObject().(client.Object)
		requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(got), got))
		assertSameFunctionalResource(t, original, got)
	}
}

func TestPostgresOwnershipDoesNotPruneForeignAnnotatedResources(t *testing.T) {
	r, c, pg, _, _ := legacyOwnershipFixture(t, false)
	// An annotation alone, especially outside the database namespace, must not
	// authorize deletion through the synchronizer's generic Unclaim path.
	foreign := &cnpgv1.DatabaseRole{ObjectMeta: metav1.ObjectMeta{Name: "foreign-role", Namespace: "another-team", UID: "foreign-role-uid"}}
	ownership.NewOwnerManager("postgres.nais.io/owner").AddOwnerAnnotation(foreign, pg)
	requireNoError(t, c.Create(t.Context(), foreign))
	before := foreign.DeepCopy()
	for range 3 {
		s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
		_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
		requireNoError(t, err)
	}
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(foreign), foreign))
	if diff := cmp.Diff(before, foreign); diff != "" {
		t.Fatalf("implicitly pruned/changed foreign resource: %s", diff)
	}
}

func TestPostgresDirectProvisioningCreatesNoBranch(t *testing.T) {
	r, _, _, _, _ := legacyOwnershipFixture(t, false)
	c := k8sClient
	pg := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "fresh-direct-ownership", Namespace: "default"}, Spec: v1.PostgresSpec{MajorVersion: "18"}}
	requireNoError(t, c.Create(t.Context(), pg))
	for range 3 {
		s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
		_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
		requireNoError(t, err)
	}
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(pg), pg))
	cluster := &cnpgv1.Cluster{}
	requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: v1.CNPGClusterName(v1.PostgresPhysicalName(pg.Name))}, cluster))
	if !physicalControlledBy(cluster, pg, postgresKind) {
		t.Fatal("new cluster not directly owned")
	}
	if err := c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: v1.PostgresPhysicalName(pg.Name)}, &v1.PostgresBranch{}); !apierrors.IsNotFound(err) {
		t.Fatalf("unexpected synthetic branch: %v", err)
	}
}

func TestPostgresOwnershipAPIServerRoundTrip(t *testing.T) {
	r, _, _, _, _ := legacyOwnershipFixture(t, false)
	c := k8sClient
	pg := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "ownership-upgrade-roundtrip", Namespace: "default", Finalizers: []string{"postgres.nais.io"}}, Spec: v1.PostgresSpec{MajorVersion: "18"}}
	requireNoError(t, c.Create(t.Context(), pg))
	b := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresPhysicalName(pg.Name), Namespace: pg.Namespace, Finalizers: []string{"postgresbranch.nais.io"}}, Spec: v1.PostgresBranchSpec{Postgres: pg.Name, BranchName: "main"}}
	requireNoError(t, controllerutil.SetControllerReference(pg, b, scheme.Scheme))
	ownership.NewOwnerManager("postgres.nais.io/owner").AddOwnerAnnotation(b, pg)
	requireNoError(t, c.Create(t.Context(), b))
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	planned, _, err := r.legacy().Update(b, PostgresBranchPreparedData{PostgresUID: pg.UID, PostgresName: pg.Name, PostgresSpec: pg.Spec}, related)
	requireNoError(t, err)
	before := make([]client.Object, 0, len(planned))
	for _, a := range planned {
		child := a.GetObject()
		ownership.NewOwnerManager("postgresbranch.nais.io/owner").AddOwnerAnnotation(child, b)
		requireNoError(t, c.Create(t.Context(), child))
		before = append(before, child.DeepCopyObject().(client.Object))
	}
	for range 2 {
		s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
		_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
		requireNoError(t, err)
	}
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(pg), pg))
	if pg.Status.Ownership == nil || pg.Status.Ownership.Phase != ownershipManaged || len(pg.Status.Ownership.Resources) != len(before) {
		t.Fatalf("ownership checkpoint was not persisted through CRD admission: %+v", pg.Status)
	}
	for _, original := range before {
		got := original.DeepCopyObject().(client.Object)
		requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(got), got))
		assertSameFunctionalResource(t, original, got)
		if !physicalControlledBy(got, pg, postgresKind) {
			t.Errorf("ownership not transferred for %s", got.GetName())
		}
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(b), &v1.PostgresBranch{}); !apierrors.IsNotFound(err) {
		t.Fatalf("branch not detached: %v", err)
	}
}

func assertSameFunctionalResource(t *testing.T, before, after client.Object) {
	t.Helper()
	want, err := runtime.DefaultUnstructuredConverter.ToUnstructured(before)
	requireNoError(t, err)
	got, err := runtime.DefaultUnstructuredConverter.ToUnstructured(after)
	requireNoError(t, err)
	// The only allowed adoption differences are resourceVersion, controller and
	// the two ownership annotations. All functional metadata and spec remain.
	for _, object := range []map[string]any{want, got} {
		// Typed clients may clear TypeMeta on read; it is not stored spec data.
		delete(object, "apiVersion")
		delete(object, "kind")
		metadata := object["metadata"].(map[string]any)
		delete(metadata, "resourceVersion")
		delete(metadata, "managedFields")
		delete(metadata, "ownerReferences")
		if annotations, ok := metadata["annotations"].(map[string]any); ok {
			delete(annotations, "postgres.nais.io/owner")
			delete(annotations, "postgresbranch.nais.io/owner")
			if len(annotations) == 0 {
				delete(metadata, "annotations")
			}
		}
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("functional resource changed %T %s (-want +got):\n%s", before, before.GetName(), diff)
	}
}

func legacyOwnershipFixture(t *testing.T, wal bool) (*PostgresClusterReconciler, client.Client, *v1.Postgres, *v1.PostgresBranch, []client.Object) {
	t.Helper()
	cfg := &config.Config{APIServerIP: "172.16.0.2/32", GoogleProjectID: "cluster-project", Google: config.Google{Location: "europe-north1"}, CNPG: config.CNPG{ImageCatalogName: "postgresql", StorageClass: "hyperdisk-balanced"}}
	if wal {
		cfg.CNPG.WalBucketPrefix = "nais-wal-dev"
	}
	pg := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "test", UID: "postgres-uid", Finalizers: []string{"postgres.nais.io"}}, Spec: v1.PostgresSpec{MajorVersion: "18"}, Status: &v1.PostgresStatus{ActiveBranch: "main"}}
	b := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresPhysicalName(pg.Name), Namespace: pg.Namespace, UID: "branch-uid", Finalizers: []string{"postgresbranch.nais.io"}}, Spec: v1.PostgresBranchSpec{Postgres: pg.Name, BranchName: "main"}}
	requireNoError(t, controllerutil.SetControllerReference(pg, b, scheme.Scheme))
	ownership.NewOwnerManager("postgres.nais.io/owner").AddOwnerAnnotation(b, pg)
	legacy := &PostgresBranchReconciler{Config: cfg, Scheme: scheme.Scheme, Recorder: recorder}
	prep := PostgresBranchPreparedData{PostgresName: pg.Name, PostgresUID: pg.UID, PostgresSpec: pg.Spec, TeamGoogleProjectID: "team-project"}
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	actions, _, err := legacy.Update(b, prep, related)
	requireNoError(t, err)
	for _, a := range actions {
		obj := a.GetObject()
		if cluster, ok := obj.(*cnpgv1.Cluster); ok {
			cluster.Status.Conditions = []metav1.Condition{{Type: string(cnpgv1.ConditionContinuousArchiving), Status: metav1.ConditionTrue}}
		}
		related.Insert(obj)
	}
	actions, _, err = legacy.Update(b, prep, related)
	requireNoError(t, err)
	objects := make([]client.Object, 0, len(actions)+3)
	objects = append(objects, pg, b, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: pg.Namespace, Labels: map[string]string{ProjectIDLabel: "team-project"}}})
	children := make([]client.Object, 0, len(actions))
	for i, a := range actions {
		obj := a.GetObject()
		obj.SetUID(types.UID(fmt.Sprintf("child-%d", i)))
		obj.SetResourceVersion("")
		annotations := maps.Clone(obj.GetAnnotations())
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations["test/functional"] = "preserve"
		obj.SetAnnotations(annotations)
		ownership.NewOwnerManager("postgresbranch.nais.io/owner").AddOwnerAnnotation(obj, b)
		if cluster, ok := obj.(*cnpgv1.Cluster); ok {
			cluster.Status.Conditions = []metav1.Condition{{Type: string(cnpgv1.ConditionContinuousArchiving), Status: metav1.ConditionTrue}}
		}
		children = append(children, obj.DeepCopyObject().(client.Object))
		objects = append(objects, obj)
	}
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithStatusSubresource(&v1.Postgres{}, &v1.PostgresBranch{}, &cnpgv1.Cluster{}).WithObjects(objects...).Build()
	r := &PostgresClusterReconciler{Config: cfg, Scheme: scheme.Scheme, Recorder: recorder}
	return r, c, pg, b, children
}
