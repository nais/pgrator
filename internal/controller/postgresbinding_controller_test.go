package controller

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/initscheme"
	rcbinding "github.com/nais/pgrator/internal/resourcecreator/binding"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestPostgresBindingSnapshot(t *testing.T) {
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "reporter", Namespace: "team"}, Spec: v1.PostgresBindingSpec{
		Postgres: "orders", SecretName: "reporter-orders-connection",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "reporter", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	physicalName := v1.PostgresPhysicalName("orders")
	postgres, cluster := directBindingTarget()
	cluster.Spec.Certificates = &cnpgv1.CertificatesConfiguration{ClientCASecret: "orders-ca"}
	roleName := rcbinding.DatabaseRoleName(binding, physicalName, v1.PostgresBindingCredentialRead)
	certSecret := (&cnpgv1.DatabaseRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}}).GetClientCertSecretName()
	caCert, caKey, caPEM := generateTestCA(t)
	certPEM, keyPEM := generateTestClientCert(t, caCert, caKey, binding.RoleName(v1.PostgresBindingCredentialRead))

	t.Run("publishes a complete snapshot", func(t *testing.T) {
		reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(postgres, cluster,
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "orders-ca", Namespace: "team"}, Data: map[string][]byte{"ca.crt": caPEM}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: certSecret, Namespace: "team"}, Data: map[string][]byte{"tls.crt": certPEM, "tls.key": keyPEM}},
		).Build()
		r := &PostgresBindingReconciler{Recorder: recorder, Scheme: scheme.Scheme}
		prepared, _, err := r.Prepare(context.Background(), reader, binding)
		requireNoError(t, err)
		requireNotNil(t, prepared.Snapshot, "complete material should produce a snapshot")
		actions, _, err := r.Update(binding, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
		requireNoError(t, err)
		for _, action := range actions {
			secret, ok := action.GetObject().(*corev1.Secret)
			if !ok || secret.Name != binding.Spec.SecretName {
				continue
			}
			requireEqual(t, string(secret.Data["ca.crt"]), string(caPEM), "CA certificate")
			requireEqual(t, string(secret.Data["read.tls.crt"]), string(certPEM), "client certificate")
			requireEqual(t, string(secret.Data["read.tls.key"]), string(keyPEM), "client key")
			requireEqual(t, secret.StringData["READ_PGHOST"], rccnpg.PoolerNameFor(physicalName)+".team", "active instance host")
			return
		}
		t.Fatal("missing stable binding Secret action")
	})

	t.Run("does not replace a stable secret while source material is incomplete", func(t *testing.T) {
		reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(postgres, cluster,
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "orders-ca", Namespace: "team"}, Data: map[string][]byte{"ca.crt": caPEM}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: certSecret, Namespace: "team"}, Data: map[string][]byte{"tls.crt": certPEM}},
		).Build()
		r := &PostgresBindingReconciler{Recorder: recorder, Scheme: scheme.Scheme}
		prepared, _, err := r.Prepare(context.Background(), reader, binding)
		requireNoError(t, err)
		requireNil(t, prepared.Snapshot, "missing certificate must not produce a snapshot")
		actions, _, err := r.Update(binding, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
		requireNoError(t, err)
		for _, action := range actions {
			if action.GetObject().GetName() == binding.Spec.SecretName {
				if _, ok := action.GetObject().(*corev1.Secret); ok {
					t.Fatal("incomplete material must not update the stable binding Secret")
				}
			}
		}
	})
}

func generateTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	requireNoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	requireNoError(t, err)
	cert, err := x509.ParseCertificate(der)
	requireNoError(t, err)
	return cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func generateTestClientCert(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	requireNoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	requireNoError(t, err)
	keyDER, err := x509.MarshalECPrivateKey(key)
	requireNoError(t, err)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func TestPostgresBindingPostgresWatchPredicate(t *testing.T) {
	pred := (&PostgresBindingReconciler{}).RelationshipWatches()[0].Predicate

	t.Run("create fires", func(t *testing.T) {
		if !pred.Create(event.CreateEvent{}) {
			t.Error("Create should fire")
		}
	})
	t.Run("delete fires", func(t *testing.T) {
		if !pred.Delete(event.DeleteEvent{}) {
			t.Error("Delete should fire")
		}
	})
	t.Run("generation change fires", func(t *testing.T) {
		oldObj := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Generation: 1}}
		newObj := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Generation: 2}}
		if !pred.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}) {
			t.Error("generation change should fire")
		}
	})
	t.Run("status activeBranch change fires", func(t *testing.T) {
		oldObj := &v1.Postgres{Status: &v1.PostgresStatus{ActiveBranch: "old"}}
		newObj := &v1.Postgres{Status: &v1.PostgresStatus{ActiveBranch: "new"}}
		if !pred.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}) {
			t.Error("status.activeBranch change should fire")
		}
	})
	t.Run("irrelevant update is filtered", func(t *testing.T) {
		oldObj := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "1"}}
		newObj := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{ResourceVersion: "2"}}
		if pred.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}) {
			t.Error("resourceVersion-only change should be filtered")
		}
	})
	t.Run("no status change is filtered", func(t *testing.T) {
		oldObj := &v1.Postgres{Status: &v1.PostgresStatus{ActiveBranch: "same"}}
		newObj := &v1.Postgres{Status: &v1.PostgresStatus{ActiveBranch: "same"}}
		if pred.Update(event.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}) {
			t.Error("unchanged status should be filtered")
		}
	})
}

