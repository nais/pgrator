package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/initscheme"
	rcstorage "github.com/nais/pgrator/internal/resourcecreator/storage"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestRecoveryFailureStatusChangesTriggerReconcile(t *testing.T) {
	filter := (&PostgresBranchReconciler{Config: &config.Config{}}).OwnedTypes()[0].AdditionalPredicate
	for _, tc := range []struct {
		name   string
		change func(*cnpgv1.Cluster)
		want   bool
	}{
		{name: "unchanged"},
		{name: "phase", want: true, change: func(c *cnpgv1.Cluster) { c.Status.Phase = cnpgv1.PhaseUnrecoverable }},
		{name: "phase reason", want: true, change: func(c *cnpgv1.Cluster) { c.Status.PhaseReason = "recovery job failed" }},
		{name: "ready reason", want: true, change: func(c *cnpgv1.Cluster) { c.Status.Conditions[0].Reason = "ClusterUnrecoverable" }},
		{name: "ready message", want: true, change: func(c *cnpgv1.Cluster) { c.Status.Conditions[0].Message = "recovery job failed" }},
		{name: "initialized", want: true, change: func(c *cnpgv1.Cluster) {
			c.Status.Conditions = append(c.Status.Conditions, metav1.Condition{Type: string(cnpgv1.ConditionInitialized), Status: metav1.ConditionFalse})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := &cnpgv1.Cluster{Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{
				Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionFalse,
			}}}}
			updated := old.DeepCopy()
			if tc.change != nil {
				tc.change(updated)
			}
			if got := filter.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}); got != tc.want {
				t.Errorf("Update() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestPrepareClassifiesOnlyUnrecoverablePendingRecovery(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(*v1.PostgresBranch, *cnpgv1.Cluster)
		wantFail bool
	}{
		{name: "unrecoverable recovery with failed job", wantFail: true},
		{name: "unrecoverable recovery without job evidence"},
		{name: "unrecoverable without phase reason"},
		{name: "ordinary readiness failure", change: func(_ *v1.PostgresBranch, c *cnpgv1.Cluster) { c.Status.Phase = cnpgv1.PhaseFirstPrimary }},
		{name: "initialized before first backup", change: func(_ *v1.PostgresBranch, c *cnpgv1.Cluster) { c.Status.Conditions[0].Status = metav1.ConditionTrue }},
		{name: "unknown initialization", change: func(_ *v1.PostgresBranch, c *cnpgv1.Cluster) { c.Status.Conditions[0].Status = metav1.ConditionUnknown }},
		{name: "missing initialization", change: func(_ *v1.PostgresBranch, c *cnpgv1.Cluster) { c.Status.Conditions = nil }},
		{name: "not recovery bootstrap", change: func(_ *v1.PostgresBranch, c *cnpgv1.Cluster) {
			c.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{InitDB: &cnpgv1.BootstrapInitDB{}}
		}},
		{name: "completed recovery outage", change: func(b *v1.PostgresBranch, c *cnpgv1.Cluster) {
			b.GetStatus().SetCondition(metav1.Condition{Type: recoveryCompletedCondition, Status: metav1.ConditionTrue, Reason: "Completed", Message: string(c.UID)})
		}},
		{name: "successful recovery", change: func(_ *v1.PostgresBranch, c *cnpgv1.Cluster) {
			c.Status.Phase = cnpgv1.PhaseHealthy
			c.Status.Conditions[0].Status = metav1.ConditionTrue
			c.Status.Conditions = append(c.Status.Conditions, metav1.Condition{
				Type: "LastBackupSucceeded", Status: metav1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(c.CreationTimestamp.AddDate(0, 0, 1)),
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, branch, cluster, k8sClient := newRecoveryFailureTest(t)
			if tc.change != nil {
				tc.change(branch, cluster)
			}
			if tc.wantFail {
				requireNoError(t, k8sClient.Create(context.Background(), failedRecoveryJob(t, r.Scheme, cluster)))
			}
			status := cluster.Status
			requireNoError(t, k8sClient.Update(context.Background(), cluster))
			cluster.Status = status
			requireNoError(t, k8sClient.Status().Update(context.Background(), cluster))
			originalSpec := branch.Spec
			prepared, _, err := r.Prepare(context.Background(), k8sClient, branch)
			if got := errors.Is(err, reconcile.TerminalError(nil)); got != tc.wantFail {
				t.Fatalf("Prepare() error = %v, want terminal = %t", err, tc.wantFail)
			}
			if !tc.wantFail && err != nil {
				t.Fatal(err)
			}
			if got := meta.IsStatusConditionTrue(branch.GetStatus().GetConditions(), recoveryFailedCondition); got != tc.wantFail {
				t.Fatalf("RecoveryFailed = %t, want %t", got, tc.wantFail)
			}
			if !equality.Semantic.DeepEqual(branch.Spec, originalSpec) {
				t.Fatal("Prepare changed recovery provenance")
			}
			if tc.wantFail {
				failure := meta.FindStatusCondition(branch.Status.Conditions, recoveryFailedCondition)
				if failure.Reason != "Unrecoverable" || failure.ObservedGeneration != branch.Generation {
					t.Fatalf("unexpected failure condition: %#v", failure)
				}
				for _, detail := range []string{cluster.Name, string(cluster.UID), branch.Spec.Bootstrap.Recovery.TargetTime.Format("2006-01-02T15:04:05Z07:00"), cluster.Status.PhaseReason} {
					if !strings.Contains(failure.Message, detail) {
						t.Errorf("failure message %q missing %q", failure.Message, detail)
					}
				}
				if !meta.IsStatusConditionFalse(branch.Status.Conditions, readyCondition) {
					t.Fatal("failed recovery is not marked unready")
				}
				actions, _, updateErr := r.Update(branch, prepared, relatedobjectsmap.NewRelatedObjectsMap(r.Scheme))
				if !errors.Is(updateErr, reconcile.TerminalError(nil)) || len(actions) != 0 {
					t.Fatalf("Update planned work for terminal recovery: %v, %v", actions, updateErr)
				}
			}
			if tc.name == "successful recovery" && completedRecoveryClusterUID(branch) != string(cluster.UID) {
				t.Fatal("successful recovery was not checkpointed")
			}
		})
	}
}

func TestFailedRecoveryPersistsAndPreservesResources(t *testing.T) {
	ctx := context.Background()
	r, branch, cluster, k8sClient := newRecoveryFailureTest(t)
	cluster.Status.Conditions[0].Status = metav1.ConditionTrue
	requireNoError(t, k8sClient.Status().Update(ctx, cluster))
	job := failedRecoveryJob(t, r.Scheme, cluster)
	requireNoError(t, k8sClient.Create(ctx, job))
	s := synchronizer.NewSynchronizer(k8sClient, r.Scheme, r, recorder)
	s.GetOwnerManager().AddOwnerAnnotation(cluster, branch)
	requireNoError(t, k8sClient.Update(ctx, cluster))
	networkPolicy := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "diagnostic-policy", Namespace: branch.Namespace}}
	s.GetOwnerManager().AddOwnerAnnotation(networkPolicy, branch)
	requireNoError(t, k8sClient.Create(ctx, networkPolicy))

	assertStopped := func() *v1.PostgresBranch {
		t.Helper()
		result, err := s.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(branch)})
		if !errors.Is(err, reconcile.TerminalError(nil)) || !result.IsZero() {
			t.Fatalf("Reconcile() = %v, %v, want terminal failure without requeue", result, err)
		}
		stored := &v1.PostgresBranch{}
		requireNoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(branch), stored))
		if stored.Status.ReconcilePhase != "Failed" || !meta.IsStatusConditionTrue(stored.Status.Conditions, recoveryFailedCondition) {
			t.Fatalf("terminal recovery status not persisted: %#v", stored.Status)
		}
		if !equality.Semantic.DeepEqual(branch.Spec, stored.Spec) {
			t.Fatal("recovery target or source changed")
		}
		return stored
	}

	first := assertStopped()
	failure := *meta.FindStatusCondition(first.Status.Conditions, recoveryFailedCondition)
	for range 2 {
		repeated := assertStopped()
		if !equality.Semantic.DeepEqual(failure, *meta.FindStatusCondition(repeated.Status.Conditions, recoveryFailedCondition)) {
			t.Fatal("durable failure condition changed")
		}
	}
	storedCluster := &cnpgv1.Cluster{}
	requireNoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), storedCluster))
	if !equality.Semantic.DeepEqual(cluster, storedCluster) {
		t.Fatal("failed Cluster was changed")
	}
	storedPolicy := &networkingv1.NetworkPolicy{}
	requireNoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(networkPolicy), storedPolicy))
	if !equality.Semantic.DeepEqual(networkPolicy, storedPolicy) {
		t.Fatal("supporting resource was changed")
	}
	storedJob := &batchv1.Job{}
	requireNoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(job), storedJob))
	if !equality.Semantic.DeepEqual(job, storedJob) {
		t.Fatal("CNPG recovery job was changed")
	}

	source := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "primary"), Namespace: branch.Namespace}}
	requireNoError(t, k8sClient.Delete(ctx, source))
	assertStopped()
	requireNoError(t, k8sClient.Delete(ctx, cluster))
	assertStopped()
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &cnpgv1.Cluster{}); !apierrors.IsNotFound(err) {
		t.Fatalf("failed Cluster was recreated: %v", err)
	}
	replacement := cluster.DeepCopy()
	replacement.ResourceVersion = ""
	replacement.UID = "replacement-uid"
	requireNoError(t, k8sClient.Create(ctx, replacement))
	assertStopped()
	storedReplacement := &cnpgv1.Cluster{}
	requireNoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(replacement), storedReplacement))
	if !equality.Semantic.DeepEqual(replacement, storedReplacement) {
		t.Fatal("replacement Cluster was changed")
	}

	requireNoError(t, k8sClient.Create(ctx, &v1.PostgresBranch{
		ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "main"), Namespace: branch.Namespace},
		Spec:       v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"},
	}))
	requireNoError(t, k8sClient.Delete(ctx, first))
	result, err := s.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(branch)})
	requireNoError(t, err)
	if !result.IsZero() {
		t.Fatalf("failed branch deletion was blocked: %v", result)
	}
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(branch), &v1.PostgresBranch{}); !apierrors.IsNotFound(err) {
		t.Fatalf("failed branch was not deleted: %v", err)
	}
	for _, obj := range []client.Object{storedReplacement, storedPolicy} {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); !apierrors.IsNotFound(err) {
			t.Fatalf("owned diagnostic resource was not cleaned up: %T: %v", obj, err)
		}
	}
}

