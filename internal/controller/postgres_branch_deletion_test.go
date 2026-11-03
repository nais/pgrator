package controller

import (
	"fmt"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/initscheme"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	gkenetworking "github.com/nais/pgrator/internal/thirdparty/google/networking/v1alpha3"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestDeletedInactiveMainIsNotRecreated(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", UID: "postgres-uid"}, Status: &v1.PostgresStatus{ActiveBranch: "restored"}}
	main := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "main"), Namespace: "team", UID: "main-uid"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"}}
	restored := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "restored"), Namespace: "team", UID: "restored-uid"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "restored"}}
	r := &PostgresReconciler{Scheme: scheme, Recorder: recorder}
	reader := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(postgres).WithObjects(postgres, main, restored).Build()
	sync := synchronizer.NewSynchronizer(reader, scheme, r, recorder)
	for _, branch := range []*v1.PostgresBranch{main, restored} {
		sync.GetOwnerManager().AddOwnerAnnotation(branch, postgres)
		requireNoError(t, reader.Update(t.Context(), branch))
	}
	requireNoError(t, reader.Delete(t.Context(), main))
	for range 2 {
		_, err := sync.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(postgres)})
		requireNoError(t, err)
		if err := reader.Get(t.Context(), client.ObjectKeyFromObject(main), &v1.PostgresBranch{}); !apierrors.IsNotFound(err) {
			t.Fatalf("deleted inactive main was recreated: %v", err)
		}
		requireNoError(t, reader.Get(t.Context(), client.ObjectKeyFromObject(restored), &v1.PostgresBranch{}))
	}
}

func TestPostgresDeletionWaitsForFailedBranch(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{
		Name: "orders", Namespace: "team", UID: "postgres-uid", Finalizers: []string{"postgres.nais.io"},
	}, Spec: v1.PostgresSpec{ActiveBranch: "failed"}}
	main := &v1.PostgresBranch{
		ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "main"), Namespace: "team", UID: "main-uid", Finalizers: []string{"postgresbranch.nais.io"}},
		Spec:       v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"},
	}
	failed := &v1.PostgresBranch{
		ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "failed"), Namespace: "team", UID: "failed-uid", Finalizers: []string{"postgresbranch.nais.io"}},
		Spec:       v1.PostgresBranchSpec{Postgres: "orders", BranchName: "failed"},
	}
	failed.GetStatus().SetCondition(metav1.Condition{Type: recoveryFailedCondition, Status: metav1.ConditionTrue, Reason: "Unrecoverable"})
	for _, branch := range []*v1.PostgresBranch{main, failed} {
		requireNoError(t, controllerutil.SetControllerReference(postgres, branch, scheme))
	}
	reader := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&v1.Postgres{}, &v1.PostgresBranch{}).
		WithObjects(postgres, main, failed).Build()
	r := &PostgresReconciler{Scheme: scheme, Recorder: recorder}
	sync := synchronizer.NewSynchronizer(reader, scheme, r, recorder)
	// An owner reference is sufficient even without the ownership annotation.
	sync.GetOwnerManager().AddOwnerAnnotation(main, postgres)
	requireNoError(t, reader.Update(t.Context(), main))
	requireNoError(t, reader.Delete(t.Context(), postgres))
	result, err := sync.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(postgres)})
	requireNoError(t, err)
	if err := reader.Get(t.Context(), client.ObjectKeyFromObject(postgres), &v1.Postgres{}); err != nil {
		t.Fatalf("Postgres disappeared before its branches were cleaned up: %v", err)
	}
	if result.IsZero() {
		t.Fatal("Postgres deletion did not schedule another cleanup pass")
	}
	for _, branch := range []*v1.PostgresBranch{main, failed} {
		stored := &v1.PostgresBranch{}
		requireNoError(t, reader.Get(t.Context(), client.ObjectKeyFromObject(branch), stored))
		if stored.DeletionTimestamp.IsZero() {
			t.Errorf("branch %q was not scheduled for deletion", branch.Name)
		}
	}
	branchReconciler := &PostgresBranchReconciler{Config: &config.Config{}, Scheme: scheme, Recorder: recorder}
	branchSync := synchronizer.NewSynchronizer(reader, scheme, branchReconciler, recorder)
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name: "failed-cluster", Namespace: "team", Finalizers: []string{"cnpg.io/cleanup"},
	}}
	// Controller ownership alone must also be sufficient for cleanup.
	requireNoError(t, controllerutil.SetControllerReference(failed, cluster, scheme))
	requireNoError(t, reader.Create(t.Context(), cluster))
	parentRequest := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(postgres)}
	for range 2 {
		for _, branch := range []*v1.PostgresBranch{main, failed} {
			_, err := branchSync.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(branch)})
			requireNoError(t, err)
		}
		_, err := sync.Reconcile(t.Context(), parentRequest)
		requireNoError(t, err)
		requireNoError(t, reader.Get(t.Context(), client.ObjectKeyFromObject(postgres), &v1.Postgres{}))
		requireNoError(t, reader.Get(t.Context(), client.ObjectKeyFromObject(failed), &v1.PostgresBranch{}))
	}
	requireNoError(t, reader.Get(t.Context(), client.ObjectKeyFromObject(cluster), cluster))
	if cluster.DeletionTimestamp.IsZero() {
		t.Fatal("failed branch's Cluster was not scheduled for deletion")
	}
	cluster.Finalizers = nil
	requireNoError(t, reader.Update(t.Context(), cluster))
	_, err = branchSync.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(failed)})
	requireNoError(t, err)
	_, err = sync.Reconcile(t.Context(), parentRequest)
	requireNoError(t, err)
	for _, obj := range []client.Object{postgres, main, failed, cluster} {
		if err := reader.Get(t.Context(), client.ObjectKeyFromObject(obj), obj); !apierrors.IsNotFound(err) {
			t.Errorf("resource %T %q remains after cleanup: %v", obj, obj.GetName(), err)
		}
	}
}

