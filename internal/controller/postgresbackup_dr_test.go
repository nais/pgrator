package controller

import (
	"context"
	"testing"
	"time"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/resourcecreator/drbackup"
	rciam "github.com/nais/pgrator/internal/resourcecreator/iam"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func newDRTestReconciler() *PostgresBackupReconciler {
	return &PostgresBackupReconciler{
		Config:   &config.Config{CNPG: config.CNPG{DrBucketPrefix: "nais-dr-test", BackupWorkerImage: "worker:test"}},
		Recorder: recorder,
		Scheme:   scheme.Scheme,
	}
}

func drTestBackup(name string) *v1.PostgresBackup {
	return &v1.PostgresBackup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "myteam", UID: types.UID(name + "-uid")},
		Spec:       v1.PostgresBackupSpec{Postgres: "orders"},
		Status:     &v1.PostgresBackupStatus{Phase: v1.PostgresBackupPhaseRunning},
	}
}

func TestPostgresBackupRetentionChangesBeforeExpiryCheck(t *testing.T) {
	r := newDRTestReconciler()
	for _, tc := range []struct {
		name         string
		old, updated int32
		wantDelete   bool
	}{
		{"extension after old deadline", 7, 30, false},
		{"shortening to the past", 30, 7, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backup := drTestBackup("orders-backup")
			completed := metav1.NewTime(time.Now().Add(-10 * 24 * time.Hour))
			backup.Status = &v1.PostgresBackupStatus{
				Phase: v1.PostgresBackupPhaseCompleted, CompletedAt: &completed,
				ExpiresAt: new(metav1.NewTime(completed.Add(time.Duration(tc.old) * 24 * time.Hour))),
			}
			backup.Spec.RetentionDays = &tc.updated
			actions, result, err := r.Update(backup, PostgresBackupPreparedData{}, nil)
			requireNoError(t, err)
			if !backup.Status.ExpiresAt.Equal(new(metav1.NewTime(completed.Add(time.Duration(tc.updated) * 24 * time.Hour)))) {
				t.Fatalf("expiry doesn't reflect current retention: %v", backup.Status.ExpiresAt)
			}
			if tc.wantDelete {
				if len(actions) != 1 || actions[0].GetObject() != backup {
					t.Fatalf("expected backup to be deleted, got %v", actions)
				}
			} else if result.RequeueAfter <= 0 {
				t.Fatal("expected requeue for new expiry")
			}
		})
	}
}

func TestPostgresBackupDeletionWaitsForCopyAndRetriesCleanup(t *testing.T) {
	r := newDRTestReconciler()
	backup := drTestBackup("partial")
	backup.DeletionTimestamp = new(metav1.Now())
	backup.Status.Location = &v1.PostgresBackupLocation{BucketName: "existing-dr-bucket", Prefix: "partial"}
	copyJob, err := drbackup.CreateCopyJob(r.Scheme, backup, "worker:test", "src", "existing-dr-bucket", 0)
	requireNoError(t, err)
	copyJob.UID = "copy-uid"

	var options *client.DeleteOptions
	c := fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(copyJob).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			options = (&client.DeleteOptions{}).ApplyOptions(opts)
			return c.Delete(ctx, obj, opts...)
		},
	}).Build()
	om := ownership.NewOwnerManager(r.Name() + "/owner")
	actions, result, err := r.Delete(backup, PostgresBackupPreparedData{CopyJob: copyJob}, nil)
	requireNoError(t, err)
	requireTrue(t, !result.IsZero() && len(actions) == 1, "only stop copy Job before attempting prefix cleanup")
	requireNoError(t, actions[0].Do(t.Context(), c, r.Scheme, om))
	requireNotNil(t, options, "copy Job is deleted")
	requireEqual(t, *options.PropagationPolicy, metav1.DeletePropagationForeground, "stop worker Pods before cleanup")
	requireEqual(t, *options.Preconditions.UID, copyJob.UID, "only delete the observed Job")

	actions, result, err = r.Delete(backup, PostgresBackupPreparedData{}, nil)
	requireNoError(t, err)
	requireTrue(t, !result.IsZero(), "wait for prefix cleanup after the worker stops")
	var cleanup *batchv1.Job
	for _, a := range actions {
		if job, ok := a.GetObject().(*batchv1.Job); ok {
			cleanup = job
		}
	}
	requireNotNil(t, cleanup, "cleanup Job exists even though backup never completed")
	requireEqual(t, cleanup.Spec.Template.Spec.Containers[0].Args[1], "--dr-bucket=existing-dr-bucket", "cleanup uses persisted destination")
	cleanup.Status.Conditions = []batchv1.JobCondition{{
		Type: batchv1.JobFailed, Status: corev1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
	}}
	actions, result, err = r.Delete(backup, PostgresBackupPreparedData{CleanupJob: cleanup}, nil)
	requireNoError(t, err)
	requireTrue(t, !result.IsZero() && len(actions) == 1 && actions[0].GetObject() == cleanup, "failed cleanup is retired for retry without removing finalizer")
}