func TestPostgresBindingSourceSecretWatchPredicate(t *testing.T) {
	pred := (&PostgresBindingReconciler{}).RelationshipWatches()[1].Predicate

	caSecret := &corev1.Secret{Data: map[string][]byte{"ca.crt": []byte("ca")}}
	clientCertSecret := &corev1.Secret{Data: map[string][]byte{"tls.crt": []byte("cert"), "tls.key": []byte("key")}}
	unrelatedSecret := &corev1.Secret{Data: map[string][]byte{"FOO": []byte("bar")}}
	incompleteCertSecret := &corev1.Secret{Data: map[string][]byte{"tls.crt": []byte("cert")}}

	t.Run("create fires for CA and client cert secrets", func(t *testing.T) {
		if !pred.Create(event.CreateEvent{Object: caSecret}) {
			t.Error("Create should fire for CA secret")
		}
		if !pred.Create(event.CreateEvent{Object: clientCertSecret}) {
			t.Error("Create should fire for client cert secret")
		}
	})

	t.Run("create is filtered for unrelated or incomplete secrets", func(t *testing.T) {
		if pred.Create(event.CreateEvent{Object: unrelatedSecret}) {
			t.Error("Create should be filtered for unrelated secret")
		}
		if pred.Create(event.CreateEvent{Object: incompleteCertSecret}) {
			t.Error("Create should be filtered for incomplete cert secret")
		}
	})

	t.Run("delete fires only for relevant secrets", func(t *testing.T) {
		if !pred.Delete(event.DeleteEvent{Object: caSecret}) {
			t.Error("Delete should fire for CA secret")
		}
		if pred.Delete(event.DeleteEvent{Object: unrelatedSecret}) {
			t.Error("Delete should be filtered for unrelated secret")
		}
	})

	t.Run("update fires only when relevant secret data changes", func(t *testing.T) {
		updatedCA := &corev1.Secret{Data: map[string][]byte{"ca.crt": []byte("new-ca")}}
		if !pred.Update(event.UpdateEvent{ObjectOld: caSecret, ObjectNew: updatedCA}) {
			t.Error("Update should fire when CA data changes")
		}
		if pred.Update(event.UpdateEvent{ObjectOld: caSecret, ObjectNew: caSecret.DeepCopy()}) {
			t.Error("Update should be filtered when CA data is unchanged")
		}
		updatedUnrelated := &corev1.Secret{Data: map[string][]byte{"FOO": []byte("baz")}}
		if pred.Update(event.UpdateEvent{ObjectOld: unrelatedSecret, ObjectNew: updatedUnrelated}) {
			t.Error("Update should be filtered for unrelated secrets")
		}
	})
}

