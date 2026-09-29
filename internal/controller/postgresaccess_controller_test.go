package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rcaccess "github.com/nais/pgrator/internal/resourcecreator/access"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func makePostgresAccessReconciler() *PostgresAccessReconciler {
	return &PostgresAccessReconciler{Recorder: recorder, Scheme: scheme.Scheme}
}

func accessFixture() (*v1.PostgresAccess, *v1.PostgresBranch, *v1.Postgres, *cnpgv1.Cluster) {
	a := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team", UID: "access-uid"}, Spec: v1.PostgresAccessSpec{Username: "frode@nav.no", PostgresBranch: "orders", AccessLevel: v1.PostgresAccessLevelRead, ExpiresAt: metav1.NewTime(time.Now().Add(30 * time.Minute))}}
	i := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "db"}}
	p := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "team"}}
	c := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg-orders", Namespace: "team"}, Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{Type: string(cnpgv1.ConditionInitialized), Status: metav1.ConditionTrue}, {Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionTrue}}}}
	return a, i, p, c
}

func TestPostgresAccessPublishesSeparateRelayProof(t *testing.T) {
	a, i, p, c := accessFixture()
	c.Spec.Certificates = &cnpgv1.CertificatesConfiguration{ServerCASecret: "custom-server-ca"}
	r := makePostgresAccessReconciler()
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(i, p, c).Build()
	prepared, _, err := r.Prepare(context.Background(), reader, a)
	requireNoError(t, err)
	actions, _, err := r.Update(a, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	if len(actions) != 5 {
		t.Fatalf("actions = %d, want 5", len(actions))
	}
	token, ok := actions[2].GetObject().(*corev1.Secret)
	if !ok {
		t.Fatalf("token action = %T", actions[2].GetObject())
	}
	digest, err := rcaccess.TokenDigest(string(token.Data[rcaccess.TokenKey]))
	requireNoError(t, err)
	if token.Name == rcaccess.CredentialSecretName(a) {
		t.Error("relay token must not be the CNPG credential")
	}
	relay := actions[3].GetObject()
	spec := relay.(*unstructured.Unstructured).Object["spec"].(map[string]any)
	if spec["tokenSHA256"] != digest || spec["target"].(map[string]any)["serviceName"] != "pg-orders-rw" {
		t.Errorf("mapping = %v", spec)
	}
	if a.Status.TokenSecret != "" || a.Status.RelayAccess != relay.GetName() {
		t.Errorf("unpersisted token must not be advertised as provisioned: %+v", a.Status)
	}
	if a.Status.ServerName != "pg-orders-rw.team.svc.cluster.local" || a.Status.ServerCASecret != "custom-server-ca" {
		t.Errorf("TLS connection metadata = %q, %q", a.Status.ServerName, a.Status.ServerCASecret)
	}
	if findReadyCondition(a.Status.Conditions).Status != metav1.ConditionFalse {
		t.Error("new access must not be Ready")
	}
}

func TestPostgresAccessPublishesDefaultServerCA(t *testing.T) {
	a, i, p, c := accessFixture()
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(i, p, c).Build()
	prepared, _, err := makePostgresAccessReconciler().Prepare(context.Background(), reader, a)
	requireNoError(t, err)
	_, _, err = makePostgresAccessReconciler().Update(a, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	if a.Status.ServerCASecret != "pg-orders-ca" {
		t.Errorf("server CA Secret = %q, want CNPG default", a.Status.ServerCASecret)
	}
}

func TestPostgresAccessRecoversFromFailedFirstTokenWrite(t *testing.T) {
	a, i, p, c := accessFixture()
	password, err := rcaccess.CreateCredentialSecret(scheme.Scheme, a, "password")
	requireNoError(t, err)
	password.Data = map[string][]byte{corev1.BasicAuthPasswordKey: []byte("password")}
	password.StringData = nil // fake client does not convert StringData to Data
	failed := false
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(a, i, p, c, password).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if obj.GetName() == rcaccess.TokenSecretName(a) && !failed {
					failed = true
					return errors.New("injected first token write failure")
				}
				return c.Create(ctx, obj, opts...)
			},
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if secret, ok := obj.(*corev1.Secret); ok && secret.Name == rcaccess.CredentialSecretName(a) {
					// Fake client does not perform the API server's StringData conversion.
					secret.Data = map[string][]byte{corev1.BasicAuthPasswordKey: []byte(secret.StringData[corev1.BasicAuthPasswordKey])}
					secret.StringData = nil
				}
				return c.Update(ctx, obj, opts...)
			},
		}).Build()
	reconcileOnce := func() error {
		sync := synchronizer.NewSynchronizer(reader, scheme.Scheme, makePostgresAccessReconciler(), recorder)
		_, err := sync.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(a)})
		return err
	}
	if err := reconcileOnce(); err == nil || !failed {
		t.Fatalf("first reconcile: err = %v, injected failure = %v", err, failed)
	}
	stored := &v1.PostgresAccess{}
	requireNoError(t, reader.Get(context.Background(), client.ObjectKeyFromObject(a), stored))
	if stored.Status != nil && stored.Status.TokenSecret != "" {
		t.Fatal("unpersisted token must not be marked as provisioned")
	}
	requireNoError(t, reconcileOnce()) // new synchronizer: recover from persisted state
	storedToken := &corev1.Secret{}
	requireNoError(t, reader.Get(context.Background(), client.ObjectKey{Namespace: a.Namespace, Name: rcaccess.TokenSecretName(a)}, storedToken))
	first := string(storedToken.Data[rcaccess.TokenKey])
	requireNoError(t, reconcileOnce())
	requireNoError(t, reader.Get(context.Background(), client.ObjectKeyFromObject(storedToken), storedToken))
	if first == "" || string(storedToken.Data[rcaccess.TokenKey]) != first {
		t.Fatal("persisted token rotated after restart")
	}
}