func TestPostgresBackupCopiesShareSourceGrant(t *testing.T) {
	r := newDRTestReconciler()
	first, second := drTestBackup("first"), drTestBackup("second")
	c := fake.NewClientBuilder().WithScheme(r.Scheme).Build()
	s := synchronizer.NewSynchronizer(c, r.Scheme, r, r.Recorder)
	om := ownership.NewOwnerManager(r.Name() + "/owner")
	const source = "orders-wal-bucket"
	create, _, err := r.sourceBucketPolicyAction(first, "team-project", source, nil)
	requireNoError(t, err)
	key := client.ObjectKeyFromObject(create.GetObject())
	s.UpdatingOwnership([]action.Action{create}, relatedobjectsmap.NewRelatedObjectsMap(r.Scheme))
	requireNoError(t, create.Do(t.Context(), c, r.Scheme, om))
	shared := &iamcnrm.IAMPolicyMember{}
	requireNoError(t, c.Get(t.Context(), key, shared))
	related := relatedobjectsmap.NewRelatedObjectsMap(r.Scheme)
	related.Insert(shared)
	claim, existing, err := r.sourceBucketPolicyAction(second, "team-project", source, related)
	requireNoError(t, err)
	requireNotNil(t, existing, "concurrent backup reuses the existing grant")
	requireNoError(t, claim.Do(t.Context(), c, r.Scheme, om))
	for _, backup := range []*v1.PostgresBackup{first, second} {
		prune, err := s.DetectUnreferenced(t.Context(), backup, nil)
		requireNoError(t, err)
		_, err = s.PerformActions(t.Context(), prune)
		requireNoError(t, err)
		err = c.Get(t.Context(), key, shared)
		if backup == first {
			requireNoError(t, err)
			requireTrue(t, !om.HasOwnerAnnotation(shared, first) && om.HasOwnerAnnotation(shared, second), "retire only the first copy's claim")
		} else if !apierrors.IsNotFound(err) {
			t.Fatalf("last copy leaves no source grant, got %v", err)
		}
	}
}

func TestPostgresBackupDoesNotAdoptOrDeleteUnmanagedDRBucket(t *testing.T) {
	r := newDRTestReconciler()
	backup := drTestBackup("last")
	backup.DeletionTimestamp = new(metav1.Now())
	backup.Finalizers = []string{r.Name()}
	bucket := drbackup.CreateDRStorageBucket(backup.Namespace, r.drBucketName(backup.Namespace), "")
	bucket.Labels = nil
	c := fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(bucket, backup).Build()
	a := &ensureSharedDRResource{backup: backup, desired: bucket.DeepCopy(), keepOwner: false}
	requireErrorContains(t, a.Do(t.Context(), c, r.Scheme, nil), "unmanaged shared DR resource")
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(bucket), &storagecnrm.StorageBucket{}))
}

func TestPostgresBackupNamespaceTeardownWaitsForBucketWithoutCreatingJobs(t *testing.T) {
	r := newDRTestReconciler()
	backup := drTestBackup("teardown")
	backup.DeletionTimestamp = new(metav1.Now())
	backup.Status.Location = &v1.PostgresBackupLocation{BucketName: "persisted-bucket", Prefix: backup.Name}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: backup.Namespace}, Status: corev1.NamespaceStatus{Phase: corev1.NamespaceTerminating}}
	bucket := drbackup.CreateDRStorageBucket(ns.Name, backup.Status.Location.BucketName, "")
	c := fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(ns, bucket).Build()
	for _, wantWait := range []bool{true, false} {
		prep, _, err := r.Prepare(t.Context(), c, backup)
		requireNoError(t, err)
		actions, result, err := r.Delete(backup, prep, nil)
		requireNoError(t, err)
		requireEqual(t, len(actions), 0, "namespace teardown must not create cleanup resources")
		requireEqual(t, result.RequeueAfter > 0, wantWait, "only release finalizer when the bucket is gone")
		if wantWait {
			requireNoError(t, c.Delete(t.Context(), bucket))
		}
	}
}

func TestPostgresBackupCanFindLegacyDottedJob(t *testing.T) {
	r := newDRTestReconciler()
	backup := drTestBackup("orders.daily")
	job, err := drbackup.CreateCopyJob(r.Scheme, backup, "worker:test", "src", "dst", 0)
	requireNoError(t, err)
	job.Name = drbackup.LegacyJobName(backup.Name, drbackup.JobRoleCopy)
	c := fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(job).Build()
	got, err := getBackupJob(t.Context(), c, backup, drbackup.CopyJobName(backup.Name), drbackup.JobRoleCopy)
	requireNoError(t, err)
	requireNotNil(t, got, "find legacy Job after upgrade")
	requireEqual(t, got.Name, job.Name, "legacy Job name")
}

