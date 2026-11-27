package storage

import (
	"fmt"
	"strings"

	"github.com/cloudnative-pg/barman-cloud/pkg/api"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MinimalObjectStore returns the ObjectStore identity only, for deletion.
func MinimalObjectStore(bucketName string, objectMeta metav1.ObjectMeta) *barmanv1.ObjectStore {
	objectMeta.Name = bucketName
	return &barmanv1.ObjectStore{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ObjectStore",
			APIVersion: barmanv1.GroupVersion.String(),
		},
		ObjectMeta: objectMeta,
	}
}

// CreateObjectStore configures barman-cloud against the bucket. Authentication
// uses gkeEnvironment, meaning barman resolves an OAuth token from the GKE
// metadata server via Workload Identity — there are no static credentials here.
//
// Swapping to an S3-compatible store later is confined to the credentials block
// and the destination path scheme.
//
// retentionDays makes barman remove backups and WAL older than the recovery window.
func CreateObjectStore(bucketName string, objectMeta metav1.ObjectMeta, retentionDays int32) *barmanv1.ObjectStore {
	objectStore := MinimalObjectStore(bucketName, objectMeta)
	objectStore.Spec = barmanv1.ObjectStoreSpec{
		Configuration: api.BarmanObjectStoreConfiguration{
			BarmanCredentials: api.BarmanCredentials{
				Google: &api.GoogleCredentials{
					GKEEnvironment: true,
				},
			},
			DestinationPath: fmt.Sprintf("gs://%s", bucketName),
			Wal: &api.WalBackupConfiguration{
				Compression: api.CompressionTypeLz4,
			},
			Data: &api.DataBackupConfiguration{
				Compression: api.CompressionTypeLz4,
			},
		},
	}
	objectStore.Spec.RetentionPolicy = fmt.Sprintf("%dd", retentionDays)
	return objectStore
}

// CreateRecoveryObjectStore configures a temporary read-only Barman ObjectStore
// pointing at a standalone PostgresBackup's isolated prefix in the DR bucket.
// It has no RetentionPolicy because pgrator manages DR backup lifecycle.
func CreateRecoveryObjectStore(name, bucketName, prefix string, objectMeta metav1.ObjectMeta) *barmanv1.ObjectStore {
	objectStore := MinimalObjectStore(name, objectMeta)
	trimmedPrefix := strings.Trim(prefix, "/")
	destination := fmt.Sprintf("gs://%s", bucketName)
	if trimmedPrefix != "" {
		destination = fmt.Sprintf("gs://%s/%s", bucketName, trimmedPrefix)
	}
	objectStore.Spec = barmanv1.ObjectStoreSpec{
		Configuration: api.BarmanObjectStoreConfiguration{
			BarmanCredentials: api.BarmanCredentials{
				Google: &api.GoogleCredentials{
					GKEEnvironment: true,
				},
			},
			DestinationPath: destination,
			Wal: &api.WalBackupConfiguration{
				Compression: api.CompressionTypeLz4,
			},
			Data: &api.DataBackupConfiguration{
				Compression: api.CompressionTypeLz4,
			},
		},
	}
	return objectStore
}
