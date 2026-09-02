package controller

import (
	"testing"

	iamv1beta1 "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
)

func TestIAMPolicyHasChanges(t *testing.T) {
	newPolicyMember := func() *iamv1beta1.IAMPolicyMember {
		return &iamv1beta1.IAMPolicyMember{
			Spec: iamv1beta1.IAMPolicyMemberSpec{
				Member: "serviceAccount:cluster.svc.id.goog[team/postgres]",
				Role:   "roles/iam.workloadIdentityUser",
				ResourceRef: iamv1beta1.ResourceRef{
					APIVersion: new("iam.cnrm.cloud.google.com/v1beta1"),
					Kind:       "IAMServiceAccount",
					Name:       "postgres",
					Namespace:  "team",
				},
			},
		}
	}

	t.Run("does not report semantically identical policies as changed", func(t *testing.T) {
		desired := newPolicyMember()
		existing := newPolicyMember()
		requireFalse(t, iamPolicyHasChanges(desired, existing), "identical policy members should not be detected as changed")
	})

	testCases := []struct {
		name   string
		change func(*iamv1beta1.IAMPolicyMember)
	}{
		{
			name: "member changes",
			change: func(policy *iamv1beta1.IAMPolicyMember) {
				policy.Spec.Member = "serviceAccount:other"
			},
		},
		{
			name: "role changes",
			change: func(policy *iamv1beta1.IAMPolicyMember) {
				policy.Spec.Role = "roles/storage.objectUser"
			},
		},
		{
			name: "resource reference changes",
			change: func(policy *iamv1beta1.IAMPolicyMember) {
				policy.Spec.ResourceRef.Name = "other-postgres"
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			desired := newPolicyMember()
			existing := newPolicyMember()
			tc.change(existing)
			requireTrue(t, iamPolicyHasChanges(desired, existing), "policy changes should require recreation")
		})
	}
}
