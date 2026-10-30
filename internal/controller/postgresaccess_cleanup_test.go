package controller

import (
	"reflect"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rcaccess "github.com/nais/pgrator/internal/resourcecreator/access"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestPostgresAccessCleanupPreservesReusedRole(t *testing.T) {
	for _, deletion := range []bool{false, true} {
		name := "expiry"
		if deletion {
			name = "deletion"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			r := makePostgresAccessReconciler()
			expired, _, _, _ := accessFixture()
			expired.Finalizers = []string{r.Name()}
			expired.Spec.AccessLevel = v1.PostgresAccessLevelAdmin
			current := expired.DeepCopy()
			current.Name, current.UID = "current-access", "current-access-uid"
			expired.Spec.ExpiresAt = metav1.NewTime(time.Now().Add(-time.Minute))
			// The expired access's role CR has already been removed. A new
			// access for the same person and branch reuses its stable name.
			role, err := rcaccess.CreateDatabaseRole(scheme.Scheme, current, true)
			requireNoError(t, err)
			owners := ownership.NewOwnerManager(r.Name() + "/owner")
			owners.AddOwnerAnnotation(role, current)
			var expiredChildren, currentChildren []client.Object
			for _, access := range []*v1.PostgresAccess{expired, current} {
				credentials, err := rcaccess.CreateCredentialSecret(scheme.Scheme, access, "test-password")
				requireNoError(t, err)
				tokenText, err := rcaccess.NewToken()
				requireNoError(t, err)
				token, err := rcaccess.CreateTokenSecret(scheme.Scheme, access, tokenText)
				requireNoError(t, err)
				digest, err := rcaccess.TokenDigest(tokenText)
				requireNoError(t, err)
				relay, err := rcaccess.CreateRelayAccess(scheme.Scheme, access, digest)
				requireNoError(t, err)
				netpol, err := rcaccess.CreateRelayNetworkPolicy(scheme.Scheme, access)
				requireNoError(t, err)
				children := []client.Object{credentials, token, relay, netpol}
				for _, child := range children {
					owners.AddOwnerAnnotation(child, access)
				}
				if access == expired {
					expiredChildren = children
				} else {
					currentChildren = children
				}
			}
			c := fake.NewClientBuilder().WithScheme(scheme.Scheme).
				WithStatusSubresource(&v1.PostgresAccess{}).WithObjects(expired, current, role).
				WithObjects(expiredChildren...).WithObjects(currentChildren...).Build()
			requireNoError(t, c.Get(ctx, client.ObjectKeyFromObject(role), role))
			if deletion {
				requireNoError(t, c.Delete(ctx, expired))
			}
			sync := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
			result, err := sync.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(expired)})
			requireNoError(t, err)
			if !result.IsZero() {
				t.Fatal("an unrelated role must not block cleanup")
			}
			for _, child := range expiredChildren {
				if err := c.Get(ctx, client.ObjectKeyFromObject(child), child); !apierrors.IsNotFound(err) {
					t.Fatalf("expired access's %T must be removed: %v", child, err)
				}
			}
			for _, child := range currentChildren {
				requireNoError(t, c.Get(ctx, client.ObjectKeyFromObject(child), child))
			}
			storedRole := &cnpgv1.DatabaseRole{}
			requireNoError(t, c.Get(ctx, client.ObjectKeyFromObject(role), storedRole))
			if !reflect.DeepEqual(storedRole.Spec, role.Spec) || !metav1.IsControlledBy(storedRole, current) {
				t.Fatal("cleanup must preserve the newer access's role and privileges")
			}
			requireNoError(t, c.Get(ctx, client.ObjectKeyFromObject(current), &v1.PostgresAccess{}))
			stored := &v1.PostgresAccess{}
			err = c.Get(ctx, client.ObjectKeyFromObject(expired), stored)
			if deletion {
				if !apierrors.IsNotFound(err) {
					t.Fatalf("expired access deletion must complete, got %v", err)
				}
			} else {
				requireNoError(t, err)
				if findReadyCondition(stored.Status.Conditions).Status != metav1.ConditionFalse {
					t.Fatal("expired access must not be ready")
				}
			}
		})
	}
}

func TestPostgresAccessCleanupRejectsOwnedRoleWithDifferentSQLIdentity(t *testing.T) {
	access, _, _, _ := accessFixture()
	role, err := rcaccess.CreateDatabaseRole(scheme.Scheme, access, true)
	requireNoError(t, err)
	role.Spec.Name = "someone-else@example.com"
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	related.Insert(role)
	actions, _, err := makePostgresAccessReconciler().Delete(access, PostgresAccessPreparedData{}, related)
	if err == nil || len(actions) != 0 {
		t.Fatal("an owned role with a different SQL identity must fail validation without modifying resources")
	}
}