func TestRecoveryFailureWithInitializedCluster(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(*v1.PostgresBranch, *cnpgv1.Cluster, *batchv1.Job)
		wantFail bool
	}{
		{name: "failed recovery with Initialized True", wantFail: true},
		{name: "failed recovery with missing Initialized", wantFail: true, change: func(_ *v1.PostgresBranch, c *cnpgv1.Cluster, _ *batchv1.Job) { c.Status.Conditions = nil }},
		{name: "failed recovery with unknown Initialized", wantFail: true, change: func(_ *v1.PostgresBranch, c *cnpgv1.Cluster, _ *batchv1.Job) {
			c.Status.Conditions[0].Status = metav1.ConditionUnknown
		}},
		{name: "retrying job", change: func(_ *v1.PostgresBranch, _ *cnpgv1.Cluster, j *batchv1.Job) {
			j.Status.Conditions = nil
			j.Status.Failed = 3
		}},
		{name: "successful job", change: func(_ *v1.PostgresBranch, _ *cnpgv1.Cluster, j *batchv1.Job) {
			j.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
			j.Status.Succeeded = 1
		}},
		{name: "failed replica join", change: func(_ *v1.PostgresBranch, _ *cnpgv1.Cluster, j *batchv1.Job) { j.Labels[recoveryJobRoleLabel] = "join" }},
		{name: "job without controller", change: func(_ *v1.PostgresBranch, _ *cnpgv1.Cluster, j *batchv1.Job) { j.OwnerReferences = nil }},
		{name: "job from replaced cluster", change: func(_ *v1.PostgresBranch, _ *cnpgv1.Cluster, j *batchv1.Job) {
			j.OwnerReferences[0].UID = "old-cluster-uid"
		}},
		{name: "job in another namespace", change: func(_ *v1.PostgresBranch, _ *cnpgv1.Cluster, j *batchv1.Job) { j.Namespace = "other-team" }},
		{name: "CNPG not unrecoverable", change: func(_ *v1.PostgresBranch, c *cnpgv1.Cluster, _ *batchv1.Job) {
			c.Status.Phase = cnpgv1.PhaseFirstPrimary
		}},
		{name: "completed recovery", change: func(b *v1.PostgresBranch, c *cnpgv1.Cluster, _ *batchv1.Job) {
			b.GetStatus().SetCondition(metav1.Condition{Type: recoveryCompletedCondition, Status: metav1.ConditionTrue, Reason: "Completed", Message: string(c.UID)})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, branch, cluster, k8sClient := newRecoveryFailureTest(t)
			cluster.Status.Conditions[0].Status = metav1.ConditionTrue
			job := failedRecoveryJob(t, r.Scheme, cluster)
			if tc.change != nil {
				tc.change(branch, cluster, job)
			}
			ctx := context.Background()
			requireNoError(t, k8sClient.Status().Update(ctx, cluster))
			requireNoError(t, k8sClient.Create(ctx, job))
			_, _, err := r.Prepare(ctx, k8sClient, branch)
			if got := errors.Is(err, reconcile.TerminalError(nil)); got != tc.wantFail {
				t.Fatalf("Prepare error = %v, want terminal = %t", err, tc.wantFail)
			}
			if err != nil && !tc.wantFail {
				t.Fatal(err)
			}
		})
	}
}

