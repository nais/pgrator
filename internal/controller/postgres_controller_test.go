package controller

import (
	"context"

	"github.com/nais/pgrator/internal/synchronizer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
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
		const postgresNamespace = "pg-default"

		ctx := context.Background()

		resourceKey := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		postgres := &data_nais_io_v1.Postgres{}

		namespaceKey := types.NamespacedName{
			Name: postgresNamespace,
		}
		namespace := &v1.Namespace{}

		BeforeEach(func() {
			By("creating the postgres namespace")
			err := k8sClient.Get(ctx, namespaceKey, namespace)
			if err != nil && errors.IsNotFound(err) {
				namespace := &v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: postgresNamespace,
					},
				}
				Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			}

			By("creating the custom resource for the Kind Postgres")
			err = k8sClient.Get(ctx, resourceKey, postgres)
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

		When("the resource is created", func() {
			AfterEach(func() {
				resource := &data_nais_io_v1.Postgres{}
				err := k8sClient.Get(ctx, resourceKey, resource)
				Expect(err).NotTo(HaveOccurred())

				By("Cleanup the specific resource instance Postgres")
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			})

			It("should successfully reconcile the resource", func() {
				By("Reconciling the created resource")
				controllerReconciler := synchronizer.NewSynchronizer(k8sClient, k8sClient.Scheme(), &PostgresReconciler{})

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: resourceKey,
				})
				Expect(err).NotTo(HaveOccurred())

				// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
				// Example: If you expect a certain status condition after reconciliation, verify it here.
			})
		})

		When("the resource is deleted", func() {
			BeforeEach(func() {
				resource := &data_nais_io_v1.Postgres{}
				err := k8sClient.Get(ctx, resourceKey, resource)
				Expect(err).NotTo(HaveOccurred())

				By("Delete the resource")
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			})

			It("should successfully clean up dependent resources", func() {
				By("Reconciling the deleted resource")
				controllerReconciler := synchronizer.NewSynchronizer(k8sClient, k8sClient.Scheme(), &PostgresReconciler{})

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: resourceKey,
				})
				Expect(err).NotTo(HaveOccurred())

				// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
				// Example: If you expect a certain status condition after reconciliation, verify it here.
			})
		})
	})
})
