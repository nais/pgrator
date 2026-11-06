package controller

import (
	"reflect"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rcaccess "github.com/nais/pgrator/internal/resourcecreator/access"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func adoptAccessFixture(t *testing.T, pg *v1.Postgres, cluster *cnpgv1.Cluster) {
	t.Helper()
	pg.UID = "postgres-uid"
	pg.Status = &v1.PostgresStatus{ClusterName: cluster.Name}
	requireNoError(t, controllerutil.SetControllerReference(pg, cluster, scheme.Scheme))
}

func TestPostgresAccessRelationshipWatchesMapBothReferences(t *testing.T) {
	legacy, branch, pg, cluster := accessFixture()
	legacy.Name = "legacy-access"
	direct := legacy.DeepCopy()
	direct.Name = "direct-access"
	direct.Spec.Postgres, direct.Spec.PostgresBranch = pg.Name, ""
	otherTeam := direct.DeepCopy()
	otherTeam.Namespace = "foreign-team"
	r := makePostgresAccessReconciler()
	builder := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(legacy, direct, otherTeam)
	for _, index := range r.Indexes() {
		builder.WithIndex(index.Object, index.Field, index.ExtractValue)
	}
	reader := builder.Build()
	for _, watch := range r.RelationshipWatches() {
		var object client.Object
		switch watch.Type.(type) {
		case *v1.Postgres:
			object = pg
		case *v1.PostgresBranch:
			object = branch
		case *cnpgv1.Cluster:
			object = cluster
		default:
			t.Fatalf("unexpected relationship watch: %T", watch.Type)
		}
		requests, err := watch.Map(t.Context(), reader, object)
		requireNoError(t, err)
		if len(requests) != 2 {
			t.Fatalf("%T mapped %d accesses, want both references", object, len(requests))
		}
		for _, request := range requests {
			if request.Namespace != "team" {
				t.Error("watch crossed the namespace boundary")
			}
		}
		if _, ok := object.(*v1.Postgres); ok {
			changed := pg.DeepCopy()
			changed.Status = &v1.PostgresStatus{ClusterName: cluster.Name}
			if !watch.Predicate.Update(event.UpdateEvent{ObjectOld: pg, ObjectNew: changed}) {
				t.Error("direct ownership status did not trigger access reconcile")
			}
		}
	}
}

func TestPostgresAccessResolvesBothReferenceShapes(t *testing.T) {
	for _, tc := range []struct {
		name            string
		directReference bool
		adopted         bool
		keepBranch      bool
	}{
		{name: "legacy reference before adoption", keepBranch: true},
		{name: "direct reference before adoption", directReference: true, keepBranch: true},
		{name: "legacy reference after detach", adopted: true},
		{name: "direct reference after detach", directReference: true, adopted: true},
		{name: "legacy reference during adoption", adopted: true, keepBranch: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access, branch, pg, cluster := accessFixture()
			if tc.directReference {
				access.Spec.Postgres, access.Spec.PostgresBranch = pg.Name, ""
			}
			if tc.adopted {
				adoptAccessFixture(t, pg, cluster)
			}
			objects := []client.Object{pg, cluster}
			if tc.keepBranch {
				objects = append(objects, branch)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(objects...).Build()
			prep, _, err := makePostgresAccessReconciler().Prepare(t.Context(), reader, access)
			requireNoError(t, err)
			if prep.Cluster.Name != "pg-db-main-78dbba05" || access.Spec.PhysicalName() != branch.Name {
				t.Fatalf("changed physical target: cluster=%q identity=%q", prep.Cluster.Name, access.Spec.PhysicalName())
			}
		})
	}
}

func TestPostgresAccessRejectsAmbiguousOrUnavailableTargets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*v1.PostgresAccess, *v1.Postgres, *cnpgv1.Cluster)
	}{
		{name: "both references", change: func(a *v1.PostgresAccess, p *v1.Postgres, _ *cnpgv1.Cluster) { a.Spec.Postgres = p.Name }},
		{name: "no reference", change: func(a *v1.PostgresAccess, _ *v1.Postgres, _ *cnpgv1.Cluster) { a.Spec.PostgresBranch = "" }},
		{name: "missing non-main", change: func(a *v1.PostgresAccess, _ *v1.Postgres, _ *cnpgv1.Cluster) {
			a.Spec.PostgresBranch = v1.PostgresBranchObjectName("db", "restore")
		}},
		{name: "unadopted missing main", change: func(_ *v1.PostgresAccess, p *v1.Postgres, _ *cnpgv1.Cluster) { p.Status = nil }},
		{name: "another namespace", change: func(a *v1.PostgresAccess, _ *v1.Postgres, _ *cnpgv1.Cluster) { a.Namespace = "other" }},
		{name: "wrong cluster reference", change: func(_ *v1.PostgresAccess, p *v1.Postgres, _ *cnpgv1.Cluster) { p.Status.ClusterName = "pg-other" }},
		{name: "wrong controller UID", change: func(_ *v1.PostgresAccess, _ *v1.Postgres, c *cnpgv1.Cluster) { c.OwnerReferences[0].UID = "other-uid" }},
		{name: "wrong controller kind", change: func(_ *v1.PostgresAccess, _ *v1.Postgres, c *cnpgv1.Cluster) {
			c.OwnerReferences[0].Kind = "PostgresBranch"
		}},
		{name: "deleting Postgres", change: func(_ *v1.PostgresAccess, p *v1.Postgres, _ *cnpgv1.Cluster) {
			p.DeletionTimestamp = new(metav1.Now())
			p.Finalizers = []string{"postgres.nais.io"}
		}},
		{name: "deleting cluster", change: func(_ *v1.PostgresAccess, _ *v1.Postgres, c *cnpgv1.Cluster) {
			c.DeletionTimestamp = new(metav1.Now())
			c.Finalizers = []string{"test"}
		}},
		{name: "cluster not ready", change: func(_ *v1.PostgresAccess, _ *v1.Postgres, c *cnpgv1.Cluster) { c.Status.Conditions = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access, _, pg, cluster := accessFixture()
			adoptAccessFixture(t, pg, cluster)
			tc.change(access, pg, cluster)
			reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pg, cluster).Build()
			if _, _, err := makePostgresAccessReconciler().Prepare(t.Context(), reader, access); err == nil {
				t.Fatal("unsafe access target was accepted")
			}
		})
	}
}

