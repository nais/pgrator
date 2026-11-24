package controller

import (
	"context"
	"slices"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/backupcopy"
	"github.com/nais/pgrator/internal/config"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/resourcecreator/drbackup"
	rciam "github.com/nais/pgrator/internal/resourcecreator/iam"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newReadySourcePostgresAndCluster() (*v1.Postgres, *cnpgv1.Cluster) {
	pg := &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders",
			Namespace: "myteam",
			UID:       "pg-uid-1234",
		},
		Spec: v1.PostgresSpec{
			MajorVersion: "18",
			Resources: v1.PostgresResources{
				DiskSize: resource.MustParse("20Gi"),
				Cpu:      resource.MustParse("100m"),
				Memory:   resource.MustParse("512Mi"),
			},
			Extensions: []v1.PostgresExtension{{Name: "postgis"}},
		},
		Status: &v1.PostgresStatus{
			ClusterName: "pg-orders-main-65648812",
			Ownership: &v1.PostgresOwnership{
				Phase:      ownershipManaged,
				ClusterUID: "cluster-uid-5678",
			},
		},
	}
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pg-orders-main-65648812",
			Namespace: "myteam",
			UID:       "cluster-uid-5678",
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: v1.GroupVersion.String(),
				Kind:       postgresKind,
				Name:       pg.Name,
				UID:        pg.UID,
				Controller: new(true),
			}},
		},
		Spec: cnpgv1.ClusterSpec{
			PostgresConfiguration: cnpgv1.PostgresConfiguration{Extensions: []cnpgv1.ExtensionConfiguration{{Name: "postgis"}}},
			Plugins: []cnpgv1.PluginConfiguration{{
				Name:    rccnpg.BarmanPluginName,
				Enabled: new(true),
			}},
		},
		Status: cnpgv1.ClusterStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(cnpgv1.ConditionInitialized),
					Status: metav1.ConditionTrue,
				},
				{
					Type:   string(cnpgv1.ConditionClusterReady),
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	return pg, cluster
}

func backupSourceDatabase(t *testing.T, pg *v1.Postgres) *cnpgv1.Database {
	t.Helper()
	database, err := rccnpg.CreateDatabase(scheme.Scheme, pg, nil)
	requireNoError(t, err)
	// Physical resource names are derived from the main branch identity.
	database.Name = pg.Status.ClusterName + "-" + rccnpg.DatabaseName
	database.Spec.ClusterRef.Name = pg.Status.ClusterName
	database.UID = "database-uid"
	pg.Status.Ownership.Resources = append(pg.Status.Ownership.Resources, v1.PostgresOwnedResource{
		Kind: "Database", Name: database.Name, UID: string(database.UID),
	})
	return database
}

