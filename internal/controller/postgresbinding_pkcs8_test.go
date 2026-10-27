package controller

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/initscheme"
	rcbinding "github.com/nais/pgrator/internal/resourcecreator/binding"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestBindingSecretPKCS8PreservesKeyIdentity(t *testing.T) {
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "reporter", Namespace: "team"}, Spec: v1.PostgresBindingSpec{
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "reporter"}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	ca, caKey, caPEM := generateTestCA(t)
	ecCert, ecPEM := generateTestClientCert(t, ca, caKey, binding.RoleName(v1.PostgresBindingCredentialRead))
	ecKey, err := parsePrivateKey(ecPEM)
	requireNoError(t, err)
	ecPKCS8, err := x509.MarshalPKCS8PrivateKey(ecKey)
	requireNoError(t, err)

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	requireNoError(t, err)
	leaf, _, err := parseLeafCertificate(ecCert)
	requireNoError(t, err)
	rsaCert, err := x509.CreateCertificate(rand.Reader, leaf, ca, &rsaKey.PublicKey, caKey)
	requireNoError(t, err)
	rsaPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rsaCert})
	rsaPKCS8, err := x509.MarshalPKCS8PrivateKey(rsaKey)
	requireNoError(t, err)

	for _, tt := range []struct {
		name        string
		certificate []byte
		key         []byte
	}{
		{"EC SEC1", ecCert, ecPEM},
		{"EC PKCS8", ecCert, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecPKCS8})},
		{"RSA PKCS1", rsaPEM, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)})},
		{"RSA PKCS8", rsaPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaPKCS8})},
	} {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := &bindingSnapshot{CACertificate: caPEM, Credentials: map[v1.PostgresBindingCredential]rcbinding.CredentialMaterial{
				v1.PostgresBindingCredentialRead: {Certificate: tt.certificate, PrivateKey: tt.key},
			}}
			requireNoError(t, validateBindingSnapshot(binding, snapshot))
			secret, err := rcbinding.CreateConfigSecret(schemeForPKCS8Test(), binding, "orders-main", snapshot.CACertificate, snapshot.Credentials)
			requireNoError(t, err)
			if !bytes.Equal(secret.Data["read.tls.key"], tt.key) || !bytes.Equal(secret.Data["read.tls.crt"], tt.certificate) || !bytes.Equal(secret.Data["ca.crt"], caPEM) {
				t.Fatal("existing PEM key and certificate material must remain unchanged")
			}
			assertPKCS8MatchesCertificate(t, secret, "read")
		})
	}
}

func TestBindingPKCS8RotatesWithCompleteCredentialSnapshot(t *testing.T) {
	ctx := context.Background()
	scheme := schemeForPKCS8Test()
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: "team", UID: "binding-uid"}, Spec: v1.PostgresBindingSpec{
		Postgres: "orders", SecretName: "consumer-connection",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "consumer"}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialAdmin, v1.PostgresBindingCredentialRead, v1.PostgresBindingCredentialReadWrite},
	}}
	branch := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "main"), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"}}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Status: &v1.PostgresStatus{ActiveBranch: "main"}}
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: rccnpg.ClusterNameFor(branch.Name), Namespace: "team"}}
	ca, caKey, caPEM := generateTestCA(t)
	objects := make([]client.Object, 0, 5+2*len(binding.Spec.Credentials))
	objects = append(objects, binding, branch, postgres, cluster,
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: cluster.GetClientCASecretName(), Namespace: "team"}, Data: map[string][]byte{"ca.crt": caPEM}},
	)
	sources := make(map[v1.PostgresBindingCredential]*corev1.Secret)
	for _, credential := range binding.Spec.Credentials {
		role, err := rcbinding.CreateDatabaseRole(scheme, binding, branch.Name, credential)
		requireNoError(t, err)
		if credential == v1.PostgresBindingCredentialAdmin {
			role.Spec.Name = rccnpg.OwnerRole
		}
		cert, key := generateTestClientCert(t, ca, caKey, binding.RoleName(credential))
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: role.GetClientCertSecretName(), Namespace: "team"}, Data: map[string][]byte{"tls.crt": cert, "tls.key": key}}
		sources[credential] = secret
		objects = append(objects, role, secret)
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(binding).WithObjects(objects...).Build()
	r := &PostgresBindingReconciler{Recorder: recorder, Scheme: scheme}
	sync := synchronizer.NewSynchronizer(kube, scheme, r, recorder)
	reconcileSecret := func() *corev1.Secret {
		t.Helper()
		_, err := sync.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(binding)})
		requireNoError(t, err)
		secret := &corev1.Secret{}
		requireNoError(t, kube.Get(ctx, client.ObjectKey{Namespace: "team", Name: binding.Spec.SecretName}, secret))
		return secret
	}
	original := reconcileSecret()
	for _, credential := range binding.Spec.Credentials {
		assertPKCS8MatchesCertificate(t, original, string(credential))
	}

	source := sources[v1.PostgresBindingCredentialReadWrite]
	requireNoError(t, kube.Get(ctx, client.ObjectKeyFromObject(source), source))
	cert, key := generateTestClientCert(t, ca, caKey, binding.RoleName(v1.PostgresBindingCredentialReadWrite))
	for _, tt := range []struct {
		name string
		key  []byte
	}{
		{"missing key", nil},
		{"malformed key", []byte("not a private key")},
		{"mismatched key", source.Data["tls.key"]},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source.Data = map[string][]byte{"tls.crt": cert, "tls.key": tt.key}
			requireNoError(t, kube.Update(ctx, source))
			retained := reconcileSecret()
			for name, data := range original.Data {
				if !bytes.Equal(retained.Data[name], data) {
					t.Errorf("incomplete/invalid rotation changed %s", name)
				}
			}
		})
	}

	source.Data = map[string][]byte{"tls.crt": cert, "tls.key": key}
	requireNoError(t, kube.Update(ctx, source))
	rotated := reconcileSecret()
	for _, credential := range binding.Spec.Credentials {
		prefix := string(credential)
		assertPKCS8MatchesCertificate(t, rotated, prefix)
		if credential == v1.PostgresBindingCredentialReadWrite {
			if !bytes.Equal(rotated.Data[prefix+".tls.key"], key) || !bytes.Equal(rotated.Data[prefix+".tls.crt"], cert) || bytes.Equal(rotated.Data[prefix+".tls.pk8"], original.Data[prefix+".tls.pk8"]) {
				t.Fatal("complete rotation must replace certificate and both key representations together")
			}
		} else if !bytes.Equal(rotated.Data[prefix+".tls.pk8"], original.Data[prefix+".tls.pk8"]) {
			t.Errorf("rotation changed unrelated %s credential", credential)
		}
	}
}

func schemeForPKCS8Test() *runtime.Scheme {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	return scheme
}

func assertPKCS8MatchesCertificate(t *testing.T, secret *corev1.Secret, credential string) {
	t.Helper()
	key, err := x509.ParsePKCS8PrivateKey(secret.Data[credential+".tls.pk8"])
	if err != nil {
		t.Fatalf("%s PKCS#8 DER: %v", credential, err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		t.Fatalf("%s private key is not a signer", credential)
	}
	cert, _, err := parseLeafCertificate(secret.Data[credential+".tls.crt"])
	requireNoError(t, err)
	if !publicKeysEqual(cert.PublicKey, signer.Public()) {
		t.Errorf("%s PKCS#8 key does not match the client certificate", credential)
	}
}