func TestValidateBindingSnapshot(t *testing.T) {
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "reporter", Namespace: "team"}, Spec: v1.PostgresBindingSpec{
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "reporter", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	caCert, caKey, caPEM := generateTestCA(t)
	validCert, validKey := generateTestClientCert(t, caCert, caKey, binding.RoleName(v1.PostgresBindingCredentialRead))

	t.Run("valid snapshot passes", func(t *testing.T) {
		snapshot := &bindingSnapshot{
			CACertificate: caPEM,
			Credentials: map[v1.PostgresBindingCredential]rcbinding.CredentialMaterial{
				v1.PostgresBindingCredentialRead: {Certificate: validCert, PrivateKey: validKey},
			},
		}
		if err := validateBindingSnapshot(binding, snapshot); err != nil {
			t.Fatalf("validateBindingSnapshot() error = %v", err)
		}
	})

	t.Run("wrong key fails", func(t *testing.T) {
		_, wrongKey := generateTestClientCert(t, caCert, caKey, binding.RoleName(v1.PostgresBindingCredentialRead))
		snapshot := &bindingSnapshot{
			CACertificate: caPEM,
			Credentials: map[v1.PostgresBindingCredential]rcbinding.CredentialMaterial{
				v1.PostgresBindingCredentialRead: {Certificate: validCert, PrivateKey: wrongKey},
			},
		}
		if err := validateBindingSnapshot(binding, snapshot); err == nil {
			t.Fatal("expected validation error for wrong key")
		}
	})

	t.Run("leaf from other CA fails", func(t *testing.T) {
		otherCA, otherCAKey, _ := generateTestCA(t)
		otherCert, otherKey := generateTestClientCert(t, otherCA, otherCAKey, binding.RoleName(v1.PostgresBindingCredentialRead))
		snapshot := &bindingSnapshot{
			CACertificate: caPEM,
			Credentials: map[v1.PostgresBindingCredential]rcbinding.CredentialMaterial{
				v1.PostgresBindingCredentialRead: {Certificate: otherCert, PrivateKey: otherKey},
			},
		}
		if err := validateBindingSnapshot(binding, snapshot); err == nil {
			t.Fatal("expected validation error for leaf from other CA")
		}
	})

	t.Run("wrong CN fails", func(t *testing.T) {
		badCert, badKey := generateTestClientCert(t, caCert, caKey, "wrong-cn")
		snapshot := &bindingSnapshot{
			CACertificate: caPEM,
			Credentials: map[v1.PostgresBindingCredential]rcbinding.CredentialMaterial{
				v1.PostgresBindingCredentialRead: {Certificate: badCert, PrivateKey: badKey},
			},
		}
		if err := validateBindingSnapshot(binding, snapshot); err == nil {
			t.Fatal("expected validation error for wrong CN")
		}
	})

	t.Run("garbage PEM fails", func(t *testing.T) {
		snapshot := &bindingSnapshot{
			CACertificate: []byte("not pem"),
			Credentials: map[v1.PostgresBindingCredential]rcbinding.CredentialMaterial{
				v1.PostgresBindingCredentialRead: {Certificate: validCert, PrivateKey: validKey},
			},
		}
		if err := validateBindingSnapshot(binding, snapshot); err == nil {
			t.Fatal("expected validation error for garbage PEM")
		}
	})
}

func TestUpdateRetainsConfigSecretWhenSnapshotNil(t *testing.T) {
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "reporter", Namespace: "team"}, Spec: v1.PostgresBindingSpec{
		Postgres: "orders", SecretName: "reporter-orders-connection",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "reporter", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	testScheme := runtime.NewScheme()
	initscheme.InitScheme(testScheme)
	r := &PostgresBindingReconciler{Recorder: recorder, Scheme: testScheme}
	prepared := PostgresBindingPreparedData{PhysicalName: "orders-restore"}

	t.Run("claims existing config Secret", func(t *testing.T) {
		related := relatedobjectsmap.NewRelatedObjectsMap(testScheme)
		related.Insert(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: binding.Spec.SecretName, Namespace: binding.Namespace}})
		actions, _, err := r.Update(binding, prepared, related)
		requireNoError(t, err)
		for _, a := range actions {
			if secret, ok := a.GetObject().(*corev1.Secret); ok && secret.Name == binding.Spec.SecretName {
				return
			}
		}
		t.Fatal("expected Claim action for existing config Secret")
	})

	t.Run("no Secret action when config Secret does not exist", func(t *testing.T) {
		actions, _, err := r.Update(binding, prepared, relatedobjectsmap.NewRelatedObjectsMap(testScheme))
		requireNoError(t, err)
		for _, a := range actions {
			if _, ok := a.GetObject().(*corev1.Secret); ok {
				t.Fatal("unexpected Secret action when config Secret is absent")
			}
		}
	})
}

func TestUpdateRetainsDatabaseRolesForPreviousInstances(t *testing.T) {
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "reporter", Namespace: "team", UID: "binding-uid"}, Spec: v1.PostgresBindingSpec{
		Postgres:    "orders",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "reporter", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialReadWrite},
	}}
	previous, err := rcbinding.CreateDatabaseRole(scheme.Scheme, binding, "orders-primary", v1.PostgresBindingCredentialReadWrite)
	requireNoError(t, err)
	removedCredential, err := rcbinding.CreateDatabaseRole(scheme.Scheme, binding, "orders-primary", v1.PostgresBindingCredentialRead)
	requireNoError(t, err)
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	related.Insert(previous)
	related.Insert(removedCredential)

	actions, _, err := (&PostgresBindingReconciler{Recorder: recorder, Scheme: scheme.Scheme}).Update(binding, PostgresBindingPreparedData{PhysicalName: "orders-restored"}, related)
	requireNoError(t, err)
	foundPrevious := false
	foundRevocation := false
	for _, a := range actions {
		if a.GetObject().GetName() == removedCredential.GetName() {
			role := a.GetObject().(*cnpgv1.DatabaseRole)
			if role.Spec.Login || len(role.Spec.InRoles) != 0 || role.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimRetain {
				t.Fatal("removed credential must be revoked before its CR is deleted")
			}
			foundRevocation = true
		}
		if a.GetObject().GetName() == previous.GetName() {
			foundPrevious = true
		}
	}
	if !foundPrevious {
		t.Fatal("expected action retaining the previous instance DatabaseRole")
	}
	if !foundRevocation {
		t.Fatal("removed credential bypassed revocation")
	}
}