func TestRecoveryJobChangesEnqueueBranch(t *testing.T) {
	r, branch, cluster, k8sClient := newRecoveryFailureTest(t)
	job := failedRecoveryJob(t, r.Scheme, cluster)
	for _, tc := range []struct {
		name   string
		change func(*batchv1.Job)
		want   bool
	}{
		{name: "current recovery job", want: true},
		{name: "replica job", change: func(j *batchv1.Job) { j.Labels[recoveryJobRoleLabel] = "join" }},
		{name: "previous cluster", change: func(j *batchv1.Job) { j.OwnerReferences[0].UID = "old-uid" }},
		{name: "cluster label mismatch", change: func(j *batchv1.Job) { j.Labels[recoveryJobClusterLabel] = "other-cluster" }},
		{name: "ownerless", change: func(j *batchv1.Job) { j.OwnerReferences = nil }},
		{name: "missing cluster", change: func(j *batchv1.Job) { j.OwnerReferences[0].Name = "missing" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := job.DeepCopy()
			if tc.change != nil {
				tc.change(candidate)
			}
			requests, err := r.branchesForRecoveryJob(context.Background(), k8sClient, candidate)
			requireNoError(t, err)
			if tc.want {
				if len(requests) != 1 || requests[0].NamespacedName != client.ObjectKeyFromObject(branch) {
					t.Fatalf("unexpected requests: %v", requests)
				}
			} else if len(requests) != 0 {
				t.Fatalf("unrelated job enqueued branch: %v", requests)
			}
		})
	}
	watches := r.RelationshipWatches()
	for _, watch := range watches {
		if _, ok := watch.Type.(*batchv1.Job); !ok {
			continue
		}
		pending := job.DeepCopy()
		pending.Status.Conditions = nil
		if !watch.Predicate.Update(event.UpdateEvent{ObjectOld: pending, ObjectNew: job}) {
			t.Fatal("terminal job failure did not trigger reconciliation")
		}
		if watch.Predicate.Update(event.UpdateEvent{ObjectOld: job, ObjectNew: job.DeepCopy()}) {
			t.Fatal("unchanged job triggered reconciliation")
		}
		if !watch.Predicate.Create(event.CreateEvent{Object: job}) {
			t.Fatal("existing failed job not observed at startup")
		}
		return
	}
	t.Fatal("recovery jobs are not watched")
}

