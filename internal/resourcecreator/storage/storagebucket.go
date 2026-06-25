package storage

import (
	"github.com/nais/pgrator/internal/resourcecreator"
	storage_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	daysUntilDelete = 8
)

func MinimalStorageBucket(postgres *data_nais_io_v1.Postgres, storageBucketName string, storageBucketNamespace string) *storage_cnrm_cloud_google_com_v1beta1.StorageBucket {
	objectMeta := resourcecreator.CreateObjectMeta(postgres)
	objectMeta.Name = storageBucketName
	objectMeta.Namespace = storageBucketNamespace

	return &storage_cnrm_cloud_google_com_v1beta1.StorageBucket{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageBucket",
			APIVersion: storage_cnrm_cloud_google_com_v1beta1.GroupVersion.String(),
		},
		ObjectMeta: objectMeta,
	}
}

func CreateStorageBucket(postgres *data_nais_io_v1.Postgres, storageBucketName, storageBucketNamespace, storageBucketLocation string) *storage_cnrm_cloud_google_com_v1beta1.StorageBucket {
	storageBucket := MinimalStorageBucket(postgres, storageBucketName, storageBucketNamespace)

	storageBucket.Spec = storage_cnrm_cloud_google_com_v1beta1.StorageBucketSpec{
		Location:               storageBucketLocation,
		PublicAccessPrevention: storage_cnrm_cloud_google_com_v1beta1.PublicAccessPreventionInherited,
		LifecycleRules: []storage_cnrm_cloud_google_com_v1beta1.StorageBucketLifecycleRule{
			{
				Action: &storage_cnrm_cloud_google_com_v1beta1.StorageBucketLifecycleRuleAction{
					Type: storage_cnrm_cloud_google_com_v1beta1.StorageBucketLifecycleRuleActionTypeDelete,
				},
				Condition: &storage_cnrm_cloud_google_com_v1beta1.StorageBucketLifecycleRuleCondition{
					Age: new(daysUntilDelete),
				},
			},
		},
	}

	return storageBucket
}
