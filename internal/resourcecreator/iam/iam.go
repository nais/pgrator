package iam

import (
	"fmt"

	"github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	WorkloadIdentityRole = "roles/iam.workloadIdentityUser"
	StorageBucketRole    = "roles/storage.objectUser"
	LogWriterRole        = "roles/logging.logWriter"
)

func MinimalPolicyMember(name, namespace string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
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

func CreateIAMServiceAccount(name, namespace string) *iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount {
	iamServiceAccount := &iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount{
		TypeMeta: v1.TypeMeta{
			Kind:       "IAMServiceAccount",
			APIVersion: "iam.cnrm.cloud.google.com/v1beta1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccountSpec{
			DisplayName: fmt.Sprintf("Postgres Pod Service Account for team %q", namespace),
		},
	}

	return iamServiceAccount
}

func CreateWorkloadIdentityPolicyMember(name, teamNamespace, pgNamespace, clusterGoogleProjectID, GSAName, KSAName string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	iamPolicyMember := MinimalPolicyMember(name, teamNamespace)
	iamPolicyMember.Spec = iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMemberSpec{
		Member: fmt.Sprintf("serviceAccount:%s.svc.id.goog[%s/%s]", clusterGoogleProjectID, pgNamespace, KSAName),
		Role:   WorkloadIdentityRole,
		ResourceRef: iam_cnrm_cloud_google_com_v1beta1.ResourceRef{
			APIVersion: ptr.To("iam.cnrm.cloud.google.com/v1beta1"),
			Kind:       "IAMServiceAccount",
			Name:       GSAName,
		},
	}
	return iamPolicyMember
}

func CreateStorageBucketPolicyMember(name, namespace, teamGoogleProjectID, GSAName, bucketName string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	iamPolicyMember := MinimalPolicyMember(name, namespace)
	iamPolicyMember.Spec = iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMemberSpec{
		Member: fmt.Sprintf("serviceAccount:%s@%s.iam.gserviceaccount.com", GSAName, teamGoogleProjectID),
		Role:   StorageBucketRole,
		ResourceRef: iam_cnrm_cloud_google_com_v1beta1.ResourceRef{
			APIVersion: ptr.To("storage.cnrm.cloud.google.com/v1beta1"),
			Kind:       "StorageBucket",
			External:   &bucketName,
		},
	}

	return iamPolicyMember
}

func CreateLogsWriterPolicyMember(name, namespace, teamGoogleProjectID, GSAName string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	iamPolicyMember := MinimalPolicyMember(name, namespace)
	iamPolicyMember.Spec = iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMemberSpec{
		Member: fmt.Sprintf("serviceAccount:%s@%s.iam.gserviceaccount.com", GSAName, teamGoogleProjectID),
		Role:   LogWriterRole,
		ResourceRef: iam_cnrm_cloud_google_com_v1beta1.ResourceRef{
			APIVersion: ptr.To("resourcemanager.cnrm.cloud.google.com/v1beta1"),
			Kind:       "Project",
			External:   ptr.To(fmt.Sprintf("projects/%s", teamGoogleProjectID)),
		},
	}

	return iamPolicyMember
}
