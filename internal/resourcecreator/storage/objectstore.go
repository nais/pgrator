package storage

import (
	"fmt"

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
func CreateObjectStore(bucketName string, objectMeta metav1.ObjectMeta) *barmanv1.ObjectStore {
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
				Compression: api.CompressionTypeZstd,
			},
			Data: &api.DataBackupConfiguration{
				Compression: api.CompressionTypeSnappy,
			},
		},
	}
	return objectStore
}
