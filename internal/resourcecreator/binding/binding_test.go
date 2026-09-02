package binding

import (
	"sort"
	"strings"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
)

var _ = Describe("binding resources", func() {
	newBinding := func(name string) *v1.PostgresBinding {
		return &v1.PostgresBinding{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "myteam", UID: types.UID(name)},
			Spec: v1.PostgresBindingSpec{
				Postgres:   "mydb",
				Workload:   v1.PostgresBindingWorkload{Name: "myapp"},
				SecretName: "mydb-myapp-read-client-cert",
				Role:       v1.PostgresBindingRoleRead,
			},
		}
	}

	It("uses the same role reservation for bindings targeting the same login role", func() {
		scheme := runtime.NewScheme()
		Expect(v1.AddToScheme(scheme)).To(Succeed())
		first := newBinding("first")
		second := newBinding("second")
		second.Spec.SecretName = "a-different-client-cert"

		firstLock, err := CreateRoleLock(scheme, first)
		Expect(err).NotTo(HaveOccurred())
		secondLock, err := CreateRoleLock(scheme, second)
		Expect(err).NotTo(HaveOccurred())
		Expect(secondLock.Name).To(Equal(firstLock.Name))
		Expect(secondLock.OwnerReferences).NotTo(Equal(firstLock.OwnerReferences))
	})

	It("uses a valid deterministic label for a maximum-length binding name", func() {
		scheme := runtime.NewScheme()
		Expect(v1.AddToScheme(scheme)).To(Succeed())
		binding := newBinding(strings.Repeat("a", 253))

		secret, err := CreateConfigSecret(scheme, binding)
		Expect(err).NotTo(HaveOccurred())
		label := secret.Labels[nameLabel]
		Expect(label).To(Equal("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-32859a3ab65ac529"))
		Expect(validation.IsValidLabelValue(label)).To(BeEmpty())

		egress, err := CreateEgressNetworkPolicy(scheme, binding)
		Expect(err).NotTo(HaveOccurred())
		Expect(egress.Name).To(HaveLen(253))
		Expect(egress.Name).To(HaveSuffix("-ace6074bdfd5c870-egress"))
		Expect(validation.IsDNS1123Subdomain(egress.Name)).To(BeEmpty())
	})

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
