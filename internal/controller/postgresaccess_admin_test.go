package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rcaccess "github.com/nais/pgrator/internal/resourcecreator/access"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestPostgresAdminUsesExistingDatabaseOwner(t *testing.T) {
	a, branch, postgres, cluster := accessFixture()
	a.Finalizers = []string{makePostgresAccessReconciler().Name()}
	a.Spec.AccessLevel = v1.PostgresAccessLevelAdmin
	// Existing and restored clusters need no new bootstrap privilege group.
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(branch, postgres, cluster).Build()
	r := makePostgresAccessReconciler()
	prepared, _, err := r.Prepare(t.Context(), reader, a)
	requireNoError(t, err)
	actions, _, err := r.Update(a, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	for _, action := range actions {
		role, ok := action.GetObject().(*cnpgv1.DatabaseRole)
		if !ok {
			continue
		}
		want := []string{rccnpg.OwnerRole, rccnpg.PersonalAccessRole(branch.Name)}
		if role.Spec.Name != a.Spec.Username || !reflect.DeepEqual(role.Spec.InRoles, want) {
			t.Fatalf("admin must retain personal identity and inherit the existing owner: %+v", role.Spec)
		}
		return
	}
	t.Fatal("admin access must create a personal DatabaseRole")
}

func TestPostgresAdminWaitsForDurableCleanupBeforeGrantingPrivileges(t *testing.T) {
	a, branch, postgres, cluster := accessFixture()
	a.Finalizers = nil
	a.Spec.AccessLevel = v1.PostgresAccessLevelAdmin
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(a, branch, postgres, cluster).
		WithInterceptorFuncs(interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if obj.GetName() == rcaccess.TokenSecretName(a) {
				return errors.New("injected failure after role creation")
			}
			return c.Create(ctx, obj, opts...)
		}}).Build()
	sync := synchronizer.NewSynchronizer(reader, scheme.Scheme, makePostgresAccessReconciler(), recorder)
	_, err := sync.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	if err == nil {
		t.Fatal("expected injected failure")
	}
	role := &cnpgv1.DatabaseRole{}
	requireNoError(t, reader.Get(t.Context(), client.ObjectKey{Namespace: a.Namespace, Name: rcaccess.DatabaseRoleName(a.Spec.Username, a.Spec.PostgresBranch)}, role))
	if role.Spec.Login || len(role.Spec.InRoles) != 0 || !role.Spec.DisablePassword {
		t.Fatal("partial provisioning without a durable finalizer must not grant SQL access")
	}
}

func TestPostgresAdminAdmission(t *testing.T) {
	a, _, _, _ := accessFixture()
	a.Name, a.Namespace, a.UID = "admin-admission", postgresAccessValidationNamespace, ""
	a.Spec.AccessLevel = v1.PostgresAccessLevelAdmin
	requireNoError(t, k8sClient.Create(t.Context(), a))
	t.Cleanup(func() { requireNoError(t, client.IgnoreNotFound(k8sClient.Delete(context.Background(), a))) })
	a.Spec.AccessLevel = v1.PostgresAccessLevelRead
	if err := k8sClient.Update(t.Context(), a); !apierrors.IsInvalid(err) {
		t.Fatalf("admin access must remain immutable, got %v", err)
	}
}

func TestPostgresAdminRevokedBeforeRoleResourceIsDeleted(t *testing.T) {
	for _, deletion := range []bool{false, true} {
		name := "expiry"
		if deletion {
			name = "deletion"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			a, _, _, _ := accessFixture()
			a.Name, a.Namespace, a.UID = "admin-"+name, postgresAccessValidationNamespace, ""
			a.Finalizers = []string{makePostgresAccessReconciler().Name()}
			a.Spec.AccessLevel = v1.PostgresAccessLevelAdmin
			if !deletion {
				a.Spec.ExpiresAt = metav1.NewTime(time.Now().Add(-time.Minute))
			}
			requireNoError(t, k8sClient.Create(ctx, a))
			t.Cleanup(func() {
				stored := &v1.PostgresAccess{}
				if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(a), stored); apierrors.IsNotFound(err) {
					return
				} else {
					requireNoError(t, err)
				}
				stored.Finalizers = nil
				requireNoError(t, k8sClient.Update(context.Background(), stored))
				requireNoError(t, client.IgnoreNotFound(k8sClient.Delete(context.Background(), stored)))
			})
			role, err := rcaccess.CreateDatabaseRole(scheme.Scheme, a, true)
			requireNoError(t, err)
			requireNoError(t, k8sClient.Create(ctx, role))
			t.Cleanup(func() { requireNoError(t, client.IgnoreNotFound(k8sClient.Delete(context.Background(), role))) })
			applied := true
			role.Status.Applied = &applied
			role.Status.ObservedGeneration = role.Generation
			requireNoError(t, k8sClient.Status().Update(ctx, role))
			tokenText, err := rcaccess.NewToken()
			requireNoError(t, err)
			token, err := rcaccess.CreateTokenSecret(scheme.Scheme, a, tokenText)
			requireNoError(t, err)
			requireNoError(t, k8sClient.Create(ctx, token))
			digest, err := rcaccess.TokenDigest(tokenText)
			requireNoError(t, err)
			relay, err := rcaccess.CreateRelayAccess(scheme.Scheme, a, digest)
			requireNoError(t, err)
			requireNoError(t, k8sClient.Create(ctx, relay))
			other := a.DeepCopy()
			other.Name, other.UID = "other-"+name, "other-access-uid"
			otherToken, err := rcaccess.CreateTokenSecret(scheme.Scheme, other, tokenText)
			requireNoError(t, err)
			requireNoError(t, k8sClient.Create(ctx, otherToken))
			t.Cleanup(func() { requireNoError(t, k8sClient.Delete(context.Background(), otherToken)) })
			if deletion {
				requireNoError(t, k8sClient.Delete(ctx, a))
			}
			sync := synchronizer.NewSynchronizer(k8sClient, scheme.Scheme, makePostgresAccessReconciler(), recorder)
			request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(a)}
			for range 2 {
				result, err := sync.Reconcile(ctx, request)
				requireNoError(t, err)
				if result.RequeueAfter <= 0 {
					t.Fatal("must wait for CNPG to confirm revocation")
				}
				requireNoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(role), role))
				if role.Spec.Login || !role.Spec.DisablePassword || len(role.Spec.InRoles) != 0 || role.Spec.PasswordSecret != nil {
					t.Fatalf("revocation must disable credentials and all memberships: %+v", role.Spec)
				}
				if role.Spec.Name != a.Spec.Username || role.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimRetain {
					t.Fatal("revocation must preserve SQL identity and owned objects")
				}
			}
			for _, object := range []client.Object{&corev1.Secret{ObjectMeta: token.ObjectMeta}, relay} {
				if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(object), object); !apierrors.IsNotFound(err) {
					t.Fatalf("transport credentials must be removed before CNPG confirms revocation: %v", err)
				}
			}
			requireNoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(otherToken), &corev1.Secret{}))
			// Applied=true for the previous generation was not enough. Only the
			// applied inactive configuration allows the Retain CR to be removed.
			role.Status.Applied = &applied
			role.Status.ObservedGeneration = role.Generation
			requireNoError(t, k8sClient.Status().Update(ctx, role))
			result, err := sync.Reconcile(ctx, request)
			requireNoError(t, err)
			if !result.IsZero() {
				t.Fatal("confirmed revocation must finish cleanup")
			}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(role), &cnpgv1.DatabaseRole{}); !apierrors.IsNotFound(err) {
				t.Fatalf("revoked role resource should be removed, got %v", err)
			}
		})
	}
}