func TestExistingPostgresAccessPreservesProofAcrossDetach(t *testing.T) {
	access, branch, pg, cluster := accessFixture()
	password, err := rcaccess.CreateCredentialSecret(scheme.Scheme, access, "existing-password")
	requireNoError(t, err)
	password.StringData = nil
	password.Data = map[string][]byte{corev1.BasicAuthPasswordKey: []byte("existing-password")}
	tokenText, err := rcaccess.NewToken()
	requireNoError(t, err)
	token, err := rcaccess.CreateTokenSecret(scheme.Scheme, access, tokenText)
	requireNoError(t, err)
	digest, err := rcaccess.TokenDigest(tokenText)
	requireNoError(t, err)
	relay, err := rcaccess.CreateRelayAccess(scheme.Scheme, access, digest)
	requireNoError(t, err)
	r := makePostgresAccessReconciler()
	beforeClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(branch, pg, cluster, password, token, relay).Build()
	before, _, err := r.Prepare(t.Context(), beforeClient, access)
	requireNoError(t, err)
	beforeActions, _, err := r.Update(access, before, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	adoptAccessFixture(t, pg, cluster)
	afterClient := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pg, cluster, password, token, relay).Build()
	after, _, err := r.Prepare(t.Context(), afterClient, access)
	requireNoError(t, err)
	afterActions, _, err := r.Update(access, after, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	if before.Password != after.Password || before.Token != after.Token {
		t.Fatal("password or relay token rotated during detach")
	}
	for i := range beforeActions {
		if !reflect.DeepEqual(beforeActions[i].GetObject(), afterActions[i].GetObject()) {
			t.Errorf("action %d changed access material during detach", i)
		}
	}
}

func TestPostgresAccessReferenceSchemaAndImmutability(t *testing.T) {
	for _, tc := range []struct {
		name     string
		postgres string
		branch   string
		valid    bool
	}{
		{name: "legacy", branch: v1.PostgresPhysicalName("db"), valid: true},
		{name: "direct", postgres: "db", valid: true},
		{name: "neither"},
		{name: "both", postgres: "db", branch: v1.PostgresPhysicalName("db")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access, _, _, _ := accessFixture()
			access.Name, access.Namespace, access.UID = "access-reference-"+tc.name, postgresAccessValidationNamespace, ""
			access.Spec.Postgres, access.Spec.PostgresBranch = tc.postgres, tc.branch
			err := k8sClient.Create(t.Context(), access)
			if !tc.valid {
				if !apierrors.IsInvalid(err) {
					t.Fatalf("invalid reference returned %v", err)
				}
				return
			}
			requireNoError(t, err)
			t.Cleanup(func() { _ = k8sClient.Delete(ctx, access) })
			stored := &v1.PostgresAccess{}
			requireNoError(t, k8sClient.Get(t.Context(), client.ObjectKeyFromObject(access), stored))
			if stored.Spec.Postgres != tc.postgres || stored.Spec.PostgresBranch != tc.branch {
				t.Fatal("reference did not round-trip")
			}
			changed := stored.DeepCopy()
			if tc.postgres != "" {
				changed.Spec.Postgres = "other-db"
			} else {
				changed.Spec.PostgresBranch = v1.PostgresPhysicalName("other-db")
			}
			if err := k8sClient.Update(t.Context(), changed); !apierrors.IsInvalid(err) {
				t.Fatalf("reference value was mutable: %v", err)
			}
			changed = stored.DeepCopy()
			if tc.postgres != "" {
				changed.Spec.Postgres, changed.Spec.PostgresBranch = "", v1.PostgresPhysicalName("db")
			} else {
				changed.Spec.Postgres, changed.Spec.PostgresBranch = "db", ""
			}
			if err := k8sClient.Update(t.Context(), changed); !apierrors.IsInvalid(err) {
				t.Fatalf("reference shape was mutable: %v", err)
			}
		})
	}
}

