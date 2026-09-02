package action

import (
	"context"

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

var _ = Describe("ExclusiveCreate action", func() {
	var (
		scheme       *runtime.Scheme
		ownerManager *mockOwnerManager
	)

	BeforeEach(func() {
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		Expect(v1.AddToScheme(scheme)).To(Succeed())
		ownerManager = &mockOwnerManager{}
	})

	newOwner := func(name string) *v1.PostgresBinding {
		return &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "myteam", UID: types.UID(name)}}
	}

	newLock := func(owner *v1.PostgresBinding) *corev1.ConfigMap {
		controller := true
		return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name:      "postgresbinding-admin-lock",
			Namespace: "myteam",
			OwnerReferences: []metav1.OwnerReference{{
				Name: owner.Name, UID: owner.UID, Controller: &controller,
			}},
		}}
	}

	It("allows the owner that created the lock to reconcile it", func() {
		owner := newOwner("first")
		lock := newLock(owner)
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(lock).Build()

		Expect(ExclusiveCreate(newLock(owner), owner).Do(context.Background(), k8sClient, scheme, ownerManager)).To(Succeed())
	})

	It("rejects another owner of the same lock", func() {
		firstOwner := newOwner("first")
		secondOwner := newOwner("second")
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newLock(firstOwner)).Build()

		err := ExclusiveCreate(newLock(secondOwner), secondOwner).Do(context.Background(), k8sClient, scheme, ownerManager)
		Expect(err).To(MatchError(ContainSubstring(`already claimed by "first"`)))
	})

	It("creates an unclaimed lock", func() {
		owner := newOwner("first")
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		Expect(ExclusiveCreate(newLock(owner), owner).Do(context.Background(), k8sClient, scheme, ownerManager)).To(Succeed())
		Expect(k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "myteam", Name: "postgresbinding-admin-lock"}, &corev1.ConfigMap{})).To(Succeed())
	})

	It("allows exactly one of two concurrent owners to claim a lock", func() {
		first := newOwner("first")
		second := newOwner("second")
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		errors := make(chan error, 2)
		start := make(chan struct{})
		for _, owner := range []*v1.PostgresBinding{first, second} {
			go func() {
				<-start
				errors <- ExclusiveCreate(newLock(owner), owner).Do(context.Background(), k8sClient, scheme, ownerManager)
			}()
		}
		close(start)

		results := []error{<-errors, <-errors}
		Expect(results).To(ContainElement(Succeed()))
		Expect(results).To(ContainElement(MatchError(ContainSubstring("already claimed by"))))
	})
})
