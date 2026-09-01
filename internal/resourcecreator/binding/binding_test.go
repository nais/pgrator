package binding

import (
	"sort"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var _ = Describe("connection config Secret", func() {
	It("uses role-specific environment variable prefixes", func() {
		scheme := runtime.NewScheme()
		Expect(v1.AddToScheme(scheme)).To(Succeed())

		for role, prefix := range map[v1.PostgresBindingRole]string{
			v1.PostgresBindingRoleAdmin:     "",
			v1.PostgresBindingRoleRead:      "READ_",
			v1.PostgresBindingRoleReadWrite: "READWRITE_",
		} {
			binding := &v1.PostgresBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "mybinding", Namespace: "myteam"},
				Spec: v1.PostgresBindingSpec{
					Postgres: "mydb",
					Workload: v1.PostgresBindingWorkload{Name: "myapp"},
					Role:     role,
				},
			}
			secret, err := CreateConfigSecret(scheme, binding)
			Expect(err).NotTo(HaveOccurred())

			keys := make([]string, 0, len(secret.StringData))
			for key := range secret.StringData {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			Expect(keys).To(Equal([]string{
				prefix + "PGDATABASE",
				prefix + "PGHOST",
				prefix + "PGPORT",
				prefix + "PGSSLMODE",
				prefix + "PGUSER",
			}))
		}
	})
})
