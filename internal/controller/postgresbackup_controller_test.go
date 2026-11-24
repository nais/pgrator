package controller

import (
	"context"
	"slices"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
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
	reader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(pg, cluster, backup).Build()

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
	requireEqual(t, backup.Status.Phase, v1.PostgresBackupPhaseCompleted, "completed phase")
	requireEqual(t, backup.Status.BackupID, "20261124T020000", "backupID")
	requireEqual(t, backup.Status.EndWal, "000000010000000000000002", "endWal")
	requireNotNil(t, backup.Status.CompletedAt, "completedAt")
	requireNotNil(t, backup.Status.ExpiresAt, "expiresAt")
	wantExpires := stoppedAt.Add(90 * 24 * time.Hour)
	requireTrue(t, backup.Status.ExpiresAt.Time.Equal(wantExpires), "expiresAt should be completedAt + 90d")
	ready = meta.FindStatusCondition(backup.Status.Conditions, readyCondition)
	requireNotNil(t, ready, "Ready condition on completion")
	requireEqual(t, ready.Status, metav1.ConditionTrue, "Ready status on completion")

	// Deleting the source Postgres and Cluster afterwards must not break a
	// completed PostgresBackup.
	emptyReader := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(backup).Build()
	afterDeletePrep, _, err := reconciler.Prepare(context.Background(), emptyReader, backup)
	requireNoError(t, err)
	_, _, err = reconciler.Update(backup, afterDeletePrep, related)
	requireNoError(t, err)
	requireEqual(t, backup.Status.Phase, v1.PostgresBackupPhaseCompleted, "phase remains Completed after source Postgres is deleted")
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
