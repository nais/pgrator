package v1

import (
	"context"

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
				DatabaseRoleName: name + "-admin",
				Role:             PostgresBindingRoleAdmin,
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

	It("rejects an update that would create a second admin binding", func() {
		existing := adminBinding("migrator", "mydb")
		oldBinding := adminBinding("application", "mydb")
		oldBinding.Spec.Role = PostgresBindingRoleReadWrite
		newBinding := oldBinding.DeepCopy()
		newBinding.Spec.Role = PostgresBindingRoleAdmin
		_, err := newValidator(existing).ValidateUpdate(context.Background(), oldBinding, newBinding)
		Expect(err).To(HaveOccurred())
	})
})
