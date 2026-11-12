package controller

import (
	"context"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/initscheme"
	rcbinding "github.com/nais/pgrator/internal/resourcecreator/binding"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestBindingSelectedBranchConnection(t *testing.T) {
	testScheme := runtime.NewScheme()
	initscheme.InitScheme(testScheme)
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: "team", UID: "binding-uid"}, Spec: v1.PostgresBindingSpec{
		Postgres: "orders", SecretName: "consumer-connection",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "consumer", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	sources := make([]client.Object, 0, 8)
	certificates := make(map[string][]byte)
	for _, name := range []string{"main", "pr-123"} {
		branch := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", name), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: name}}
		cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: rccnpg.ClusterNameFor(branch.Name), Namespace: "team"}}
		ca, key, caPEM := generateTestCA(t)
		cert, privateKey := generateTestClientCert(t, ca, key, binding.RoleName(v1.PostgresBindingCredentialRead))
		certificates[name] = caPEM
		roleName := rcbinding.DatabaseRoleName(binding, branch.Name, v1.PostgresBindingCredentialRead)
		sources = append(sources, branch, cluster,
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: cluster.GetClientCASecretName(), Namespace: "team"}, Data: map[string][]byte{"ca.crt": caPEM}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: (&cnpgv1.DatabaseRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}}).GetClientCertSecretName(), Namespace: "team"}, Data: map[string][]byte{"tls.crt": cert, "tls.key": privateKey}},
		)
	}
	for _, tt := range []struct{ name, branch, requested, observed, want string }{
		{name: "omitted uses direct identity", requested: "pr-123", observed: "main", want: "main"},
		{name: "stale branch metadata cannot select another database", requested: "pr-123", observed: "pr-123", want: "main"},
		{name: "explicit main remains compatible", branch: "main", want: "main"},
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
			requireEqual(t, prepared.Branch, v1.PostgresBranchObjectName("orders", tt.want), "selected physical branch")
			requireNotNil(t, prepared.Snapshot, "selected branch credentials")
			actions, _, err := r.Update(obj, prepared, relatedobjectsmap.NewRelatedObjectsMap(testScheme))
			requireNoError(t, err)
			found := false
			for _, a := range actions {
				switch resource := a.GetObject().(type) {
				case *corev1.Secret:
					found = true
					requireEqual(t, resource.StringData["READ_PGHOST"], rccnpg.PoolerNameFor(prepared.Branch)+".team", "selected host")
					requireEqual(t, string(resource.Data["ca.crt"]), string(certificates[tt.want]), "selected CA")
				case *cnpgv1.DatabaseRole:
					requireEqual(t, resource.Spec.ClusterRef.Name, rccnpg.ClusterNameFor(prepared.Branch), "selected role cluster")
				}
			}
			requireTrue(t, found, "connection Secret should be published")
		})
	}
}

func TestBindingUnavailableBranchDoesNotFallBack(t *testing.T) {
	testScheme := runtime.NewScheme()
	initscheme.InitScheme(testScheme)
	for _, deleting := range []bool{false, true} {
		name := "missing"
		if deleting {
			name = "deleting"
		}
		t.Run(name, func(t *testing.T) {
			binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: "team", UID: "binding-uid"}, Spec: v1.PostgresBindingSpec{
				Postgres: "orders", Branch: "pr-123", SecretName: "connection",
				Consumer: v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "consumer", Type: v1.PostgresBindingWorkloadTypeApplication}}, Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
			}}
			postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Status: &v1.PostgresStatus{ActiveBranch: "main"}}
			main := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "main"), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"}}
			builder := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(postgres, main)
			if deleting {
				branch := main.DeepCopy()
				branch.Name = v1.PostgresBranchObjectName("orders", "pr-123")
				branch.Spec.BranchName = "pr-123"
				branch.Finalizers = []string{"postgresbranch.nais.io"}
				branch.DeletionTimestamp = new(metav1.Now())
				builder.WithObjects(branch)
			}
			reader := builder.Build()
			r := &PostgresBindingReconciler{Scheme: testScheme, Recorder: recorder}
			_, _, err := r.Prepare(t.Context(), reader, binding)
			if err == nil {
				t.Fatal("non-main selection was accepted")
			}
		})
	}
}