func TestDirectPostgresAccessCanBeRevokedWithoutItsDatabase(t *testing.T) {
	access, _, pg, _ := accessFixture()
	access.Spec.Postgres, access.Spec.PostgresBranch = pg.Name, ""
	access.Spec.ExpiresAt = metav1.NewTime(time.Now().Add(-time.Minute))
	role, err := rcaccess.CreateDatabaseRole(scheme.Scheme, access, true)
	requireNoError(t, err)
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	related.Insert(role)
	r := makePostgresAccessReconciler()
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	prep, _, err := r.Prepare(t.Context(), reader, access)
	requireNoError(t, err)
	if !prep.Expired {
		t.Fatal("expired direct access still depended on database lookup")
	}
	actions, result, err := r.Update(access, prep, related)
	requireNoError(t, err)
	if len(actions) != 1 || result.RequeueAfter == 0 {
		t.Fatal("revocation did not wait for the disabled role to be applied")
	}
	disabled := actions[0].GetObject().(*cnpgv1.DatabaseRole)
	if disabled.Name != role.Name || disabled.Spec.ClusterRef != role.Spec.ClusterRef || disabled.Spec.Name != role.Spec.Name {
		t.Error("revocation changed the retained role identity")
	}
	if disabled.Spec.Login || !disabled.Spec.DisablePassword || disabled.Spec.PasswordSecret != nil || len(disabled.Spec.InRoles) != 0 {
		t.Fatal("revocation retained credentials or privilege memberships")
	}
}
