package controller

import (
	"context"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rcbinding "github.com/nais/pgrator/internal/resourcecreator/binding"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestPostgresBindingSnapshot(t *testing.T) {
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "reporter", Namespace: "team"}, Spec: v1.PostgresBindingSpec{
		Postgres:    "orders",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "reporter", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	instance := &v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders-restore", Namespace: "team"}, Spec: v1.PostgresInstanceSpec{Postgres: "orders"}}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveInstance: instance.Name}}
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: rccnpg.ClusterNameFor(instance.Name), Namespace: "team"}, Spec: cnpgv1.ClusterSpec{Certificates: &cnpgv1.CertificatesConfiguration{ClientCASecret: "orders-ca"}}}
	roleName := rcbinding.DatabaseRoleName(binding, instance.Name, v1.PostgresBindingCredentialRead)
	certSecret := (&cnpgv1.DatabaseRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}}).GetClientCertSecretName()

	t.Run("publishes a complete snapshot", func(t *testing.T) {
		reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(postgres, instance, cluster,
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "orders-ca", Namespace: "team"}, Data: map[string][]byte{"ca.crt": []byte("ca")}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: certSecret, Namespace: "team"}, Data: map[string][]byte{"tls.crt": []byte("cert"), "tls.key": []byte("key")}},
		).Build()
		r := &PostgresBindingReconciler{Recorder: recorder, Scheme: scheme.Scheme}
		prepared, _, err := r.Prepare(context.Background(), reader, binding)
		requireNoError(t, err)
		requireNotNil(t, prepared.Snapshot, "complete material should produce a snapshot")
		actions, _, err := r.Update(binding, prepared, nil)
		requireNoError(t, err)
		for _, action := range actions {
			secret, ok := action.GetObject().(*corev1.Secret)
			if !ok || secret.Name != binding.Name {
				continue
			}
			requireEqual(t, string(secret.Data["ca.crt"]), "ca", "CA certificate")
			requireEqual(t, string(secret.Data["read.tls.crt"]), "cert", "client certificate")
			requireEqual(t, string(secret.Data["read.tls.key"]), "key", "client key")
			requireEqual(t, secret.StringData["READ_PGHOST"], "pg-orders-restore-pooler.team", "active instance host")
			return
		}
		t.Fatal("missing stable binding Secret action")
	})

	t.Run("does not replace a stable secret while source material is incomplete", func(t *testing.T) {
		reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(postgres, instance, cluster,
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "orders-ca", Namespace: "team"}, Data: map[string][]byte{"ca.crt": []byte("ca")}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: certSecret, Namespace: "team"}, Data: map[string][]byte{"tls.crt": []byte("cert")}},
		).Build()
		r := &PostgresBindingReconciler{Recorder: recorder, Scheme: scheme.Scheme}
		prepared, _, err := r.Prepare(context.Background(), reader, binding)
		requireNoError(t, err)
		requireNil(t, prepared.Snapshot, "missing certificate must not produce a snapshot")
		actions, _, err := r.Update(binding, prepared, nil)
		requireNoError(t, err)
		for _, action := range actions {
			if action.GetObject().GetName() == binding.Name {
				if _, ok := action.GetObject().(*corev1.Secret); ok {
					t.Fatal("incomplete material must not update the stable binding Secret")
				}
			}
		}
	})
}

func TestPrepareBindingRetainsStatusActiveInstanceWhenSpecIsRemoved(t *testing.T) {
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "reporter", Namespace: "team"}, Spec: v1.PostgresBindingSpec{
		Postgres:    "orders",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "reporter", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	instance := &v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders-restore", Namespace: "team"}, Spec: v1.PostgresInstanceSpec{Postgres: "orders"}}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Status: &v1.PostgresStatus{ActiveInstance: instance.Name}}
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(postgres, instance).Build()

	prepared, _, err := (&PostgresBindingReconciler{}).Prepare(context.Background(), reader, binding)
	requireNoError(t, err)
	requireEqual(t, prepared.Instance, instance.Name, "active instance")
}

func TestPostgresBindingRelationshipMappers(t *testing.T) {
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "reporter", Namespace: "team"}, Spec: v1.PostgresBindingSpec{
		Postgres:    "orders",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "reporter", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	otherBinding := binding.DeepCopy()
	otherBinding.Name = "unrelated"
	otherBinding.Spec.Postgres = "other"
	instance := &v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders-primary", Namespace: "team"}, Spec: v1.PostgresInstanceSpec{Postgres: "orders"}}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresSpec{ActiveInstance: instance.Name}}
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: rccnpg.ClusterNameFor(instance.Name), Namespace: "team"}, Spec: cnpgv1.ClusterSpec{Certificates: &cnpgv1.CertificatesConfiguration{ClientCASecret: "orders-ca"}}}
	roleName := rcbinding.DatabaseRoleName(binding, instance.Name, v1.PostgresBindingCredentialRead)
	certificateName := (&cnpgv1.DatabaseRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}}).GetClientCertSecretName()
	r := &PostgresBindingReconciler{Recorder: recorder, Scheme: scheme.Scheme}
	index := r.Indexes()[0]
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).
		WithIndex(index.Object, index.Field, index.ExtractValue).
		WithObjects(binding, otherBinding, postgres, instance, cluster).
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
				Postgres: "already-gone",
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
