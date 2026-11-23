package controller

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/initscheme"
	rcbinding "github.com/nais/pgrator/internal/resourcecreator/binding"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// decoyPhysicalName is the physical name of another database of "orders"
// (a former non-main branch). Deprecated branch fields must never select it.
const decoyPhysicalName = "orders-pr-123-0b9a1c3e"

func TestBindingDeprecatedBranchFieldsUseDirectIdentity(t *testing.T) {
	testScheme := runtime.NewScheme()
	initscheme.InitScheme(testScheme)
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: "team", UID: "binding-uid"}, Spec: v1.PostgresBindingSpec{
		Postgres: "orders", SecretName: "consumer-connection",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "consumer", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	sources := make([]client.Object, 0, 8)
	certificates := make(map[string][]byte)
	for _, physicalName := range []string{v1.PostgresPhysicalName("orders"), decoyPhysicalName} {
		cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: rccnpg.ClusterNameFor(physicalName), Namespace: "team"}}
		ca, key, caPEM := generateTestCA(t)
		cert, privateKey := generateTestClientCert(t, ca, key, binding.RoleName(v1.PostgresBindingCredentialRead))
		certificates[physicalName] = caPEM
		roleName := rcbinding.DatabaseRoleName(binding, physicalName, v1.PostgresBindingCredentialRead)
		sources = append(sources, cluster,
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: cluster.GetClientCASecretName(), Namespace: "team"}, Data: map[string][]byte{"ca.crt": caPEM}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: (&cnpgv1.DatabaseRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}}).GetClientCertSecretName(), Namespace: "team"}, Data: map[string][]byte{"tls.crt": cert, "tls.key": privateKey}},
		)
	}
	want := v1.PostgresPhysicalName("orders")
	for _, tt := range []struct{ name, branch, requested, observed string }{
		{name: "omitted uses direct identity", requested: "pr-123", observed: "main"},
		{name: "stale branch metadata cannot select another database", requested: "pr-123", observed: "pr-123"},
		{name: "explicit main remains compatible", branch: "main"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			obj := binding.DeepCopy()
			obj.Spec.Branch = tt.branch
			postgres, cluster := directBindingTarget()
			postgres.Spec.ActiveBranch = tt.requested
			postgres.Status.ActiveBranch = tt.observed
			for _, source := range sources {
				if candidate, ok := source.(*cnpgv1.Cluster); ok && candidate.Name == cluster.Name {
					candidate.OwnerReferences = cluster.OwnerReferences
				}
			}
			reader := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(sources...).WithObjects(postgres).Build()
			r := &PostgresBindingReconciler{Scheme: testScheme, Recorder: recorder}
			prepared, _, err := r.Prepare(t.Context(), reader, obj)
			requireNoError(t, err)
			requireEqual(t, prepared.PhysicalName, want, "selected physical name")
			requireNotNil(t, prepared.Snapshot, "selected credentials")
			actions, _, err := r.Update(obj, prepared, relatedobjectsmap.NewRelatedObjectsMap(testScheme))
			requireNoError(t, err)
			found := false
			for _, a := range actions {
				switch resource := a.GetObject().(type) {
				case *corev1.Secret:
					found = true
					requireEqual(t, resource.StringData["READ_PGHOST"], rccnpg.PoolerNameFor(prepared.PhysicalName)+".team", "selected host")
					requireEqual(t, string(resource.Data["ca.crt"]), string(certificates[want]), "selected CA")
				case *cnpgv1.DatabaseRole:
					requireEqual(t, resource.Spec.ClusterRef.Name, rccnpg.ClusterNameFor(prepared.PhysicalName), "selected role cluster")
				}
			}
			requireTrue(t, found, "connection Secret should be published")
		})
	}
}

func TestBindingRejectsNonMainBranch(t *testing.T) {
	testScheme := runtime.NewScheme()
	initscheme.InitScheme(testScheme)
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: "team", UID: "binding-uid"}, Spec: v1.PostgresBindingSpec{
		Postgres: "orders", Branch: "pr-123", SecretName: "connection",
		Consumer: v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "consumer", Type: v1.PostgresBindingWorkloadTypeApplication}}, Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	postgres, cluster := directBindingTarget()
	decoy := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: rccnpg.ClusterNameFor(decoyPhysicalName), Namespace: "team"}}
	reader := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(postgres, cluster, decoy).Build()
	r := &PostgresBindingReconciler{Scheme: testScheme, Recorder: recorder}
	if _, _, err := r.Prepare(t.Context(), reader, binding); err == nil {
		t.Fatal("non-main selection was accepted")
	}
}

func TestBindingPinnedMainSourceEvents(t *testing.T) {
	testScheme := runtime.NewScheme()
	initscheme.InitScheme(testScheme)
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: "team"}, Spec: v1.PostgresBindingSpec{Postgres: "orders", Branch: "main", Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialAdmin}}}
	postgres, cluster := directBindingTarget()
	r := &PostgresBindingReconciler{}
	index := r.Indexes()[0]
	reader := fake.NewClientBuilder().WithScheme(testScheme).WithIndex(index.Object, index.Field, index.ExtractValue).WithObjects(binding, postgres, cluster).Build()
	requests, err := r.bindingsForPostgres(t.Context(), reader, postgres)
	requireNoError(t, err)
	requireEqual(t, len(requests), 1, "Postgres event should wake waiting binding")
	for _, name := range []string{cluster.GetClientCASecretName(), "unrelated-ca"} {
		requests, err = r.bindingsForSourceSecret(t.Context(), reader, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "team"}})
		requireNoError(t, err)
		want := 0
		if name == cluster.GetClientCASecretName() {
			want = 1
		}
		requireEqual(t, len(requests), want, "pinned source event requests")
	}
}

func TestBindingWaitDoesNotKeepAnotherDatabaseConnection(t *testing.T) {
	testScheme := runtime.NewScheme()
	initscheme.InitScheme(testScheme)
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: "team", UID: "binding-uid"}, Spec: v1.PostgresBindingSpec{
		Postgres: "orders", Branch: "pr-123", SecretName: "connection",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "consumer", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	prepared := PostgresBindingPreparedData{PhysicalName: decoyPhysicalName}
	for _, physicalName := range []string{v1.PostgresPhysicalName("orders"), decoyPhysicalName} {
		t.Run(physicalName, func(t *testing.T) {
			related := relatedobjectsmap.NewRelatedObjectsMap(testScheme)
			related.Insert(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "connection", Namespace: "team"}, Data: map[string][]byte{
				"READ_PGHOST": []byte(rccnpg.PoolerNameFor(physicalName) + ".team"),
			}})
			actions, _, err := (&PostgresBindingReconciler{Scheme: testScheme, Recorder: recorder}).Update(binding.DeepCopy(), prepared, related)
			requireNoError(t, err)
			kept := false
			for _, a := range actions {
				if _, ok := a.GetObject().(*corev1.Secret); ok {
					kept = true
				}
			}
			requireEqual(t, kept, physicalName == decoyPhysicalName, "only snapshots of the prepared database may be retained during credential provisioning")
		})
	}
}