func TestPostgresBackupKeepsPersistedBucketAndGrantsAcrossPrefixChanges(t *testing.T) {
	r := newDRTestReconciler()
	old, next := drTestBackup("old"), drTestBackup("next")
	oldBucket := r.drBucketName(old.Namespace)
	old.Status.Location = &v1.PostgresBackupLocation{BucketName: oldBucket, Prefix: old.Name}
	r.Config.CNPG.DrBucketPrefix = "nais-dr-next"
	newBucket := r.drBucketName(next.Namespace)
	next.Status.Location = &v1.PostgresBackupLocation{BucketName: newBucket, Prefix: next.Name}
	// Simulate the original namespace-named binding from before the upgrade.
	legacy := rciam.CreateStorageBucketPolicyMember(drbackup.BucketObjectUserPolicyName(old.Namespace), old.Namespace, "project", drbackup.GSAName(old.Namespace), oldBucket, rciam.StorageObjectUserRole)
	legacy.Labels = map[string]string{drbackup.ManagedByLabel: drbackup.ManagedByValue}
	related := relatedobjectsmap.NewRelatedObjectsMap(r.Scheme)
	related.Insert(legacy)
	c := fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(old, next, legacy).Build()
	for _, backup := range []*v1.PostgresBackup{old, next} {
		for _, a := range r.sharedDRInfrastructureActions(backup, "project", true, related) {
			requireNoError(t, a.Do(t.Context(), c, r.Scheme, nil))
		}
		bucket := &storagecnrm.StorageBucket{}
		requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: backup.Namespace, Name: backup.Status.Location.BucketName}, bucket))
		requireEqual(t, len(bucket.OwnerReferences), 1, "bucket scoped to its own backups")
		requireEqual(t, bucket.OwnerReferences[0].UID, backup.UID, "bucket owner UID")
	}
	newGrant := &iamcnrm.IAMPolicyMember{}
	requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: next.Namespace, Name: drbackup.BucketObjectUserPolicyName(newBucket)}, newGrant))
	requireEqual(t, newGrant.Spec.ResourceRef.Name, newBucket, "new grant references new bucket")
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(legacy), legacy))
	requireEqual(t, legacy.Spec.ResourceRef.Name, oldBucket, "legacy grant stays on old bucket")
	err := c.Get(t.Context(), client.ObjectKey{Namespace: old.Namespace, Name: drbackup.BucketObjectUserPolicyName(oldBucket)}, &iamcnrm.IAMPolicyMember{})
	requireTrue(t, apierrors.IsNotFound(err), "no duplicate GCP binding for old bucket")
	old.DeletionTimestamp = new(metav1.Now())
	for _, a := range r.sharedDRInfrastructureActions(old, "project", false, related) {
		requireNoError(t, a.Do(t.Context(), c, r.Scheme, nil))
	}
	err = c.Get(t.Context(), client.ObjectKey{Namespace: old.Namespace, Name: oldBucket}, &storagecnrm.StorageBucket{})
	requireTrue(t, apierrors.IsNotFound(err), "old bucket released after last owner")
	requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: next.Namespace, Name: newBucket}, &storagecnrm.StorageBucket{}))
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(newGrant), &iamcnrm.IAMPolicyMember{}))
}

func TestPostgresBackupJobResultSelectsCurrentWorkerAttempt(t *testing.T) {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "copy", Namespace: "test", UID: "current"}}
	makePod := func(name string, uid types.UID, exit int32, finished int64, msg string) *corev1.Pod {
		term := &corev1.ContainerStateTerminated{ExitCode: exit, FinishedAt: metav1.Unix(finished, 0), Message: msg}
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: job.Namespace, Labels: map[string]string{batchv1.ControllerUidLabel: string(job.UID)},
				OwnerReferences: []metav1.OwnerReference{{Kind: "Job", Name: job.Name, UID: uid, Controller: new(true)}},
			},
			Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
				{Name: "sidecar", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Message: `{"sizeBytes":9}`}}},
				{Name: drbackup.WorkerContainerName, State: corev1.ContainerState{Terminated: term}},
			}},
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(
		makePod("a-failed", job.UID, 1, 10, `{"error":"first attempt"}`),
		makePod("b-failed", job.UID, 1, 20, `{"error":"latest failed attempt"}`),
		makePod("c-previous-job", "previous", 0, 40, `{"sizeBytes":10}`),
		makePod("d-success", job.UID, 0, 30, `{"sizeBytes":42}`),
	).Build()
	job.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	result := readJobTerminationPayload(t.Context(), c, job)
	requireNotNil(t, result, "success payload")
	requireEqual(t, result.SizeBytes, int64(42), "successful worker attempt")
	job.Status.Conditions[0].Type = batchv1.JobFailed
	result = readJobTerminationPayload(t.Context(), c, job)
	requireNotNil(t, result, "failure payload")
	requireEqual(t, result.Error, "latest failed attempt", "latest failure for this job")
}
