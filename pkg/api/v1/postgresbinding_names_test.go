package v1

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PostgresBinding resource names", func() {
	It("strips the CNPG suffix from the DatabaseRole name", func() {
		binding := &PostgresBinding{Spec: PostgresBindingSpec{
			SecretName: "mydb-myapp-readwrite-client-cert",
		}}

		Expect(binding.DatabaseRoleName()).To(Equal("mydb-myapp-readwrite"))
		Expect(binding.ClientCertSecretName()).To(Equal("mydb-myapp-readwrite-client-cert"))
	})

	It("keeps every derived name within the Kubernetes DNS-subdomain limit", func() {
		binding := &PostgresBinding{Spec: PostgresBindingSpec{
			SecretName: strings.Repeat("a", 241) + "-client-cert",
		}}

		Expect(binding.DatabaseRoleName()).To(HaveLen(241))
		Expect(binding.ClientCertSecretName()).To(HaveLen(253))
		Expect(binding.ConfigSecretName()).To(HaveLen(250))
		Expect(binding.CASecretName()).To(HaveLen(253))
	})
})
