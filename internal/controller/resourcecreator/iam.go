package resourcecreator

import (
	"fmt"

	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/pkg/api/thirdparty/google/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ProjectRole              = "roles/iam.workloadIdentityUser"
	GSAName                  = "postgres-pod"
	KSAName                  = "postgres-pod"
	ServiceAccountsNamespace = "serviceaccounts"
)

func CreateMinimalIAMPolicyMember(postgres *data_nais_io_v1.Postgres, nameSuffix string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	objectMeta := CreateObjectMeta(postgres)
	objectMeta.Name = fmt.Sprintf("%s-%s", objectMeta.Name, nameSuffix)

	iamPolicyMember := &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{
		TypeMeta: v1.TypeMeta{
			Kind:       "IAMPolicyMember",
			APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
		},
		ObjectMeta: objectMeta,
	}
	return iamPolicyMember
}

func CreateIAMServiceAccount(postgres *data_nais_io_v1.Postgres) *iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount {
	iamServiceAccount := &iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount{
		TypeMeta: v1.TypeMeta{
			Kind:       "IAMServiceAccount",
			APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
		},
		ObjectMeta: CreateObjectMeta(postgres),
		Spec: iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccountSpec{
			DisplayName: fmt.Sprintf("Postgres Pod Service Account for team %q", postgres.Namespace),
		},
	}

	return iamServiceAccount
}

func CreateWorkloadIdentityIAMPolicyMember(postgres *data_nais_io_v1.Postgres, teamGoogleProjectID string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	iamPolicyMember := CreateMinimalIAMPolicyMember(postgres, "wi-user")
	spec := iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMemberSpec{
		Member: fmt.Sprintf("serviceAccount:%s.svc.id.goog[%s/%s]", teamGoogleProjectID, postgres.Namespace, KSAName),
		Role:   ProjectRole,
		ResourceRef: iam_cnrm_cloud_google_com_v1beta1.ResourceRef{
			APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
			Kind:       "IAMServiceAccount",
			Name:       GSAName,
		},
	}

	iamPolicyMember.Spec = spec
	return iamPolicyMember
}

func CreateStorageBucketIAMPolicyMember(postgres *data_nais_io_v1.Postgres, projectID, bucketName string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	iamPolicyMember := CreateMinimalIAMPolicyMember(postgres, "gcs-user")
	iamPolicyMember.Namespace = ServiceAccountsNamespace
	spec := iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMemberSpec{
		Member: fmt.Sprintf("serviceAccount:%s@%s.iam.gserviceaccount.com", GSAName, projectID),
		Role:   "roles/storage.objectUser",
		ResourceRef: iam_cnrm_cloud_google_com_v1beta1.ResourceRef{
			APIVersion: "storage.cnrm.cloud.google.com/v1beta1",
			Kind:       "StorageBucket",
			Name:       bucketName,
			Namespace:  "nais-system",
		},
	}

	iamPolicyMember.Spec = spec
	return iamPolicyMember
}
