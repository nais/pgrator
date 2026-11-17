package controller

import (
	"context"
	"errors"
	"slices"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rcbinding "github.com/nais/pgrator/internal/resourcecreator/binding"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func bindingLifecycleFixture(t *testing.T) (*PostgresBindingReconciler, *v1.PostgresBinding, *cnpgv1.DatabaseRole) {
	t.Helper()
	b := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "db-workload", Namespace: "team", UID: "binding-uid", Finalizers: []string{"postgresbinding.nais.io"}}, Spec: v1.PostgresBindingSpec{Postgres: "db", Consumer: v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "workload", Type: v1.PostgresBindingWorkloadTypeApplication}}, Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialReadWrite}}}
	r := &PostgresBindingReconciler{Scheme: scheme.Scheme, Recorder: recorder}
	role, err := rcbinding.CreateDatabaseRole(scheme.Scheme, b, v1.PostgresPhysicalName(b.Spec.Postgres), v1.PostgresBindingCredentialReadWrite)
	requireNoError(t, err)
	role.UID, role.Generation = "role-uid", 3
	role.Spec.ReclaimPolicy = cnpgv1.DatabaseRoleReclaimDelete // deployed, pre-fix state
	role.Status.Applied, role.Status.ObservedGeneration = new(true), 3
	return r, b, role
}

func TestBindingActiveRolePreservesIdentityButRetainsSQLRole(t *testing.T) {
	r, b, before := bindingLifecycleFixture(t)
	roles := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	roles.Insert(before)
	actions, _, err := r.Update(b, PostgresBindingPreparedData{Branch: v1.PostgresPhysicalName(b.Spec.Postgres)}, roles)
	requireNoError(t, err)
	for _, a := range actions {
		if role, ok := a.GetObject().(*cnpgv1.DatabaseRole); ok && role.Name == before.Name {
			if role.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimRetain {
				t.Error("active workload identity can still be dropped on unbind")
			}
			if role.Spec.Name != before.Spec.Name || role.Spec.ClusterRef != before.Spec.ClusterRef || !role.Spec.Login || len(role.Spec.InRoles) != 1 || role.Spec.InRoles[0] != before.Spec.InRoles[0] || !role.IsClientCertificateEnabled() {
				t.Fatal("active database access changed during policy migration")
			}
			return
		}
	}
	t.Fatal("active role not reconciled")
}

func TestDeletingOldBindingWaitsForConfirmedRevocation(t *testing.T) {
	r, b, role := bindingLifecycleFixture(t)
	b.DeletionTimestamp = new(metav1.Now())
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	related.Insert(role)
	actions, result, err := r.Delete(b, PostgresBindingPreparedData{}, related)
	requireNoError(t, err)
	if result.IsZero() {
		t.Fatal("released binding finalizer before revoking a deployed Delete-policy role")
	}
	for _, a := range actions {
		if desired, ok := a.GetObject().(*cnpgv1.DatabaseRole); ok {
			if desired.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimRetain || desired.Spec.Login || !desired.Spec.DisablePassword || len(desired.Spec.InRoles) != 0 {
				t.Fatalf("unsafe revocation: %#v", desired.Spec)
			}
			return
		}
	}
	t.Fatal("no SQL revocation planned")
}

func TestRemovedCredentialIsRevokedBeforeItsRoleIsPruned(t *testing.T) {
	r, b, role := bindingLifecycleFixture(t)
	b.Spec.Credentials = []v1.PostgresBindingCredential{v1.PostgresBindingCredentialAdmin}
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	related.Insert(role)
	actions, result, err := r.Update(b, PostgresBindingPreparedData{Branch: v1.PostgresPhysicalName(b.Spec.Postgres)}, related)
	requireNoError(t, err)
	if result.IsZero() {
		t.Fatal("credential removal does not wait for SQL revocation")
	}
	for _, a := range actions {
		if desired, ok := a.GetObject().(*cnpgv1.DatabaseRole); ok && desired.Name == role.Name {
			if desired.Spec.Login || len(desired.Spec.InRoles) != 0 || desired.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimRetain {
				t.Fatal("removed credential still grants SQL access")
			}
			return
		}
	}
	t.Fatal("retired role would be pruned without revoking it")
}