func TestBindingPinnedBranchSourceEvents(t *testing.T) {
	testScheme := runtime.NewScheme()
	initscheme.InitScheme(testScheme)
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: "team"}, Spec: v1.PostgresBindingSpec{Postgres: "orders", Branch: "main", Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialAdmin}}}
	postgres, cluster := directBindingTarget()
	branch := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresPhysicalName("orders"), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"}}
	r := &PostgresBindingReconciler{}
	index := r.Indexes()[0]
	reader := fake.NewClientBuilder().WithScheme(testScheme).WithIndex(index.Object, index.Field, index.ExtractValue).WithObjects(binding, postgres, branch, cluster).Build()
	requests, err := r.bindingsForPostgres(t.Context(), reader, postgres)
	requireNoError(t, err)
	requireEqual(t, len(requests), 1, "branch event should wake waiting binding")
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

func TestPostgresRetainsPinnedBranchWhenActivationRequestRemoved(t *testing.T) {
	testScheme := runtime.NewScheme()
	initscheme.InitScheme(testScheme)
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", UID: "postgres-uid"}, Spec: v1.PostgresSpec{MajorVersion: "18"}, Status: &v1.PostgresStatus{ActiveBranch: "restored"}}
	branch := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "pr-123"), Namespace: "team", UID: "branch-uid"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "pr-123"}}
	r := &PostgresReconciler{Scheme: testScheme, Recorder: recorder}
	reader := fake.NewClientBuilder().WithScheme(testScheme).WithStatusSubresource(postgres, branch).WithObjects(postgres, branch).Build()
	sync := synchronizer.NewSynchronizer(reader, testScheme, r, recorder)
	sync.GetOwnerManager().AddOwnerAnnotation(branch, postgres)
	requireNoError(t, reader.Update(context.Background(), branch))
	_, err := sync.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(postgres)})
	requireNoError(t, err)
	retained := &v1.PostgresBranch{}
	requireNoError(t, reader.Get(t.Context(), client.ObjectKeyFromObject(branch), retained))
	requireTrue(t, retained.DeletionTimestamp.IsZero(), "inactive branch must survive parent reconciliation")
}

func TestExplicitBranchWaitDoesNotKeepAnotherBranchConnection(t *testing.T) {
	testScheme := runtime.NewScheme()
	initscheme.InitScheme(testScheme)
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: "team", UID: "binding-uid"}, Spec: v1.PostgresBindingSpec{
		Postgres: "orders", Branch: "pr-123", SecretName: "connection",
		Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "consumer", Type: v1.PostgresBindingWorkloadTypeApplication}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
	}}
	prepared := PostgresBindingPreparedData{Branch: v1.PostgresBranchObjectName("orders", "pr-123")}
	for _, branch := range []string{"main", "pr-123"} {
		t.Run(branch, func(t *testing.T) {
			related := relatedobjectsmap.NewRelatedObjectsMap(testScheme)
			related.Insert(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "connection", Namespace: "team"}, Data: map[string][]byte{
				"READ_PGHOST": []byte(rccnpg.PoolerNameFor(v1.PostgresBranchObjectName("orders", branch)) + ".team"),
			}})
			actions, _, err := (&PostgresBindingReconciler{Scheme: testScheme, Recorder: recorder}).Update(binding.DeepCopy(), prepared, related)
			requireNoError(t, err)
			kept := false
			for _, a := range actions {
				if _, ok := a.GetObject().(*corev1.Secret); ok {
					kept = true
				}
			}
			requireEqual(t, kept, branch == "pr-123", "only same-branch snapshots may be retained during credential provisioning")
		})
	}
}