func failedRecoveryJob(t *testing.T, scheme *runtime.Scheme, cluster *cnpgv1.Cluster) *batchv1.Job {
	t.Helper()
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: cluster.Name + "-1-full-recovery", Namespace: cluster.Namespace,
			Labels: map[string]string{
				recoveryJobRoleLabel: recoveryJobRole, recoveryJobClusterLabel: cluster.Name,
			},
		},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobFailed, Status: corev1.ConditionTrue,
			Reason: "BackoffLimitExceeded",
		}}},
	}
	requireNoError(t, controllerutil.SetControllerReference(cluster, job, scheme))
	return job
}

func TestSuccessfulRecoveryRemainsSuccessfulDuringLaterOutage(t *testing.T) {
	ctx := context.Background()
	r, branch, cluster, k8sClient := newRecoveryFailureTest(t)
	cluster.Status.Phase = cnpgv1.PhaseHealthy
	cluster.Status.Conditions[0].Status = metav1.ConditionTrue
	cluster.Status.Conditions[1].Status = metav1.ConditionTrue
	cluster.Status.Conditions = append(cluster.Status.Conditions, metav1.Condition{
		Type: "LastBackupSucceeded", Status: metav1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(cluster.CreationTimestamp.AddDate(0, 0, 1)),
	})
	requireNoError(t, k8sClient.Status().Update(ctx, cluster))
	s := synchronizer.NewSynchronizer(k8sClient, r.Scheme, r, recorder)
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(branch)}
	_, err := s.Reconcile(ctx, request)
	requireNoError(t, err)
	storedBranch := &v1.PostgresBranch{}
	requireNoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(branch), storedBranch))
	if completedRecoveryClusterUID(storedBranch) != string(cluster.UID) {
		t.Fatal("successful recovery milestone was not persisted")
	}
	requireNoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), cluster))
	if cluster.Spec.Bootstrap == nil || cluster.Spec.Bootstrap.Recovery != nil || len(cluster.Spec.ExternalClusters) != 0 {
		t.Fatal("successful recovery was not detached from the source archive")
	}
	if !equality.Semantic.DeepEqual(branch.Spec, storedBranch.Spec) {
		t.Fatal("successful recovery changed bootstrap provenance")
	}
	cluster.Status.Phase = cnpgv1.PhaseUnrecoverable
	cluster.Status.PhaseReason = "No pods are active"
	cluster.Status.Conditions[1].Status = metav1.ConditionFalse
	requireNoError(t, k8sClient.Status().Update(ctx, cluster))
	_, err = s.Reconcile(ctx, request)
	requireNoError(t, err)
	requireNoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(branch), storedBranch))
	if meta.IsStatusConditionTrue(storedBranch.Status.Conditions, recoveryFailedCondition) {
		t.Fatal("later outage was classified as recovery failure")
	}
	if completedRecoveryClusterUID(storedBranch) != string(cluster.UID) {
		t.Fatal("later outage cleared the successful recovery milestone")
	}
}

