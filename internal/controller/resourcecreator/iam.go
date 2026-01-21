package resourcecreator

import (
	"fmt"

	"github.com/nais/pgrator/internal/namegen"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/pkg/api/thirdparty/google/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	WorkloadIdentityRole     = "roles/iam.workloadIdentityUser"
	StorageBucketRole        = "roles/storage.objectUser"
	GSAName                  = "postgres-pod"
	KSAName                  = "postgres-pod"
	ServiceAccountsNamespace = "serviceaccounts"
)

func CreateMinimalIAMPolicyMember(name, namespace string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	return &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{
		TypeMeta: v1.TypeMeta{
			Kind:       "IAMPolicyMember",
			APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func CreateIAMServiceAccount(postgres *data_nais_io_v1.Postgres) *iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount {
	iamServiceAccount := &iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount{
		TypeMeta: v1.TypeMeta{
			Kind:       "IAMServiceAccount",
			APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      GSAName,
			Namespace: postgres.GetNamespace(),
		},
		Spec: iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccountSpec{
			DisplayName: fmt.Sprintf("Postgres Pod Service Account for team %q", postgres.Namespace),
		},
	}

	return iamServiceAccount
}

func CreateWorkloadIdentityIAMPolicyMember(teamNamespace, teamGoogleProjectID string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	iamPolicyMember := CreateMinimalIAMPolicyMember(GSAName+"-wi-user", teamNamespace)
	iamPolicyMember.Spec = iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMemberSpec{
		Member: fmt.Sprintf("serviceAccount:%s.svc.id.goog[%s/%s]", teamGoogleProjectID, teamNamespace, KSAName),
		Role:   WorkloadIdentityRole,
		ResourceRef: iam_cnrm_cloud_google_com_v1beta1.ResourceRef{
			APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
			Kind:       "IAMServiceAccount",
			Name:       GSAName,
		},
	}
	return iamPolicyMember
}

func CreateStorageBucketIAMPolicyMember(teamNamespace, teamGoogleProjectID, bucketName string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	name, err := namegen.ShortName(fmt.Sprintf("pg-gcs-%s", teamNamespace), 63)
	if err != nil {
		panic(err)
	}
	iamPolicyMember := CreateMinimalIAMPolicyMember(name, ServiceAccountsNamespace)
	iamPolicyMember.Spec = iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMemberSpec{
		Member: fmt.Sprintf("serviceAccount:%s@%s.iam.gserviceaccount.com", GSAName, teamGoogleProjectID),
		Role:   StorageBucketRole,
		ResourceRef: iam_cnrm_cloud_google_com_v1beta1.ResourceRef{
			APIVersion: "storage.cnrm.cloud.google.com/v1beta1",
			Kind:       "StorageBucket",
			External:   &bucketName,
		},
	}

	return iamPolicyMember
}
