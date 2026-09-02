package action

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("ExclusiveCreateOrUpdate action", func() {
	var scheme *runtime.Scheme

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(cnpgv1.AddToScheme(scheme)).To(Succeed())
		Expect(v1.AddToScheme(scheme)).To(Succeed())
	})

	newOwner := func(name string) *v1.PostgresBinding {
		return &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "myteam", UID: types.UID(name),
		}}
	}
	newSecret := func(owner *v1.PostgresBinding, value string) *corev1.Secret {
		controller := true
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: "binding-config", Namespace: "myteam",
				OwnerReferences: []metav1.OwnerReference{{
					Name: owner.Name, UID: owner.UID, Controller: &controller,
				}},
			},
			StringData: map[string]string{"value": value},
		}
	}
	perform := func(k8sClient client.Client, desired *corev1.Secret, owner *v1.PostgresBinding) error {
		return ExclusiveCreateOrUpdate(desired, owner, func(client.Object, *runtime.Scheme) []metav1.Condition {
			return nil
		}, &mockRecorder{}).Do(context.Background(), k8sClient, scheme, &mockOwnerManager{})
	}

	It("creates an unclaimed child", func() {
		owner := newOwner("first")
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		Expect(perform(k8sClient, newSecret(owner, "initial"), owner)).To(Succeed())
		created := &corev1.Secret{}
		Expect(k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "myteam", Name: "binding-config"}, created)).To(Succeed())
		Expect(created.StringData).To(HaveKeyWithValue("value", "initial"))
	})

	It("updates a child already owned by the binding", func() {
		owner := newOwner("first")
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newSecret(owner, "initial")).Build()

		Expect(perform(k8sClient, newSecret(owner, "updated"), owner)).To(Succeed())
		updated := &corev1.Secret{}
		Expect(k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "myteam", Name: "binding-config"}, updated)).To(Succeed())
		Expect(updated.StringData).To(HaveKeyWithValue("value", "updated"))
	})

	It("rejects a child controlled by another binding without changing it", func() {
		first := newOwner("first")
		second := newOwner("second")
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newSecret(first, "initial")).Build()

		err := perform(k8sClient, newSecret(second, "hijacked"), second)
		Expect(err).To(MatchError(ContainSubstring(`already claimed by "first"`)))
		existing := &corev1.Secret{}
		Expect(k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "myteam", Name: "binding-config"}, existing)).To(Succeed())
		Expect(existing.StringData).To(HaveKeyWithValue("value", "initial"))
	})

	controller := true
	roleFor := func(owner *v1.PostgresBinding, comment string) *cnpgv1.DatabaseRole {
		return &cnpgv1.DatabaseRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: "shared-role-resource", Namespace: "myteam",
				OwnerReferences: []metav1.OwnerReference{{
					Name: owner.Name, UID: owner.UID, Controller: &controller,
				}},
			},
			Spec: cnpgv1.DatabaseRoleSpec{RoleConfiguration: cnpgv1.RoleConfiguration{Comment: comment}},
		}
	}
	performRole := func(k8sClient client.Client, desired *cnpgv1.DatabaseRole, owner *v1.PostgresBinding) error {
		return ExclusiveCreateOrUpdate(desired, owner, func(client.Object, *runtime.Scheme) []metav1.Condition {
			return nil
		}, &mockRecorder{}).Do(context.Background(), k8sClient, scheme, &mockOwnerManager{})
	}

	It("updates a DatabaseRole already owned by the binding", func() {
		owner := newOwner("first")
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(roleFor(owner, "initial")).Build()

		Expect(performRole(k8sClient, roleFor(owner, "updated"), owner)).To(Succeed())
		updated := &cnpgv1.DatabaseRole{}
		Expect(k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "myteam", Name: "shared-role-resource"}, updated)).To(Succeed())
		Expect(updated.Spec.Comment).To(Equal("updated"))
	})

	It("rejects the same DatabaseRole name from another binding", func() {
		first := newOwner("first")
		second := newOwner("second")
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(roleFor(first, "initial")).Build()

		err := performRole(k8sClient, roleFor(second, "hijacked"), second)
		Expect(err).To(MatchError(ContainSubstring(`already claimed by "first"`)))
	})

	It("rejects an ownerless existing child", func() {
		owner := newOwner("first")
		existing := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "binding-config", Namespace: "myteam"}}
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

		err := perform(k8sClient, newSecret(owner, "hijacked"), owner)
		Expect(err).To(MatchError(ContainSubstring("already exists without a controller owner")))
	})
})
