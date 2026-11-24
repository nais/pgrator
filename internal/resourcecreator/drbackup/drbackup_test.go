package drbackup_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nais/pgrator/internal/backupcopy"
	"github.com/nais/pgrator/internal/resourcecreator/drbackup"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
)

func TestDRBucketNameFitsGCSLimitAndIsDeterministic(t *testing.T) {
	short := drbackup.DRBucketName("nais-wal-dev-nais-dev", "basseng")
	if short == "" || len(short) > 63 {
		t.Fatalf("DRBucketName() = %q (len %d), want 1..63 chars", short, len(short))
	}
	if !strings.HasPrefix(short, "nais-dr-dev-nais-dev-basseng-") {
		t.Errorf("DRBucketName() = %q, want prefix nais-dr-dev-nais-dev-basseng-", short)
	}
	if errs := validation.IsDNS1035Label(short); len(errs) > 0 {
		t.Errorf("DRBucketName() = %q is not a valid DNS-1035 label: %v", short, errs)
	}
	if again := drbackup.DRBucketName("nais-wal-dev-nais-dev", "basseng"); again != short {
		t.Errorf("DRBucketName() not deterministic: %q != %q", again, short)
	}

	longNS := strings.Repeat("verylongteamname", 4)
	longBucket := drbackup.DRBucketName("nais-pg-wal-prod-gcp", longNS)
	if len(longBucket) > 63 {
		t.Errorf("long DRBucketName() len = %d (%q), want <= 63", len(longBucket), longBucket)
	}
}

type listRecorder struct {
	backupcopy.ObjectStore
	list func(string) error
}

func (s listRecorder) List(_ context.Context, prefix string) ([]backupcopy.ObjectInfo, error) {
	return nil, s.list(prefix)
}

func TestWorkerJobsUseValidLabelsAndRecognizedArguments(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	backup := &v1.PostgresBackup{
		ObjectMeta: metav1.ObjectMeta{Name: strings.Repeat("long-name-", 8) + "backup", Namespace: "team", UID: "backup-uid"},
		Spec:       v1.PostgresBackupSpec{Postgres: strings.Repeat("long-name-", 8) + "postgres"},
		Status: &v1.PostgresBackupStatus{
			ServerName: "orders", BackupID: "20261124T020000",
			BeginWal: "000000010000000000000002", EndWal: "000000010000000000000003",
		},
	}
	copyJob, err := drbackup.CreateCopyJob(scheme, backup, "pgrator:test", "source", "destination", 8*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if deadline := copyJob.Spec.ActiveDeadlineSeconds; deadline == nil || *deadline != int64((8*time.Hour)/time.Second) {
		t.Fatalf("copy Job deadline does not reflect configuration: %v", deadline)
	}
	cleanup, err := drbackup.CreateCleanupJob(scheme, backup, "pgrator:test", "destination", backup.Name)
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop at the first storage operation")
	for _, tc := range []struct {
		job     *batchv1.Job
		buckets []string
		prefix  string
	}{
		{copyJob, []string{"source", "destination"}, "orders/base/20261124T020000/"},
		{cleanup, []string{"destination"}, backup.Name + "/"},
	} {
		t.Run(tc.job.Labels[drbackup.JobRoleLabel], func(t *testing.T) {
			for key, value := range tc.job.Spec.Template.Labels {
				if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
					t.Errorf("label %q is invalid: %v", key, errs)
				}
			}
			args := tc.job.Spec.Template.Spec.Containers[0].Args
			if !backupcopy.IsBackupWorkerCommand(args) || !slices.Contains(args, "--backup-name="+backup.Name) {
				t.Fatalf("invalid worker command: %q", args)
			}
			var buckets []string
			newStore := func(bucket string) backupcopy.ObjectStore {
				buckets = append(buckets, bucket)
				return listRecorder{list: func(prefix string) error {
					if prefix != tc.prefix {
						t.Errorf("listed prefix %q, want %q", prefix, tc.prefix)
					}
					return stop
				}}
			}
			if err := backupcopy.RunCommand(t.Context(), args, newStore, filepath.Join(t.TempDir(), "termination")); !errors.Is(err, stop) {
				t.Fatalf("worker rejected generated arguments: %v", err)
			}
			if !slices.Equal(buckets, tc.buckets) {
				t.Errorf("opened buckets %v, want %v", buckets, tc.buckets)
			}
		})
	}
}

func TestCreateCopyAndCleanupJobs(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	disk := resource.MustParse("20Gi")
	backup := &v1.PostgresBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-backup-1",
			Namespace: "myteam",
			UID:       "backup-uid-1",
		},
		Spec: v1.PostgresBackupSpec{Postgres: "orders"},
		Status: &v1.PostgresBackupStatus{
			SourceUID:    "pg-uid-1",
			MajorVersion: "18",
			Extensions:   []v1.PostgresExtension{{Name: "postgis"}},
			DiskSize:     &disk,
			ServerName:   "pg-orders-main-65648812",
			BackupID:     "20261124T020000",
			BeginWal:     "000000010000000000000002",
			EndWal:       "000000010000000000000003",
		},
	}

	copyJob, err := drbackup.CreateCopyJob(scheme, backup, "pgrator:test", "src-wal-bucket", "dst-dr-bucket", 0)
	if err != nil {
		t.Fatalf("CreateCopyJob: %v", err)
	}
	if !metav1.IsControlledBy(copyJob, backup) {
		t.Errorf("copy Job is not controlled by PostgresBackup")
	}
	args := copyJob.Spec.Template.Spec.Containers[0].Args
	for _, wantArg := range []string{
		"backup-copy",
		"--source-bucket=src-wal-bucket",
		"--dr-bucket=dst-dr-bucket",
		"--backup-name=orders-backup-1",
		"--backup-id=20261124T020000",
	} {
		if !slices.Contains(args, wantArg) {
			t.Errorf("copy Job args %v missing %q", args, wantArg)
		}
	}

	cleanupJob, err := drbackup.CreateCleanupJob(scheme, backup, "pgrator:test", "dst-dr-bucket", "orders-backup-1")
	if err != nil {
		t.Fatalf("CreateCleanupJob: %v", err)
	}
	if !metav1.IsControlledBy(cleanupJob, backup) {
		t.Errorf("cleanup Job is not controlled by PostgresBackup")
	}
	cleanupArgs := cleanupJob.Spec.Template.Spec.Containers[0].Args
	for _, wantArg := range []string{
		"backup-delete",
		"--dr-bucket=dst-dr-bucket",
		"--backup-name=orders-backup-1",
	} {
		if !slices.Contains(cleanupArgs, wantArg) {
			t.Errorf("cleanup Job args %v missing %q", cleanupArgs, wantArg)
		}
	}
}
