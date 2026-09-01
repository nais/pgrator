package v1

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PostgresBinding resource names", func() {
	It("keeps every derived name within the Kubernetes DNS-subdomain limit", func() {
		binding := &PostgresBinding{Spec: PostgresBindingSpec{
			DatabaseRoleName: strings.Repeat("a", 241),
		}}

		Expect(binding.ClientCertSecretName()).To(HaveLen(253))
		Expect(binding.ConfigSecretName()).To(HaveLen(250))
		Expect(binding.CASecretName()).To(HaveLen(253))
	})
})