func TestPostgresAccessPreservesTokenAcrossReconcile(t *testing.T) {
	a, i, p, c := accessFixture()
	r := makePostgresAccessReconciler()
	tokenText, err := rcaccess.NewToken()
	requireNoError(t, err)
	token, err := rcaccess.CreateTokenSecret(scheme.Scheme, a, tokenText)
	requireNoError(t, err)
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(i, p, c, token).Build()
	for range 2 {
		prep, _, err := r.Prepare(context.Background(), reader, a)
		requireNoError(t, err)
		if prep.Token != tokenText || !prep.TokenPersisted {
			t.Fatal("stored token changed")
		}
		actions, _, err := r.Update(a, prep, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
		requireNoError(t, err)
		if !reflect.DeepEqual(actions[2].GetObject().(*corev1.Secret).Data, token.Data) {
			t.Fatal("token rotated")
		}
	}
}

func TestPostgresAccessReadyRequiresAppliedRoleAndPersistedMapping(t *testing.T) {
	a, i, p, c := accessFixture()
	r := makePostgresAccessReconciler()
	tokenText, err := rcaccess.NewToken()
	requireNoError(t, err)
	digest, err := rcaccess.TokenDigest(tokenText)
	requireNoError(t, err)
	token, err := rcaccess.CreateTokenSecret(scheme.Scheme, a, tokenText)
	requireNoError(t, err)
	relay, err := rcaccess.CreateRelayAccess(scheme.Scheme, a, digest)
	requireNoError(t, err)
	role, err := rcaccess.CreateDatabaseRole(scheme.Scheme, a, true)
	requireNoError(t, err)
	applied := true
	role.Generation = 2
	role.Status.Applied = &applied
	role.Status.ObservedGeneration = 1
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(i, p, c, token, relay, role).Build()
	prep, _, err := r.Prepare(context.Background(), reader, a)
	requireNoError(t, err)
	_, _, err = r.Update(a, prep, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	if findReadyCondition(a.Status.Conditions).Status != metav1.ConditionFalse {
		t.Error("stale role marked Ready")
	}
	readyRole := role.DeepCopy()
	readyRole.Status.ObservedGeneration = 2
	reader = fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(i, p, c, token, relay, readyRole).Build()
	prep, _, err = r.Prepare(context.Background(), reader, a)
	requireNoError(t, err)
	_, _, err = r.Update(a, prep, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	if findReadyCondition(a.Status.Conditions).Status != metav1.ConditionTrue {
		t.Error("persisted mapping/token and applied role should be Ready")
	}
}

func TestPostgresAccessRefusesMissingTokenForExistingMapping(t *testing.T) {
	a, i, p, c := accessFixture()
	r := makePostgresAccessReconciler()
	token, err := rcaccess.NewToken()
	requireNoError(t, err)
	digest, err := rcaccess.TokenDigest(token)
	requireNoError(t, err)
	relay, err := rcaccess.CreateRelayAccess(scheme.Scheme, a, digest)
	requireNoError(t, err)
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(i, p, c, relay).Build()
	_, _, err = r.Prepare(context.Background(), reader, a)
	if err == nil {
		t.Fatal("missing token must not be replaced")
	}
}

func TestPostgresAccessRevokesReadinessOnTokenMutationOrDeletion(t *testing.T) {
	a, i, p, c := accessFixture()
	r := makePostgresAccessReconciler()
	original, err := rcaccess.NewToken()
	requireNoError(t, err)
	digest, err := rcaccess.TokenDigest(original)
	requireNoError(t, err)
	relay, err := rcaccess.CreateRelayAccess(scheme.Scheme, a, digest)
	requireNoError(t, err)
	stored, err := rcaccess.CreateTokenSecret(scheme.Scheme, a, original)
	requireNoError(t, err)
	replacement, err := rcaccess.NewToken()
	requireNoError(t, err)

	if !r.OwnedTypes()[0].AdditionalPredicate.Update(event.UpdateEvent{
		ObjectOld: stored,
		ObjectNew: func() *corev1.Secret {
			modified := stored.DeepCopy()
			modified.Data[rcaccess.TokenKey] = []byte(replacement)
			return modified
		}(),
	}) {
		t.Fatal("data-only token update must enqueue PostgresAccess")
	}
	for _, tc := range []struct {
		name   string
		secret *corev1.Secret
	}{
		{name: "changed valid token", secret: func() *corev1.Secret {
			modified := stored.DeepCopy()
			modified.Data[rcaccess.TokenKey] = []byte(replacement)
			return modified
		}()},
		{name: "deleted token"},
		{name: "terminating token", secret: func() *corev1.Secret {
			modified := stored.DeepCopy()
			now := metav1.Now()
			modified.DeletionTimestamp = &now
			modified.Finalizers = []string{"test.nais.io/cleanup"}
			return modified
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			access := a.DeepCopy()
			access.GetStatus().SetCondition(metav1.Condition{Type: postgresAccessReadyCondition, Status: metav1.ConditionTrue, Reason: "Ready"})
			objects := []client.Object{i, p, c, relay}
			if tc.secret != nil {
				objects = append(objects, tc.secret)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(objects...).Build()
			prep, _, err := r.Prepare(context.Background(), reader, access)
			if err == nil {
				_, _, err = r.Update(access, prep, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
			}
			if err == nil || findReadyCondition(access.Status.Conditions).Status != metav1.ConditionFalse {
				t.Fatalf("invalid token state must reject access and clear Ready, error = %v", err)
			}
		})
	}
}

func TestPostgresAccessRejectsTerminatingRelay(t *testing.T) {
	a, i, p, c := accessFixture()
	r := makePostgresAccessReconciler()
	token, err := rcaccess.NewToken()
	requireNoError(t, err)
	digest, err := rcaccess.TokenDigest(token)
	requireNoError(t, err)
	relay, err := rcaccess.CreateRelayAccess(scheme.Scheme, a, digest)
	requireNoError(t, err)
	terminating := relay.DeepCopy()
	now := metav1.Now()
	terminating.SetDeletionTimestamp(&now)
	terminating.SetFinalizers([]string{"test.nais.io/cleanup"})
	if !r.OwnedTypes()[2].AdditionalPredicate.Update(event.UpdateEvent{ObjectOld: relay, ObjectNew: terminating}) {
		t.Fatal("relay deletion transition must enqueue PostgresAccess")
	}
	access := a.DeepCopy()
	access.GetStatus().SetCondition(metav1.Condition{Type: postgresAccessReadyCondition, Status: metav1.ConditionTrue, Reason: "Ready"})
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(i, p, c, terminating).Build()
	_, _, err = r.Prepare(context.Background(), reader, access)
	if err == nil || findReadyCondition(access.Status.Conditions).Status != metav1.ConditionFalse {
		t.Fatalf("terminating relay cannot be Ready, error = %v", err)
	}
}

func TestPostgresAccessRefusesImmutableMappingMismatch(t *testing.T) {
	a, i, p, c := accessFixture()
	r := makePostgresAccessReconciler()
	tokenText, err := rcaccess.NewToken()
	requireNoError(t, err)
	digest, err := rcaccess.TokenDigest(tokenText)
	requireNoError(t, err)
	token, err := rcaccess.CreateTokenSecret(scheme.Scheme, a, tokenText)
	requireNoError(t, err)
	relay, err := rcaccess.CreateRelayAccess(scheme.Scheme, a, digest)
	requireNoError(t, err)
	mapping := relay.Object["spec"].(map[string]any)
	mapping["target"].(map[string]any)["serviceName"] = "pg-other-rw"
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(i, p, c, token, relay).Build()
	prep, _, err := r.Prepare(context.Background(), reader, a)
	requireNoError(t, err)
	actions, _, err := r.Update(a, prep, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	if err == nil || len(actions) != 0 {
		t.Fatal("immutable mapping mismatch must fail without writes")
	}
}

func TestPostgresAccessMappingSurvivesAPIServerRoundTrip(t *testing.T) {
	ctx := context.Background()
	a, i, _, _ := accessFixture()
	a.Namespace = "default"
	i.Namespace = "default"
	requireNoError(t, k8sClient.Create(ctx, a))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, a) })
	token, err := rcaccess.NewToken()
	requireNoError(t, err)
	digest, err := rcaccess.TokenDigest(token)
	requireNoError(t, err)
	mapping, err := rcaccess.CreateRelayAccess(scheme.Scheme, a, digest)
	requireNoError(t, err)
	requireNoError(t, k8sClient.Create(ctx, mapping))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, mapping) })
	read := relayAccessObject()
	requireNoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(mapping), read))
	secret, err := rcaccess.CreateTokenSecret(scheme.Scheme, a, token)
	requireNoError(t, err)
	prepared := PostgresAccessPreparedData{Token: token, TokenPersisted: true, RelayAccess: read, Cluster: &cnpgv1.Cluster{}}
	// A persisted immutable mapping must be claimed, not updated or recreated.
	actions, _, err := makePostgresAccessReconciler().Update(a, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	if len(actions) != 5 || actions[3].GetObject().GetName() != mapping.GetName() || secret.Name != rcaccess.TokenSecretName(a) {
		t.Fatalf("reconcile changed persisted mapping or token references: %d actions", len(actions))
	}
	// The existing mapping's immutable spec survives the actual action against
	// the API server; only pgrator's ownership annotation may be claimed.
	requireNoError(t, actions[3].Do(ctx, k8sClient, scheme.Scheme, ownership.NewOwnerManager("postgresaccess.nais.io/owner")))
	read = relayAccessObject()
	requireNoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(mapping), read))
	if !reflect.DeepEqual(read.Object["spec"], mapping.Object["spec"]) {
		t.Errorf("immutable mapping changed after claim: %v", read.Object["spec"])
	}
}

