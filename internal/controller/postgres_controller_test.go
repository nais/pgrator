package controller

import (
	"context"

	"github.com/nais/pgrator/internal/synchronizer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
)

var _ = Describe("Postgres Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		postgres := &data_nais_io_v1.Postgres{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Postgres")
			err := k8sClient.Get(ctx, typeNamespacedName, postgres)
			if err != nil && errors.IsNotFound(err) {
				resource := &data_nais_io_v1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: data_nais_io_v1.PostgresSpec{
						Cluster: data_nais_io_v1.PostgresCluster{
							Resources: data_nais_io_v1.PostgresResources{
								DiskSize: resource.MustParse("1G"),
								Cpu:      resource.MustParse("1"),
								Memory:   resource.MustParse("1G"),
							},
							MajorVersion: "17",
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &data_nais_io_v1.Postgres{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Postgres")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := synchronizer.NewSynchronizer[
				*data_nais_io_v1.Postgres,
				PreparedData,
			](k8sClient, &PostgresReconciler{})

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})