func TestPostgresBackupReconcilerLifecycle(t *testing.T) {
	pg, cluster := newReadySourcePostgresAndCluster()
	retention := int32(90)
	backup := &v1.PostgresBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "orders-backup-1",
			Namespace:  "myteam",
			UID:        "backup-uid-9999",
			Generation: 1,
		},
		Spec: v1.PostgresBackupSpec{
			Postgres:      "orders",
			RetentionDays: &retention,
		},
	}

	reconciler := &PostgresBackupReconciler{Recorder: recorder, Scheme: scheme.Scheme}
	database := backupSourceDatabase(t, pg)
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pg, cluster, database, backup).Build()

	original := backup.DeepCopy()
	prepared, _, err := reconciler.Prepare(context.Background(), reader, backup)
	requireNoError(t, err)
	requireTrue(t, reconciler.StatusCheckpointRequired(backup, original), "expected source snapshot to require status checkpoint before creating CNPG Backup")
	requireEqual(t, backup.Status.SourceUID, "pg-uid-1234", "sourceUID")
	requireEqual(t, backup.Status.MajorVersion, "18", "majorVersion")
	requireEqual(t, backup.Status.ServerName, "pg-orders-main-65648812", "serverName")
	requireNotNil(t, backup.Status.DiskSize, "diskSize")
	requireEqual(t, backup.Status.DiskSize.String(), "20Gi", "diskSize value")
	requireTrue(t, slices.Equal(backup.Status.Extensions, []v1.PostgresExtension{{Name: "postgis"}}), "extensions snapshot")

	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	actions, _, err := reconciler.Update(backup, prepared, related)
	requireNoError(t, err)
	requireEqual(t, len(actions), 1, "expected 1 action to create CNPG Backup")
	requireEqual(t, backup.Status.Phase, v1.PostgresBackupPhasePreparing, "initial phase")
	ready := meta.FindStatusCondition(backup.Status.Conditions, readyCondition)
	requireNotNil(t, ready, "Ready condition")
	requireEqual(t, ready.Status, metav1.ConditionFalse, "Ready status while preparing")

	cnpgBackup := actions[0].GetObject().(*cnpgv1.Backup).DeepCopy()
	startedAt := metav1.NewTime(time.Date(2026, 11, 24, 2, 0, 0, 0, time.UTC))
	cnpgBackup.Status = cnpgv1.BackupStatus{
		Phase:     cnpgv1.BackupPhaseRunning,
		StartedAt: &startedAt,
		BeginWal:  "000000010000000000000001",
	}

	prepared.CNPGBackup = cnpgBackup
	_, _, err = reconciler.Update(backup, prepared, related)
	requireNoError(t, err)
	requireEqual(t, backup.Status.Phase, v1.PostgresBackupPhaseRunning, "running phase")
	requireEqual(t, backup.Status.BeginWal, "000000010000000000000001", "beginWal")

	stoppedAt := metav1.NewTime(time.Date(2026, 11, 24, 2, 5, 0, 0, time.UTC))
	cnpgBackup.Status.Phase = cnpgv1.BackupPhaseCompleted
	cnpgBackup.Status.BackupID = "20261124T020000"
	cnpgBackup.Status.EndWal = "000000010000000000000002"
	cnpgBackup.Status.StoppedAt = &stoppedAt

	_, _, err = reconciler.Update(backup, prepared, related)
	requireNoError(t, err)
	requireEqual(t, backup.Status.Phase, v1.PostgresBackupPhaseFailed, "CNPG completion without a DR destination is not a standalone backup")
	requireEqual(t, backup.Status.BackupID, "20261124T020000", "backupID")
	requireEqual(t, backup.Status.EndWal, "000000010000000000000002", "endWal")
	ready = meta.FindStatusCondition(backup.Status.Conditions, readyCondition)
	requireNotNil(t, ready, "Ready condition on completion")
	requireEqual(t, ready.Reason, "DRStorageNotConfigured", "reason for rejected backup")

	// Deleting the source Postgres and Cluster must not reopen a terminal backup.
	emptyReader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(backup).Build()
	afterDeletePrep, _, err := reconciler.Prepare(context.Background(), emptyReader, backup)
	requireNoError(t, err)
	_, _, err = reconciler.Update(backup, afterDeletePrep, related)
	requireNoError(t, err)
	requireEqual(t, backup.Status.Phase, v1.PostgresBackupPhaseFailed, "phase remains terminal after source Postgres is deleted")
}

