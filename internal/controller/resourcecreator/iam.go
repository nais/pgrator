package resourcecreator

import (
	"fmt"

	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	iam_google_v1beta1 "github.com/nais/liberator/pkg/apis/iam.cnrm.cloud.google.com/v1beta1"
	"github.com/nais/liberator/pkg/namegen"
	"github.com/nais/pgrator/internal/config"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
)

const (
	IAMServiceAccountNamespace = "serviceaccounts"
	ProjectIdAnnotation        = "cnrm.cloud.google.com/project-id"
	ProjectRole                = "roles/iam.workloadIdentityUser"
)

func CreateMinimalIAMPolicyMember(postgres *data_nais_io_v1.Postgres, pgNamespace string) *iam_google_v1beta1.IAMPolicyMember {
	objectMeta := CreateObjectMeta(postgres)
	name, err := namegen.SuffixedShortName(pgNamespace, "postgres-pod", validation.DNS1123LabelMaxLength)
	if err != nil {
		panic(fmt.Sprintf("This should never happen: %v", err))
	}
	objectMeta.Name = name
	objectMeta.Namespace = IAMServiceAccountNamespace

	iamPolicyMember := &iam_google_v1beta1.IAMPolicyMember{
		TypeMeta: v1.TypeMeta{
			Kind:       "IAMPolicyMember",
			APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
		},
		ObjectMeta: objectMeta,
	}
	return iamPolicyMember
}

func CreateIAMPolicyMemberSpec(postgres *data_nais_io_v1.Postgres, cfg *config.Config, pgNamespace string) *iam_google_v1beta1.IAMPolicyMember {
	iamPolicyMember := CreateMinimalIAMPolicyMember(postgres, pgNamespace)
	spec := iam_google_v1beta1.IAMPolicyMemberSpec{
		Member: fmt.Sprintf("serviceAccount:%s.svc.id.goog[%s/postgres-pod]", cfg.GoogleProjectID, pgNamespace),
		Role:   ProjectRole,
		ResourceRef: iam_google_v1beta1.ResourceRef{
			ApiVersion: "iam.cnrm.cloud.google.com/v1beta1",
			Kind:       "IAMServiceAccount",
			Name:       ptr.To("postgres-pod"),
		},
	}

	iamPolicyMember.Spec = spec
	v1.SetMetaDataAnnotation(&iamPolicyMember.ObjectMeta, ProjectIdAnnotation, cfg.GoogleProjectID)
	return iamPolicyMember
}
