package v1

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("PostgresBinding webhook validation", func() {
	adminBinding := func(name, postgres string) *PostgresBinding {
		return &PostgresBinding{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "myteam"},
			Spec: PostgresBindingSpec{
				Postgres: postgres,
				Workload: PostgresBindingWorkload{
					Name: name,
					Type: PostgresBindingWorkloadTypeApplication,
				},
				SecretName: name + "-admin-client-cert",
				Role:       PostgresBindingRoleAdmin,
			},
		}
	}

	newValidator := func(objects ...*PostgresBinding) *PostgresBindingValidator {
		scheme := runtime.NewScheme()
		Expect(AddToScheme(scheme)).To(Succeed())
		clientObjects := make([]client.Object, len(objects))
		for i, object := range objects {
			clientObjects[i] = object
		}
		return &PostgresBindingValidator{reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(clientObjects...).Build()}
	}

	It("allows the first admin binding for a Postgres", func() {
		binding := adminBinding("migrator", "mydb")
		_, err := newValidator().ValidateCreate(context.Background(), binding)
		Expect(err).NotTo(HaveOccurred())
	})

	It("allows a maximum-length Kubernetes resource name", func() {
		binding := adminBinding(strings.Repeat("a", 253), "mydb")
		_, err := newValidator().ValidateCreate(context.Background(), binding)
		Expect(err).NotTo(HaveOccurred())
	})

	It("rejects another admin binding for the same Postgres", func() {
		existing := adminBinding("migrator", "mydb")
		candidate := adminBinding("other-migrator", "mydb")
		_, err := newValidator(existing).ValidateCreate(context.Background(), candidate)
		Expect(err).To(MatchError(ContainSubstring(`Postgres "mydb" already has admin binding "migrator"`)))
	})

	It("allows admin bindings for different Postgres resources", func() {
		existing := adminBinding("migrator", "mydb")
		candidate := adminBinding("other-migrator", "otherdb")
		_, err := newValidator(existing).ValidateCreate(context.Background(), candidate)
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("rejects spec identity changes",
		func(change func(*PostgresBinding)) {
			oldBinding := adminBinding("application", "mydb")
			newBinding := oldBinding.DeepCopy()
			change(newBinding)

			_, err := newValidator().ValidateUpdate(context.Background(), oldBinding, newBinding)
			Expect(err).To(MatchError("spec is immutable"))
		},
		Entry("Postgres", func(binding *PostgresBinding) { binding.Spec.Postgres = "otherdb" }),
		Entry("workload name", func(binding *PostgresBinding) { binding.Spec.Workload.Name = "otherapp" }),
		Entry("workload type", func(binding *PostgresBinding) { binding.Spec.Workload.Type = PostgresBindingWorkloadTypeJob }),
		Entry("Secret name", func(binding *PostgresBinding) { binding.Spec.SecretName = "other-client-cert" }),
		Entry("role", func(binding *PostgresBinding) { binding.Spec.Role = PostgresBindingRoleReadWrite }),
	)

	It("allows metadata-only updates", func() {
		oldBinding := adminBinding("application", "mydb")
		newBinding := oldBinding.DeepCopy()
		newBinding.Labels = map[string]string{"updated": "true"}

		_, err := newValidator(oldBinding).ValidateUpdate(context.Background(), oldBinding, newBinding)
		Expect(err).NotTo(HaveOccurred())
	})
})