func TestPostgresBackupReconcilerRejectsUnreadyOrUnsupportedSource(t *testing.T) {
	reconciler := &PostgresBackupReconciler{Recorder: recorder, Scheme: scheme.Scheme}
	backup := &v1.PostgresBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-backup-1", Namespace: "myteam", UID: "backup-uid"},
		Spec:       v1.PostgresBackupSpec{Postgres: "orders"},
	}

	t.Run("missing source Postgres", func(t *testing.T) {
		reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).Build()
		_, _, err := reconciler.Prepare(context.Background(), reader, backup.DeepCopy())
		requireErrorContains(t, err, `getting source Postgres "orders"`)
	})

	t.Run("cluster not ready", func(t *testing.T) {
		pg, cluster := newReadySourcePostgresAndCluster()
		cluster.Status.Conditions = nil
		reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pg, cluster).Build()
		_, _, err := reconciler.Prepare(context.Background(), reader, backup.DeepCopy())
		requireErrorContains(t, err, "is not ready")
	})

	t.Run("cluster without barman plugin", func(t *testing.T) {
		pg, cluster := newReadySourcePostgresAndCluster()
		cluster.Spec.Plugins = nil
		reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pg, cluster).Build()
		_, _, err := reconciler.Prepare(context.Background(), reader, backup.DeepCopy())
		requireErrorContains(t, err, "no enabled barman-cloud plugin")
	})

	t.Run("pending extension removal", func(t *testing.T) {
		pg, cluster := newReadySourcePostgresAndCluster()
		database := backupSourceDatabase(t, pg)
		pg.Spec.Extensions = nil
		database.Spec.Extensions[0].Ensure = cnpgv1.EnsureAbsent
		reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pg, cluster, database).Build()
		request := backup.DeepCopy()
		_, _, err := reconciler.Prepare(t.Context(), reader, request)
		requireErrorContains(t, err, "still being removed")
		requireEqual(t, request.Status.SourceUID, "", "unreconciled source was not checkpointed")
	})
}

func TestPostgresBackupReconcilerDeleteRemovesOwnedCNPGBackup(t *testing.T) {
	reconciler := &PostgresBackupReconciler{Recorder: recorder, Scheme: scheme.Scheme}
	backup := &v1.PostgresBackup{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-backup-1", Namespace: "myteam", UID: "backup-uid"},
		Spec:       v1.PostgresBackupSpec{Postgres: "orders"},
	}
	cnpgBackup, err := rccnpg.CreateBackup(scheme.Scheme, backup, "pg-orders-main-65648812")
	requireNoError(t, err)

	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	related.Insert(cnpgBackup)

	actions, _, err := reconciler.Delete(backup, PostgresBackupPreparedData{}, related)
	requireNoError(t, err)
	requireEqual(t, len(actions), 1, "expected 1 deleteIfExists action")
	requireEqual(t, actions[0].GetObject().GetName(), "orders-backup-1", "deleted CNPG Backup name")
}

