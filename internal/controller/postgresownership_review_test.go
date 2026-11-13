package controller

import (
	"fmt"
	"reflect"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/google/go-cmp/cmp"
	rcbinding "github.com/nais/pgrator/internal/resourcecreator/binding"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func managedOwnershipFixture(t *testing.T, wal bool) (*PostgresClusterReconciler, client.Client, *v1.Postgres) {
	t.Helper()
	r, c, pg, _, _ := directOwnershipFixture(t, wal)
	for range 2 {
		s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
		_, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)})
		requireNoError(t, err)
	}
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(pg), pg))
	return r, c, pg
}

func TestDirectPostgresRejectsIAMRecreation(t *testing.T) {
	for _, kind := range []string{"IAMServiceAccount", "IAMPolicyMember"} {
		t.Run(kind, func(t *testing.T) {
			r, c, pg := managedOwnershipFixture(t, true)
			r.Config.ResyncIAMPermissions = true
			children, err := r.ownedChildren(t.Context(), c, pg)
			requireNoError(t, err)
			var changed client.Object
			for _, child := range children {
				switch obj := child.(type) {
				case *iamcnrm.IAMServiceAccount:
					if kind == "IAMServiceAccount" {
						obj.Spec.DisplayName = "different display name"
						changed = obj
					}
				case *iamcnrm.IAMPolicyMember:
					if kind == "IAMPolicyMember" && changed == nil {
						obj.Spec.Member = "serviceAccount:changed@example.invalid"
						changed = obj
					}
				}
			}
			if changed == nil {
				t.Fatal("fixture did not find IAM resource")
			}
			requireNoError(t, c.Update(t.Context(), changed))
			p, _, err := r.Prepare(t.Context(), c, pg)
			requireNoError(t, err)
			related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
			for _, child := range p.Children {
				related.Insert(child)
			}
			if _, _, err := r.Update(pg, p, related); err == nil {
				t.Fatal("planned destructive IAM recreation despite checkpointed identity")
			}
			current := changed.DeepCopyObject().(client.Object)
			requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(current), current))
			assertSameFunctionalResource(t, changed, current)
		})
	}
}

func TestOwnershipInventoryFailurePreservesStoredUIDGuards(t *testing.T) {
	r, c, pg := managedOwnershipFixture(t, false)
	before := pg.Status.Ownership.DeepCopy()
	newChild := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "new-deleting-child", Namespace: pg.Namespace, UID: "new-deleting-child-uid", Finalizers: []string{"test/hold"}}}
	requireNoError(t, controllerutil.SetControllerReference(pg, newChild, scheme.Scheme))
	requireNoError(t, c.Create(t.Context(), newChild))
	requireNoError(t, c.Delete(t.Context(), newChild))
	s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
	if _, err := s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(pg)}); err == nil {
		t.Fatal("accepted deleting, uncheckpointed child")
	}
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(pg), pg))
	if diff := cmp.Diff(before, pg.Status.Ownership); diff != "" {
		t.Fatalf("failed Prepare changed persisted UID guards (-before +after): %s", diff)
	}
}

