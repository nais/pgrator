package v1

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PostgresBinding resource names", func() {
	DescribeTable("uses distinct PostgreSQL login roles",
		func(role PostgresBindingRole, workload, expected string) {
			binding := &PostgresBinding{Spec: PostgresBindingSpec{
				Workload: PostgresBindingWorkload{Name: workload},
				Role:     role,
			}}

			Expect(binding.RoleName()).To(Equal(expected))
			Expect(len(binding.RoleName())).To(BeNumerically("<=", 63))
		},
		Entry("read", PostgresBindingRoleRead, "reporter", "reporter-read"),
		Entry("readwrite", PostgresBindingRoleReadWrite, "reporter", "reporter-readwrite"),
		Entry("admin", PostgresBindingRoleAdmin, "reporter", "app"),
		Entry("long read workload", PostgresBindingRoleRead, strings.Repeat("a", 63), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-6c913093f3d95ca4-read"),
		Entry("long readwrite workload", PostgresBindingRoleReadWrite, strings.Repeat("a", 63), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-3c51106b347a274d-readwrite"),
	)

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
	})
})