func TestPostgresDeletionLeavesUnrelatedBranches(t *testing.T) {
	for _, tc := range []struct {
		name      string
		namespace string
		postgres  string
	}{
		{name: "another Postgres", namespace: "team", postgres: "customers"},
		{name: "another namespace", namespace: "other-team", postgres: "orders"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", Finalizers: []string{"postgres.nais.io"}}}
			branch := &v1.PostgresBranch{
				ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName(tc.postgres, "main"), Namespace: tc.namespace},
				Spec:       v1.PostgresBranchSpec{Postgres: tc.postgres, BranchName: "main"},
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1.Postgres{}).WithObjects(postgres, branch).Build()
			sync := synchronizer.NewSynchronizer(reader, scheme, &PostgresReconciler{Scheme: scheme, Recorder: recorder}, recorder)
			requireNoError(t, reader.Delete(t.Context(), postgres))
			result, err := sync.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(postgres)})
			requireNoError(t, err)
			if !result.IsZero() {
				t.Fatalf("unrelated branch blocked deletion: %v", result)
			}
			requireNoError(t, reader.Get(t.Context(), client.ObjectKeyFromObject(branch), &v1.PostgresBranch{}))
			if err := reader.Get(t.Context(), client.ObjectKeyFromObject(postgres), &v1.Postgres{}); !apierrors.IsNotFound(err) {
				t.Fatalf("Postgres remains after cleanup: %v", err)
			}
		})
	}
}

func TestPostgresDeletionRejectsAmbiguousBranchOwnership(t *testing.T) {
	for _, state := range []string{"ownerless", "annotation only", "previous Postgres UID", "invalid identity", "conflicting spec"} {
		t.Run(state, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", UID: "current-uid", Finalizers: []string{"postgres.nais.io"}}}
			branch := &v1.PostgresBranch{
				ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "restore"), Namespace: "team", UID: "branch-uid"},
				Spec:       v1.PostgresBranchSpec{Postgres: "orders", BranchName: "restore"},
			}
			if state != "ownerless" && state != "annotation only" {
				requireNoError(t, controllerutil.SetControllerReference(postgres, branch, scheme))
			}
			switch state {
			case "previous Postgres UID":
				branch.OwnerReferences[0].UID = "previous-uid"
			case "invalid identity":
				branch.Name = "unexpected-name"
			case "conflicting spec":
				branch.Spec.Postgres = "customers"
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&v1.Postgres{}).WithObjects(postgres, branch).Build()
			s := synchronizer.NewSynchronizer(c, scheme, &PostgresReconciler{Scheme: scheme, Recorder: recorder}, recorder)
			if state == "annotation only" {
				s.GetOwnerManager().AddOwnerAnnotation(branch, postgres)
				requireNoError(t, c.Update(t.Context(), branch))
			}
			requireNoError(t, c.Delete(t.Context(), postgres))
			_, err := s.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(postgres)})
			if err == nil {
				t.Fatal("ambiguous branch ownership did not block deletion")
			}
			requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(postgres), postgres))
			if !controllerutil.ContainsFinalizer(postgres, "postgres.nais.io") {
				t.Fatal("Postgres finalizer was removed with ambiguous branch ownership")
			}
			requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(branch), branch))
			if !branch.DeletionTimestamp.IsZero() {
				t.Fatal("branch with ambiguous ownership was scheduled for deletion")
			}
		})
	}
}

func TestPostgresBranchDeletionWaitsForOwnedResources(t *testing.T) {
	for _, child := range []client.Object{
		&cnpgv1.Cluster{},
		&cnpgv1.DatabaseRole{},
		&cnpgv1.Pooler{},
		&networkingv1.NetworkPolicy{},
		&cnpgv1.ScheduledBackup{},
		&barmanv1.ObjectStore{},
		&gkenetworking.FQDNNetworkPolicy{},
		&iamcnrm.IAMServiceAccount{},
		&iamcnrm.IAMPolicyMember{},
		&storagecnrm.StorageBucket{},
	} {
		t.Run(fmt.Sprintf("%T", child), func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			branch := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: "branch", Namespace: "team", UID: "branch-uid"}}
			child.SetName("child")
			child.SetNamespace("team")
			requireNoError(t, controllerutil.SetControllerReference(branch, child, scheme))
			related := relatedobjectsmap.NewRelatedObjectsMap(scheme)
			related.Insert(child)
			r := &PostgresBranchReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal"}}, Scheme: scheme, Recorder: recorder}
			actions, result, err := r.Delete(branch, PostgresBranchPreparedData{PostgresDeleting: true}, related)
			requireNoError(t, err)
			if result.IsZero() || len(actions) != 1 {
				t.Fatalf("Delete() = %d actions, %v; want cleanup and requeue", len(actions), result)
			}
		})
	}
}