func newRecoveryFailureTest(t *testing.T) (*PostgresBranchReconciler, *v1.PostgresBranch, *cnpgv1.Cluster, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	branch := recoveryInstance("primary")
	branch.Generation = 1
	branch.Finalizers = []string{"postgresbranch.nais.io"}
	cluster := ownedRecoveryCluster(t, scheme, branch, false)
	cluster.Status.Conditions[0].Status = metav1.ConditionFalse
	cluster.Status.Phase = cnpgv1.PhaseUnrecoverable
	cluster.Status.PhaseReason = "Instance creation failed for recovery job; check the job logs"
	source := &v1.PostgresBranch{
		ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "primary"), Namespace: "team"},
		Spec:       v1.PostgresBranchSpec{Postgres: "orders", BranchName: "primary"},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&v1.PostgresBranch{}, &cnpgv1.Cluster{}).
		WithIndex(&v1.PostgresBranch{}, postgresBranchRecoverySourceIndex, recoverySourceBranchIndex).
		WithObjects(branch, cluster, source,
			&v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresSpec{MajorVersion: "18"}},
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team", Labels: map[string]string{ProjectIDLabel: "team-gcp-project"}}},
			&barmanv1.ObjectStore{ObjectMeta: metav1.ObjectMeta{Name: "source-archive", Namespace: "team", Labels: map[string]string{rcstorage.OwnerNameLabel: source.Name}}},
			&storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: "source-archive", Namespace: "team"}},
		).Build()
	r := &PostgresBranchReconciler{
		Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal-bucket-prefix"}},
		Scheme: scheme, Recorder: recorder,
	}
	return r, branch, cluster, k8sClient
}