func TestPostgresAccessSynchronizerListsOwnedRelayMapping(t *testing.T) {
	a, i, p, c := accessFixture()
	a.Spec.ExpiresAt = metav1.NewTime(time.Now().Add(3 * time.Second))
	role, err := rcaccess.CreateDatabaseRole(scheme.Scheme, a, true)
	requireNoError(t, err)
	role.Generation = 1
	applied := true
	role.Status.Applied = &applied
	role.Status.ObservedGeneration = 1
	tokenText, err := rcaccess.NewToken()
	requireNoError(t, err)
	token, err := rcaccess.CreateTokenSecret(scheme.Scheme, a, tokenText)
	requireNoError(t, err)
	digest, err := rcaccess.TokenDigest(tokenText)
	requireNoError(t, err)
	relay, err := rcaccess.CreateRelayAccess(scheme.Scheme, a, digest)
	requireNoError(t, err)
	password, err := rcaccess.CreateCredentialSecret(scheme.Scheme, a, "test-password")
	requireNoError(t, err)
	password.Data = map[string][]byte{corev1.BasicAuthPasswordKey: []byte("test-password")}
	password.StringData = nil // fake client does not perform Secret StringData conversion
	// The generic synchronizer must list an UnstructuredList and resolve its
	// items' GVK for ownership and cleanup on a real reconcile.
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(a, i, p, c, role, relay, token, password).Build()
	sync := synchronizer.NewSynchronizer(reader, scheme.Scheme, makePostgresAccessReconciler(), recorder)
	result, err := sync.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	requireNoError(t, err)
	if result.RequeueAfter <= 0 || result.RequeueAfter > 3*time.Second {
		t.Fatalf("expiry requeue = %s, want remaining access lifetime", result.RequeueAfter)
	}

	// Simulate the scheduled requeue without changing the immutable spec.
	// Expiry prunes the unstructured mapping alongside the other owned artifacts.
	time.Sleep(result.RequeueAfter + 100*time.Millisecond)
	_, err = sync.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(a)})
	requireNoError(t, err)
	err = reader.Get(context.Background(), client.ObjectKeyFromObject(relay), relayAccessObject())
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expired mapping still exists: %v", err)
	}
	err = reader.Get(context.Background(), client.ObjectKeyFromObject(token), &corev1.Secret{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expired token still exists: %v", err)
	}
	err = reader.Get(context.Background(), client.ObjectKey{Namespace: a.Namespace, Name: rcaccess.RelayNetworkPolicyName(a)}, &networkingv1.NetworkPolicy{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expired ingress policy still exists: %v", err)
	}
}

func TestPostgresAccessExpiryDoesNotRemainReady(t *testing.T) {
	a, _, _, _ := accessFixture()
	a.Spec.ExpiresAt = metav1.NewTime(time.Now().Add(-time.Minute))
	a.Status = &v1.PostgresAccessStatus{ServerName: "pg-orders-rw.team.svc.cluster.local", ServerCASecret: "pg-orders-ca"}
	r := makePostgresAccessReconciler()
	prep, _, err := r.Prepare(context.Background(), fake.NewClientBuilder().WithScheme(scheme.Scheme).Build(), a)
	requireNoError(t, err)
	actions, _, err := r.Update(a, prep, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	if len(actions) != 0 || findReadyCondition(a.Status.Conditions).Status != metav1.ConditionFalse || a.Status.ServerName != "" || a.Status.ServerCASecret != "" {
		t.Fatal("expired access must be disabled and its TLS metadata cleared")
	}
}

func TestPostgresAccessDeletionLetsGarbageCollectionRemoveOwnedResources(t *testing.T) {
	actions, result, err := makePostgresAccessReconciler().Delete(&v1.PostgresAccess{}, PostgresAccessPreparedData{}, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	if len(actions) != 0 || !result.IsZero() {
		t.Error("deletion must leave owned resources to garbage collection")
	}
}

func findReadyCondition(conditions []metav1.Condition) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == postgresAccessReadyCondition {
			return &conditions[i]
		}
	}
	return nil
}
