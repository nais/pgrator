package controller

import (
	"fmt"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/google/go-cmp/cmp"
	rcbinding "github.com/nais/pgrator/internal/resourcecreator/binding"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestBindingCredentialsSurviveBranchDetach(t *testing.T) {
	for _, workload := range []v1.PostgresBindingWorkloadType{v1.PostgresBindingWorkloadTypeApplication, v1.PostgresBindingWorkloadTypeJob} {
		for _, selected := range []string{"", "main"} {
			t.Run(fmt.Sprintf("%s/branch=%s", workload, selected), func(t *testing.T) {
				r, c, pg, b, _ := legacyOwnershipFixture(t, false)
				binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "consumer", Namespace: pg.Namespace, UID: "binding-uid"}, Spec: v1.PostgresBindingSpec{Postgres: pg.Name, Branch: selected, SecretName: "consumer-connection", Consumer: v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "consumer", Type: workload}}, Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead, v1.PostgresBindingCredentialAdmin}}}
				cluster := &cnpgv1.Cluster{}
				requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: v1.CNPGClusterName(b.Name)}, cluster))
				ca, key, caPEM := generateTestCA(t)
				requireNoError(t, c.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: cluster.GetClientCASecretName(), Namespace: pg.Namespace}, Data: map[string][]byte{"ca.crt": caPEM}}))
				for _, credential := range binding.Spec.Credentials {
					roleName := rcbinding.DatabaseRoleName(binding, b.Name, credential)
					userName := binding.RoleName(credential)
					if credential == v1.PostgresBindingCredentialAdmin {
						roleName = cluster.Name + "-app"
						userName = "app"
					}
					cert, privateKey := generateTestClientCert(t, ca, key, userName)
					requireNoError(t, c.Create(t.Context(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: (&cnpgv1.DatabaseRole{ObjectMeta: metav1.ObjectMeta{Name: roleName}}).GetClientCertSecretName(), Namespace: pg.Namespace}, Data: map[string][]byte{"tls.crt": cert, "tls.key": privateKey}}))
				}
				bindingReconciler := &PostgresBindingReconciler{Scheme: scheme.Scheme, Recorder: recorder}
				before, _, err := bindingReconciler.Prepare(t.Context(), c, binding)
				requireNoError(t, err)
				if before.Snapshot == nil {
					t.Fatal("fixture did not resolve credentials")
				}
				for range 2 {
					s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
					_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
					requireNoError(t, err)
				}
				after, _, err := bindingReconciler.Prepare(t.Context(), c, binding)
				requireNoError(t, err)
				if diff := cmp.Diff(before, after); diff != "" {
					t.Fatalf("connection snapshot changed (-before +after): %s", diff)
				}
				// Whole desired resource equality covers hostname, connection Secret,
				// certificate/private key, role membership and network selectors.
				var expected []client.Object
				for _, prepared := range []PostgresBindingPreparedData{before, after} {
					planned, _, err := bindingReconciler.Update(binding, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
					requireNoError(t, err)
					var actual []client.Object
					for _, a := range planned {
						actual = append(actual, a.GetObject())
					}
					if expected == nil {
						expected = actual
					} else if diff := cmp.Diff(expected, actual); diff != "" {
						t.Fatalf("binding resource contract changed: %s", diff)
					}
				}
				names, err := bindingSourceSecretNames(t.Context(), c, binding)
				requireNoError(t, err)
				if len(names) != 3 {
					t.Fatalf("source-secret watch lost sources after detach: %v", names)
				}
			})
		}
	}
}

func TestBindingDoesNotWithdrawCredentialsOnInvalidDirectTarget(t *testing.T) {
	for _, issue := range []string{"absent-direct-cluster", "replaced", "foreign-owner", "non-main"} {
		t.Run(issue, func(t *testing.T) {
			r, c, pg, b, _ := legacyOwnershipFixture(t, false)
			for range 2 {
				s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
				_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
				requireNoError(t, err)
			}
			binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "invalid-target-consumer", Namespace: pg.Namespace, UID: "binding-uid"}, Spec: v1.PostgresBindingSpec{Postgres: pg.Name, SecretName: "existing-connection", Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead}}}
			if issue == "non-main" {
				binding.Spec.Branch = "restore"
			}
			requireNoError(t, c.Create(t.Context(), binding))
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: binding.Spec.SecretName, Namespace: pg.Namespace, UID: "connection-uid"}, Data: map[string][]byte{"keep": []byte("existing-credentials")}}
			requireNoError(t, controllerutil.SetControllerReference(binding, secret, scheme.Scheme))
			requireNoError(t, c.Create(t.Context(), secret))
			before := secret.DeepCopy()
			cluster := &cnpgv1.Cluster{}
			requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: v1.CNPGClusterName(b.Name)}, cluster))
			switch issue {
			case "absent-direct-cluster":
				requireNoError(t, c.Delete(t.Context(), cluster))
			case "replaced":
				requireNoError(t, c.Delete(t.Context(), cluster))
				cluster.UID = "another-database"
				cluster.ResourceVersion = ""
				requireNoError(t, c.Create(t.Context(), cluster))
			case "foreign-owner":
				cluster.OwnerReferences[0].UID = "foreign-owner-uid"
				requireNoError(t, c.Update(t.Context(), cluster))
			}
			bindingReconciler := &PostgresBindingReconciler{Scheme: scheme.Scheme, Recorder: recorder}
			s := synchronizer.NewSynchronizer(c, scheme.Scheme, bindingReconciler, recorder)
			if _, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(binding)}); err == nil {
				t.Fatal("invalid direct target unexpectedly resolved")
			}
			requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(secret), secret))
			if diff := cmp.Diff(before, secret); diff != "" {
				t.Fatalf("invalid target withdrew/changed credentials: %s", diff)
			}
		})
	}
}
