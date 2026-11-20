package storage

import (
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCreateStorageBucketLeavesRetentionToBarman(t *testing.T) {
	days := int32(14)
	for name, backup := range map[string]*v1.PostgresBackupPolicy{
		"default":  nil,
		"explicit": {RetentionDays: &days},
	} {
		t.Run(name, func(t *testing.T) {
			pg := &v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"},
				Spec:       v1.PostgresSpec{Backup: backup},
			}

			bucket := CreateStorageBucket(pg, "wal-bucket", "europe-north1")

			if len(bucket.Spec.LifecycleRules) != 0 {
				t.Errorf("bucket lifecycle rules = %v, want none; barman's retention policy owns deletion", bucket.Spec.LifecycleRules)
			}
		})
	}
}
