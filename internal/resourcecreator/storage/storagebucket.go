// Package storage builds the WAL archive storage for a CloudNativePG cluster:
// the Google Cloud Storage bucket (via Config Connector) and the barman-cloud
// ObjectStore that CloudNativePG's WAL archiver plugin points at.
package storage

import (
	storage_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// daysUntilDelete is how long WAL segments and base backups are kept in the
	// bucket before the lifecycle rule removes them.
	daysUntilDelete = 30

	// OwnerNameLabel and OwnerNamespaceLabel make the owning Postgres resource
	// easy to identify in addition to its Kubernetes owner reference.
	OwnerNameLabel      = "postgres.nais.io/name"
	OwnerNamespaceLabel = "postgres.nais.io/namespace"
)

func minimalStorageBucket(postgres *v1.Postgres, bucketName string) *storage_cnrm_cloud_google_com_v1beta1.StorageBucket {
	return &storage_cnrm_cloud_google_com_v1beta1.StorageBucket{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageBucket",
			APIVersion: storage_cnrm_cloud_google_com_v1beta1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      bucketName,
			Namespace: postgres.GetNamespace(),
			Labels: map[string]string{
				OwnerNameLabel:      postgres.GetName(),
				OwnerNamespaceLabel: postgres.GetNamespace(),
			},
		},
	}
}

// CreateStorageBucket builds the WAL bucket in the Postgres team's namespace.
func CreateStorageBucket(postgres *v1.Postgres, bucketName, location string) *storage_cnrm_cloud_google_com_v1beta1.StorageBucket {
	bucket := minimalStorageBucket(postgres, bucketName)
	bucket.Spec = storage_cnrm_cloud_google_com_v1beta1.StorageBucketSpec{
		Location:               location,
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
	return bucket
}