func TestBindingRevocationWaitsForCurrentAppliedGeneration(t *testing.T) {
	r, b, role := bindingLifecycleFixture(t)
	b.DeletionTimestamp = new(metav1.Now())
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	related.Insert(role)
	actions, _, err := r.Delete(b, PostgresBindingPreparedData{}, related)
	requireNoError(t, err)
	for _, a := range actions {
		if candidate, ok := a.GetObject().(*cnpgv1.DatabaseRole); ok {
			role = candidate.DeepCopy()
			break
		}
	}
	role.Generation++
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithStatusSubresource(&cnpgv1.DatabaseRole{}).WithObjects(role).Build()
	owners := ownership.NewOwnerManager(r.Name() + "/owner")
	related.Insert(role)
	actions, result, err := r.Delete(b, PostgresBindingPreparedData{}, related)
	requireNoError(t, err)
	if result.IsZero() {
		t.Fatal("old Applied=true released the binding")
	}
	for _, a := range actions {
		requireNoError(t, a.Do(t.Context(), c, scheme.Scheme, owners))
	}
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(role), role))
	role.Status.ObservedGeneration = role.Generation
	requireNoError(t, c.Status().Update(t.Context(), role))
	related.Insert(role)
	actions, result, err = r.Delete(b, PostgresBindingPreparedData{}, related)
	requireNoError(t, err)
	if result.IsZero() {
		t.Fatal("binding must wait for the retired role CR to disappear")
	}
	for _, a := range actions {
		requireNoError(t, a.Do(t.Context(), c, scheme.Scheme, owners))
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(role), role); !apierrors.IsNotFound(err) {
		t.Fatalf("confirmed Retain CR was not removed: %v", err)
	}
	_, result, err = r.Delete(b, PostgresBindingPreparedData{}, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	if !result.IsZero() {
		t.Fatal("empty binding cleanup did not complete")
	}
}

func TestBindingFirstPassPersistsCleanupBeforeActivatingRetainRole(t *testing.T) {
	r, b, _ := bindingLifecycleFixture(t)
	pg, cluster := directBindingTarget()
	b.Spec.Postgres = pg.Name
	b.Finalizers = nil
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithStatusSubresource(&v1.PostgresBinding{}).WithObjects(b, pg, cluster).
		WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, ok := obj.(*networkingv1.NetworkPolicy); ok {
				return errors.New("injected failure after role creation")
			}
			return c.Create(ctx, obj, opts...)
		}}).Build()
	s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
	if _, err := s.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(b)}); err == nil {
		t.Fatal("expected injected failure")
	}
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(b), b))
	if !slices.Contains(b.Finalizers, r.Name()) {
		t.Fatal("an activated Retain role has no durable binding cleanup responsibility")
	}
	role := &cnpgv1.DatabaseRole{}
	requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: b.Namespace, Name: rcbinding.DatabaseRoleName(b, v1.PostgresPhysicalName(pg.Name), v1.PostgresBindingCredentialReadWrite)}, role))
	if !role.Spec.Login || role.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimRetain {
		t.Fatal("fixture did not reach active role creation")
	}
}

func TestBindingCleanupDoesNotNeedDeletedDatabaseOrCredentials(t *testing.T) {
	r, b, role := bindingLifecycleFixture(t)
	b.DeletionTimestamp = new(metav1.Now())
	role.Spec.ReclaimPolicy = cnpgv1.DatabaseRoleReclaimRetain
	role.Spec.Login, role.Spec.DisablePassword, role.Spec.InRoles = false, true, nil
	role.Spec.ClientCertificate = &cnpgv1.ClientCertificateConfiguration{Enabled: new(false)}
	role.Status.Applied = nil
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(b, role).Build()
	prepared, _, err := r.Prepare(t.Context(), c, b)
	requireNoError(t, err)
	if !prepared.MissingClusters[role.Spec.ClusterRef.Name] {
		t.Fatal("missing database was not distinguished from a failed SQL revocation")
	}
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	related.Insert(role)
	actions, _, err := r.Delete(b, prepared, related)
	requireNoError(t, err)
	for _, a := range actions {
		requireNoError(t, a.Do(t.Context(), c, scheme.Scheme, ownership.NewOwnerManager(r.Name()+"/owner")))
	}
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(role), role); !apierrors.IsNotFound(err) {
		t.Fatalf("deleted database stranded a retained binding: %v", err)
	}
}

func TestBindingRetirementDoesNotTouchForeignRolesOrDisableOwner(t *testing.T) {
	r, b, role := bindingLifecycleFixture(t)
	b.DeletionTimestamp = new(metav1.Now())
	foreign := role.DeepCopy()
	foreign.Name, foreign.UID = "another-binding-role", "foreign-role-uid"
	foreign.OwnerReferences[0].UID = "other-binding-uid"
	owner := role.DeepCopy()
	owner.Name, owner.UID, owner.Spec.Name = "legacy-admin-alias", "legacy-admin-uid", "app"
	owner.Spec.InRoles = nil
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	related.Insert(role)
	related.Insert(foreign)
	related.Insert(owner)
	actions, _, err := r.Delete(b, PostgresBindingPreparedData{}, related)
	requireNoError(t, err)
	seenOwner := false
	for _, a := range actions {
		if candidate, ok := a.GetObject().(*cnpgv1.DatabaseRole); ok {
			if candidate.Name == foreign.Name {
				t.Fatal("another binding's role was changed")
			}
			if candidate.Name == owner.Name {
				seenOwner = true
				if !candidate.Spec.Login || candidate.Spec.DisablePassword || candidate.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimRetain {
					t.Fatal("shared database owner was disabled or left destructively reclaimable")
				}
			}
		}
	}
	if !seenOwner {
		t.Fatal("legacy admin alias did not migrate to safe retention")
	}
}
