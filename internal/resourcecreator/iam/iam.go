// Package iam builds the Google IAM resources (via Config Connector) that let a
// CloudNativePG cluster authenticate to its WAL bucket through GKE Workload
// Identity. No static credentials are involved: barman-cloud is configured with
// googleCredentials.gkeEnvironment, which makes it pick up an OAuth token from
// the metadata server.
package iam

import (
	"fmt"

	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	WorkloadIdentityRole = "roles/iam.workloadIdentityUser"
	StorageBucketRole    = "roles/storage.objectUser"
)

func MinimalPolicyMember(name, namespace string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	return &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{
		TypeMeta: metav1.TypeMeta{
			Kind:       "IAMPolicyMember",
			APIVersion: iam_cnrm_cloud_google_com_v1beta1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

// CreateIAMServiceAccount creates the Google service account that the CNPG
// instance pods impersonate through Workload Identity.
func CreateIAMServiceAccount(name, namespace string) *iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount {
	return &iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount{
		TypeMeta: metav1.TypeMeta{
			Kind:       "IAMServiceAccount",
			APIVersion: iam_cnrm_cloud_google_com_v1beta1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccountSpec{
			DisplayName: fmt.Sprintf("Postgres Pod Service Account for team %q", namespace),
		},
	}
}

// CreateWorkloadIdentityPolicyMember binds the Kubernetes service account that
// CloudNativePG creates for the cluster to the Google service account.
func CreateWorkloadIdentityPolicyMember(name, teamNamespace, pgNamespace, clusterGoogleProjectID, gsaName, ksaName string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	policyMember := MinimalPolicyMember(name, teamNamespace)
	policyMember.Spec = iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMemberSpec{
		Member: fmt.Sprintf("serviceAccount:%s.svc.id.goog[%s/%s]", clusterGoogleProjectID, pgNamespace, ksaName),
		Role:   WorkloadIdentityRole,
		ResourceRef: iam_cnrm_cloud_google_com_v1beta1.ResourceRef{
			APIVersion: new(iam_cnrm_cloud_google_com_v1beta1.GroupVersion.String()),
			Kind:       "IAMServiceAccount",
			Name:       gsaName,
		},
	}
	return policyMember
}

// CreateStorageBucketPolicyMember grants the Google service account access to the
// WAL bucket. It lives alongside the bucket in the central WAL bucket namespace,
// not in the team namespace.
func CreateStorageBucketPolicyMember(name, namespace, teamGoogleProjectID, gsaName, bucketName, role string) *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember {
	policyMember := MinimalPolicyMember(name, namespace)
	policyMember.Spec = iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMemberSpec{
		Member: fmt.Sprintf("serviceAccount:%s@%s.iam.gserviceaccount.com", gsaName, teamGoogleProjectID),
		Role:   role,
		ResourceRef: iam_cnrm_cloud_google_com_v1beta1.ResourceRef{
			APIVersion: new("storage.cnrm.cloud.google.com/v1beta1"),
			Kind:       "StorageBucket",
			External:   &bucketName,
		},
	}
	return policyMember
}
