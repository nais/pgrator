package resourcecreator

import (
	"fmt"

	"github.com/nais/pgrator/internal/config"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/pkg/api/thirdparty/google/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ProjectRole = "roles/iam.workloadIdentityUser"
	GSAName     = "postgres-pod"
	KSAName     = "postgres-pod"
)

func CreateMinimalIAMPolicyMember(postgres *data_nais_io_v1.Postgres) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	iamPolicyMember := &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{
		TypeMeta: v1.TypeMeta{
			Kind:       "IAMPolicyMember",
			APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
		},
		ObjectMeta: CreateObjectMeta(postgres),
	}
	return iamPolicyMember
}

func CreateIAMServiceAccount(postgres *data_nais_io_v1.Postgres, cfg *config.Config) *iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount {
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

func CreateIAMPolicyMemberSpec(postgres *data_nais_io_v1.Postgres, cfg *config.Config, teamGoogleProjectID string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	iamPolicyMember := CreateMinimalIAMPolicyMember(postgres)
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
