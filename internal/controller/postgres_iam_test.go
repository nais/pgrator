package controller

import (
	iamv1beta1 "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"
)

var _ = Describe("IAM policy member change detection", func() {
	newPolicyMember := func() *iamv1beta1.IAMPolicyMember {
		return &iamv1beta1.IAMPolicyMember{
			Spec: iamv1beta1.IAMPolicyMemberSpec{
				Member: "serviceAccount:cluster.svc.id.goog[team/postgres]",
				Role:   "roles/iam.workloadIdentityUser",
				ResourceRef: iamv1beta1.ResourceRef{
					APIVersion: ptr.To("iam.cnrm.cloud.google.com/v1beta1"),
					Kind:       "IAMServiceAccount",
					Name:       "postgres",
					Namespace:  "team",
				},
			},
		}
	}

	It("does not report semantically identical policies as changed", func() {
		desired := newPolicyMember()
		existing := newPolicyMember()

		Expect(iamPolicyHasChanges(desired, existing)).To(BeFalse())
	})

	DescribeTable("reports policy changes that require recreation",
		func(change func(*iamv1beta1.IAMPolicyMember)) {
			desired := newPolicyMember()
			existing := newPolicyMember()
			change(existing)

			Expect(iamPolicyHasChanges(desired, existing)).To(BeTrue())
		},
		Entry("when the member changes", func(policy *iamv1beta1.IAMPolicyMember) {
			policy.Spec.Member = "serviceAccount:other"
		}),
		Entry("when the role changes", func(policy *iamv1beta1.IAMPolicyMember) {
			policy.Spec.Role = "roles/storage.objectUser"
		}),
		Entry("when the referenced resource changes", func(policy *iamv1beta1.IAMPolicyMember) {
			policy.Spec.ResourceRef.Name = "other-postgres"
		}),
	)
})
