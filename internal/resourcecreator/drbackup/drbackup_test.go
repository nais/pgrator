package drbackup_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/nais/pgrator/internal/resourcecreator/drbackup"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
)

func TestDRBucketNameFitsGCSLimitAndIsDeterministic(t *testing.T) {
	short := drbackup.DRBucketName("nais-pg-wal-dev", "myteam")
	if short == "" || len(short) > 63 {
		t.Fatalf("DRBucketName() = %q (len %d), want 1..63 chars", short, len(short))
	}
	if errs := validation.IsDNS1035Label(short); len(errs) > 0 {
		t.Errorf("DRBucketName() = %q is not a valid DNS-1035 label: %v", short, errs)
	}
	if again := drbackup.DRBucketName("nais-pg-wal-dev", "myteam"); again != short {
		t.Errorf("DRBucketName() not deterministic: %q != %q", again, short)
	}

	longNS := strings.Repeat("verylongteamname", 4)
	longBucket := drbackup.DRBucketName("nais-pg-wal-prod-gcp", longNS)
	if len(longBucket) > 63 {
		t.Errorf("long DRBucketName() len = %d (%q), want <= 63", len(longBucket), longBucket)
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

	copyJob, err := drbackup.CreateCopyJob(scheme, backup, "pgrator:test", "src-wal-bucket", "dst-dr-bucket")
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
