package storage

import (
	"fmt"

	"github.com/cloudnative-pg/barman-cloud/pkg/api"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/nais/pgrator/internal/resourcecreator"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func MinimalObjectStore(postgres *data_nais_io_v1.Postgres, storageBucketName string) *barmanv1.ObjectStore {
	objectMeta := resourcecreator.CreateObjectMeta(postgres)
	objectMeta.Name = storageBucketName

	return &barmanv1.ObjectStore{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ObjectStore",
			APIVersion: barmanv1.GroupVersion.String(),
		},
		ObjectMeta: objectMeta,
	}
}

func CreateObjectStore(postgres *data_nais_io_v1.Postgres, storageBucketName string) *barmanv1.ObjectStore {
	objectStore := MinimalObjectStore(postgres, storageBucketName)

	objectStore.Spec = barmanv1.ObjectStoreSpec{
		Configuration: api.BarmanObjectStoreConfiguration{
			BarmanCredentials: api.BarmanCredentials{
				Google: &api.GoogleCredentials{
					GKEEnvironment: true,
				},
			},
			DestinationPath: fmt.Sprintf("gs://%s", storageBucketName),
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