func TestPostgresBackupReconcilerDRCopyExpiryAndCleanup(t *testing.T) {
	pg, cluster := newReadySourcePostgresAndCluster()
	cluster.Spec.Plugins[0].Parameters = map[string]string{"barmanObjectName": "nais-pg-wal-dev-myteam-orders-1234"}
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "myteam",
			Labels: map[string]string{ProjectIDLabel: "myteam-dev-1234"},
		},
	}
	retention := int32(30)
	backup := &v1.PostgresBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "orders-backup-dr",
			Namespace:  "myteam",
			UID:        "backup-uid-dr",
			Generation: 1,
		},
		Spec: v1.PostgresBackupSpec{
			Postgres:      "orders",
			RetentionDays: &retention,
		},
	}

	cfg := &config.Config{
		GoogleProjectID: "nais-dev-cluster",
		Google:          config.Google{Location: "europe-north1"},
		CNPG: config.CNPG{
			WalBucketPrefix:   "nais-pg-wal-dev",
			BackupWorkerImage: "pgrator:test",
		},
	}
	reconciler := &PostgresBackupReconciler{Config: cfg, Recorder: recorder, Scheme: scheme.Scheme}
	drBucket := reconciler.drBucketName("myteam")

	// 1. CNPG Backup completes -> reconciler creates source bucket IAM policy and waits for DR infra ready.
	cnpgBackup, err := rccnpg.CreateBackup(scheme.Scheme, backup, cluster.Name)
	requireNoError(t, err)
	stoppedAt := metav1.NewTime(time.Date(2026, 11, 24, 2, 5, 0, 0, time.UTC))
	cnpgBackup.Status = cnpgv1.BackupStatus{
		Phase:           cnpgv1.BackupPhaseCompleted,
		BackupID:        "20261124T020000",
		ServerName:      cluster.Name,
		DestinationPath: "gs://nais-pg-wal-dev-myteam-orders-1234",
		BeginWal:        "000000010000000000000002",
		EndWal:          "000000010000000000000003",
		StoppedAt:       &stoppedAt,
	}

	database := backupSourceDatabase(t, pg)
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(ns, pg, cluster, database, backup, cnpgBackup).Build()
	prepared, _, err := reconciler.Prepare(context.Background(), reader, backup)
	requireNoError(t, err)
	requireEqual(t, prepared.TeamProjectID, "myteam-dev-1234", "teamProjectID")
	requireEqual(t, prepared.SourceBucket, "nais-pg-wal-dev-myteam-orders-1234", "sourceBucket")

	related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	related.Insert(cnpgBackup)
	actions, res, err := reconciler.Update(backup, prepared, related)
	requireNoError(t, err)
	requireEqual(t, backup.Status.Phase, v1.PostgresBackupPhaseRunning, "phase stays Running while waiting for DR infra and copy Job")
	requireTrue(t, res.RequeueAfter > 0, "expected requeue while waiting for DR readiness")
	var sourceGrant *iamcnrm.IAMPolicyMember
	for _, a := range actions {
		if policy, ok := a.GetObject().(*iamcnrm.IAMPolicyMember); ok && policy.Spec.Role == rciam.StorageObjectViewerRole {
			sourceGrant = policy.DeepCopy()
		}
		if _, ok := a.GetObject().(*batchv1.Job); ok {
			t.Fatal("copy Job must not be created until IAM is ready")
		}
	}
	requireNotNil(t, sourceGrant, "temporary source read grant")
	requireEqual(t, sourceGrant.Spec.ResourceRef.Name, prepared.SourceBucket, "granted bucket")

	// 2. Mark Config Connector DR resources Ready -> reconciler creates copy Job.
	readyCond := []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
	obsGen := int64(1)
	sourceGrant.Generation = obsGen
	sourceGrant.Status = iamcnrm.IAMStatus{Conditions: readyCond, ObservedGeneration: obsGen}
	for _, obj := range []client.Object{
		&iamcnrm.IAMServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: drbackup.GSAName("myteam"), Namespace: "myteam", Generation: 1}, Status: iamcnrm.IAMStatus{Conditions: readyCond, ObservedGeneration: 1}},
		&iamcnrm.IAMPolicyMember{ObjectMeta: metav1.ObjectMeta{Name: drbackup.WorkloadIdentityPolicyName("myteam"), Namespace: "myteam", Generation: 1}, Status: iamcnrm.IAMStatus{Conditions: readyCond, ObservedGeneration: 1}},
		&iamcnrm.IAMPolicyMember{ObjectMeta: metav1.ObjectMeta{Name: drbackup.BucketObjectUserPolicyName("myteam"), Namespace: "myteam", Generation: 1}, Status: iamcnrm.IAMStatus{Conditions: readyCond, ObservedGeneration: 1}},
		&iamcnrm.IAMPolicyMember{ObjectMeta: metav1.ObjectMeta{Name: drbackup.BucketViewerPolicyName("myteam"), Namespace: "myteam", Generation: 1}, Status: iamcnrm.IAMStatus{Conditions: readyCond, ObservedGeneration: 1}},
		sourceGrant,
		&storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: drBucket, Namespace: "myteam", Generation: 1}, Status: storagecnrm.StorageBucketStatus{Conditions: readyCond, ObservedGeneration: &obsGen}},
	} {
		related.Insert(obj)
	}

	actions, _, err = reconciler.Update(backup, prepared, related)
	requireNoError(t, err)
	requireNotNil(t, backup.Status.Location, "destination checkpoint")
	for _, a := range actions {
		if _, ok := a.GetObject().(*batchv1.Job); ok {
			t.Fatal("copy Job must not be created in the destination checkpoint pass")
		}
	}
	actions, _, err = reconciler.Update(backup, prepared, related)
	requireNoError(t, err)
	requireEqual(t, backup.Status.Phase, v1.PostgresBackupPhaseRunning, "phase is Running while copy Job executes")
	var copyJob *batchv1.Job
	for _, a := range actions {
		if j, ok := a.GetObject().(*batchv1.Job); ok && j.Name == drbackup.CopyJobName(backup.Name) {
			copyJob = j.DeepCopy()
		}
	}
	requireNotNil(t, copyJob, "expected copy Job to be created")
	copyJob.UID = "copy-job-uid"
	jobReader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(ns, copyJob).Build()
	prepared, _, err = reconciler.Prepare(t.Context(), jobReader, backup)
	requireNoError(t, err)
	// Neither the source nor ready IAM objects are visible now. Retain the
	// existing Job, rather than recreating or pruning it on a transient flap.
	activeActions, _, err := reconciler.Update(backup, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	assertCopyJobKept(t, activeActions, copyJob)

	// 3. Copy Job succeeds -> PostgresBackup becomes Completed with Location and SizeBytes.
	completedAt := metav1.NewTime(time.Date(2026, 11, 24, 2, 10, 0, 0, time.UTC))
	copyJob.Status.CompletionTime = &completedAt
	copyJob.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	copyJob.Annotations = nil // legacy Job with no recorded source bucket
	jobReader = fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(ns, copyJob).Build()
	prepared, _, err = reconciler.Prepare(t.Context(), jobReader, backup)
	requireNoError(t, err)
	requireEqual(t, prepared.SourceBucket, "", "source bucket can no longer be identified")
	prepared.CopyResult = &backupcopy.TerminationPayload{SizeBytes: 42_000_000}

	actions, _, err = reconciler.Update(backup, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme))
	requireNoError(t, err)
	assertCopyJobKept(t, actions, copyJob)
	requireEqual(t, backup.Status.Phase, v1.PostgresBackupPhaseCompleted, "phase after copy Job succeeds")
	requireNotNil(t, backup.Status.ExpiresAt, "expiresAt")
	requireTrue(t, backup.Status.ExpiresAt.Time.Equal(completedAt.Add(30*24*time.Hour)), "expiresAt should be completedAt + 30d")
	requireNotNil(t, backup.Status.Location, "DR location set")
	requireEqual(t, backup.Status.Location.BucketName, drBucket, "DR bucket name")
	requireEqual(t, backup.Status.Location.Prefix, backup.Name, "DR prefix")
	requireNotNil(t, backup.Status.SizeBytes, "SizeBytes set")
	requireEqual(t, *backup.Status.SizeBytes, int64(42_000_000), "SizeBytes value")

	// 4. Delete runs cleanup Job before releasing finalizer.
	delActions, delRes, err := reconciler.Delete(backup, PostgresBackupPreparedData{TeamProjectID: "myteam-dev-1234"}, related)
	requireNoError(t, err)
	requireTrue(t, delRes.RequeueAfter > 0, "expected finalizer to wait for cleanup Job")
	var cleanupJob *batchv1.Job
	for _, a := range delActions {
		if j, ok := a.GetObject().(*batchv1.Job); ok && j.Name == drbackup.CleanupJobName(backup.Name) {
			cleanupJob = j.DeepCopy()
		}
	}
	requireNotNil(t, cleanupJob, "expected cleanup Job to be created during Delete")

	cleanupJob.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	_, delResDone, err := reconciler.Delete(backup, PostgresBackupPreparedData{TeamProjectID: "myteam-dev-1234", CleanupJob: cleanupJob}, related)
	requireNoError(t, err)
	requireTrue(t, delResDone.IsZero(), "expected finalizer removal once cleanup Job succeeds")
}

func assertCopyJobKept(t *testing.T, actions []action.Action, job *batchv1.Job) {
	t.Helper()
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(job).Build()
	om := ownership.NewOwnerManager("postgresbackup.nais.io/owner")
	for _, a := range actions {
		if obj, ok := a.GetObject().(*batchv1.Job); ok && obj.Name == job.Name {
			requireNoError(t, a.Do(t.Context(), c, scheme.Scheme, om))
			requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(job), &batchv1.Job{}))
			return
		}
	}
	t.Fatalf("existing copy Job %q was unclaimed", job.Name)
}