func TestPrepareBindingWaitsForDirectStatus(t *testing.T) {
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "reporter", Namespace: "team"}, Spec: v1.PostgresBindingSpec{
		Postgres: "orders", SecretName: "reporter-orders-connection",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "reporter", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	// Without direct status, a ready cluster of another database must not be used.
	decoy := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: rccnpg.ClusterNameFor(decoyPhysicalName), Namespace: "team"}}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Status: &v1.PostgresStatus{ActiveBranch: "pr-123"}}
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(postgres, decoy).Build()

	_, _, err := (&PostgresBindingReconciler{}).Prepare(context.Background(), reader, binding)
	if err == nil {
		t.Fatal("used another database instead of waiting for direct status")
	}
}

func TestPostgresBindingRelationshipMappers(t *testing.T) {
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "reporter", Namespace: "team"}, Spec: v1.PostgresBindingSpec{
		Postgres: "orders", SecretName: "reporter-orders-connection",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "reporter", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	otherBinding := binding.DeepCopy()
	otherBinding.Name = "unrelated"
	otherBinding.Spec.Postgres = "other"
	physicalName := v1.PostgresPhysicalName("orders")
	postgres, cluster := directBindingTarget()
	cluster.Spec.Certificates = &cnpgv1.CertificatesConfiguration{ClientCASecret: "orders-ca"}
	roleName := rcbinding.DatabaseRoleName(binding, physicalName, v1.PostgresBindingCredentialRead)
	certificateName := (&cnpgv1.DatabaseRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}}).GetClientCertSecretName()
	r := &PostgresBindingReconciler{Recorder: recorder, Scheme: scheme.Scheme}
	index := r.Indexes()[0]
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).
		WithIndex(index.Object, index.Field, index.ExtractValue).
		WithObjects(binding, otherBinding, postgres, cluster).
		Build()

	requests, err := r.bindingsForPostgres(context.Background(), reader, postgres)
	requireNoError(t, err)
	requireEqual(t, len(requests), 1, "Postgres request count")
	requireEqual(t, requests[0].Name, binding.Name, "Postgres mapper target")

	for _, secretName := range []string{"orders-ca", certificateName} {
		requests, err = r.bindingsForSourceSecret(context.Background(), reader, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: "team"}})
		requireNoError(t, err)
		requireEqual(t, len(requests), 1, "source Secret request count")
		requireEqual(t, requests[0].Name, binding.Name, "source Secret mapper target")
	}

	requests, err = r.bindingsForSourceSecret(context.Background(), reader, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "team"}})
	requireNoError(t, err)
	requireEqual(t, len(requests), 0, "unrelated Secret request count")
}

func TestPostgresBindingReconciliation(t *testing.T) {
	t.Run("removes the finalizer when the referenced Postgres is already missing", func(t *testing.T) {
		binding := &v1.PostgresBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "missing-postgres-deletion",
				Namespace:  "default",
				Finalizers: []string{"postgresbinding.nais.io"},
			},
			Spec: v1.PostgresBindingSpec{
				Postgres: "already-gone", SecretName: "missing-postgres-connection",
				Consumer: v1.PostgresBindingConsumer{
					Workload: &v1.PostgresBindingWorkload{
						Name: "myapp",
						Type: v1.PostgresBindingWorkloadTypeApplication,
					},
				},
				Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
			},
		}
		requireNoError(t, k8sClient.Create(context.Background(), binding))
		requireNoError(t, k8sClient.Delete(context.Background(), binding))

		bindingReconciler := &PostgresBindingReconciler{Recorder: recorder, Scheme: scheme.Scheme}
		syncReconciler := synchronizer.NewSynchronizer(k8sClient, scheme.Scheme, bindingReconciler, recorder)
		key := client.ObjectKeyFromObject(binding)
		_, err := syncReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: key})
		requireNoError(t, err)

		err = k8sClient.Get(context.Background(), key, &v1.PostgresBinding{})
		requireTrue(t, apierrors.IsNotFound(err), "binding should be deleted after finalizer removal")
	})
}