func TestCheckpointedPhysicalWritesRejectTargetChanges(t *testing.T) {
	for _, kind := range []string{"Cluster", "DatabaseRole", "Pooler", "StorageBucket", "IAMServiceAccount", "IAMPolicyMember"} {
		for _, timing := range []string{"before planning", "before execution"} {
			for _, replace := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/replaced=%t", kind, timing, replace), func(t *testing.T) {
					r, c, pg := managedOwnershipFixture(t, true)
					p, _, err := r.Prepare(t.Context(), c, pg)
					requireNoError(t, err)
					requireNoError(t, c.Status().Update(t.Context(), pg))
					var target client.Object
					for _, child := range p.Children {
						if child.GetObjectKind().GroupVersionKind().Kind == kind {
							target = child
							break
						}
					}
					if target == nil {
						t.Fatal("missing fixture target")
					}
					change := func() {
						requireNoError(t, c.Delete(t.Context(), target))
						if replace {
							replacement := target.DeepCopyObject().(client.Object)
							replacement.SetResourceVersion("")
							replacement.SetUID("review-replacement-uid")
							requireNoError(t, c.Create(t.Context(), replacement))
						}
					}
					if timing == "before planning" {
						change()
					}
					children, err := r.ownedChildren(t.Context(), c, pg)
					requireNoError(t, err)
					related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
					for _, child := range children {
						related.Insert(child)
					}
					planned, _, err := r.Update(pg, p, related)
					if err != nil {
						return
					} // Rejecting the changed target during planning is safe too.
					var write action.Action
					for _, a := range planned {
						if reflect.TypeOf(a.GetObject()) == reflect.TypeOf(target) && client.ObjectKeyFromObject(a.GetObject()) == client.ObjectKeyFromObject(target) {
							write = a
							break
						}
					}
					if write == nil {
						t.Fatal("no planned action for checkpointed target")
					}
					if timing == "before execution" {
						change()
					}
					if err := write.Do(t.Context(), c, scheme.Scheme, ownership.NewOwnerManager(r.Name()+"/owner")); err == nil {
						t.Fatal("executed write against missing/replaced checkpointed target")
					}
					current := target.DeepCopyObject().(client.Object)
					err = c.Get(t.Context(), client.ObjectKeyFromObject(current), current)
					if !replace {
						if !apierrors.IsNotFound(err) {
							t.Fatalf("missing resource was recreated: %v", err)
						}
					} else {
						requireNoError(t, err)
						if current.GetUID() != "review-replacement-uid" {
							t.Fatal("replacement identity changed")
						}
						original := target.DeepCopyObject().(client.Object)
						original.SetUID(current.GetUID())
						assertSameFunctionalResource(t, original, current)
					}
				})
			}
		}
	}
}

func TestDeletingPostgresWithdrawsBindingWithoutResolvingCluster(t *testing.T) {
	for _, clusterState := range []string{"already absent", "awaiting finalizer"} {
		t.Run(clusterState, func(t *testing.T) {
			_, c, pg := managedOwnershipFixture(t, false)
			binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "deleting-parent-binding", Namespace: pg.Namespace, UID: "deleting-binding-uid"}, Spec: v1.PostgresBindingSpec{Postgres: pg.Name, SecretName: "deleted-parent-connection", Consumer: v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "consumer", Type: v1.PostgresBindingWorkloadTypeApplication}}, Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead}}}
			binding.GetStatus().SetCondition(metav1.Condition{Type: readyCondition, Status: metav1.ConditionTrue, Reason: "CredentialsReady"})
			requireNoError(t, c.Create(t.Context(), binding))
			role, err := rcbinding.CreateDatabaseRole(scheme.Scheme, binding, v1.PostgresPhysicalName(pg.Name), v1.PostgresBindingCredentialRead)
			requireNoError(t, err)
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: binding.Spec.SecretName, Namespace: pg.Namespace}, Data: map[string][]byte{"keep": []byte("old-connection")}}
			requireNoError(t, controllerutil.SetControllerReference(binding, secret, scheme.Scheme))
			netpol, err := rcbinding.CreateNetworkPolicy(scheme.Scheme, binding, v1.PostgresPhysicalName(pg.Name))
			requireNoError(t, err)
			for _, obj := range []client.Object{role, secret, netpol} {
				ownership.NewOwnerManager("postgresbinding.nais.io/owner").AddOwnerAnnotation(obj, binding)
				requireNoError(t, c.Create(t.Context(), obj))
			}
			cluster := &cnpgv1.Cluster{}
			requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: pg.Status.ClusterName}, cluster))
			if clusterState == "awaiting finalizer" {
				cluster.Finalizers = []string{"test/hold"}
				requireNoError(t, c.Update(t.Context(), cluster))
			}
			requireNoError(t, c.Delete(t.Context(), pg))
			requireNoError(t, c.Delete(t.Context(), cluster))
			r := &PostgresBindingReconciler{Scheme: scheme.Scheme, Recorder: recorder}
			s := synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder)
			_, err = s.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(binding)})
			requireNoError(t, err)
			requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(binding), binding))
			for _, condition := range binding.GetStatus().GetConditions() {
				if condition.Type == readyCondition && condition.Status != metav1.ConditionFalse {
					t.Fatal("binding still reports Ready during explicit parent deletion")
				}
			}
			for _, obj := range []client.Object{secret, netpol} {
				if err := c.Get(t.Context(), client.ObjectKeyFromObject(obj), obj); !apierrors.IsNotFound(err) {
					t.Fatalf("did not withdraw %T: %v", obj, err)
				}
			}
			requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(role), role))
		})
	}
}
