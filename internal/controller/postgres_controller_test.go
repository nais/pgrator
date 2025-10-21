package controller

import (
	"context"

	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/synchronizer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	core_v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("Postgres Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"
		const postgresNamespace = "pg-default"
		const undeletableName = "undeletable-resource"
		var reconcilerConfig config.Config

		ctx := context.Background()

		resourceKey := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		undeletableResourceKey := types.NamespacedName{
			Name:      undeletableName,
			Namespace: "default",
		}

		postgres := &data_nais_io_v1.Postgres{}

		clusterKey := types.NamespacedName{
			Name:      resourceName,
			Namespace: postgresNamespace,
		}

		undeletableClusterKey := types.NamespacedName{
			Name:      undeletableName,
			Namespace: postgresNamespace,
		}

		namespaceKey := types.NamespacedName{
			Name: postgresNamespace,
		}
		namespace := &core_v1.Namespace{}

		BeforeEach(func() {
			By("creating the postgres namespace")
			err := k8sClient.Get(ctx, namespaceKey, namespace)
			if err != nil && apierrors.IsNotFound(err) {
				namespace := &core_v1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: postgresNamespace,
					},
				}
				Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			}

			By("creating the custom resource for the Kind Postgres")
			err = k8sClient.Get(ctx, resourceKey, postgres)
			if err != nil && apierrors.IsNotFound(err) {
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
							MajorVersion:  "17",
							AllowDeletion: true,
						},
					},
				}
				err = k8sClient.Create(ctx, resource)
				Expect(err).To(Succeed())
			}
			Expect(err).NotTo(HaveOccurred())

			By("creating an undeletable resource for the Kind Postgres")
			err = k8sClient.Get(ctx, undeletableResourceKey, postgres)
			if err != nil && apierrors.IsNotFound(err) {
				resource := &data_nais_io_v1.Postgres{
					ObjectMeta: metav1.ObjectMeta{
						Name:      undeletableName,
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
				err = k8sClient.Create(ctx, resource)
				Expect(err).To(Succeed())
			}
			Expect(err).NotTo(HaveOccurred())
		})

		When("the resource is created", func() {

			var controllerReconciler *synchronizer.Synchronizer[*data_nais_io_v1.Postgres, PreparedData]

			AfterEach(func() {
				resource := &data_nais_io_v1.Postgres{}
				err := k8sClient.Get(ctx, resourceKey, resource)
				Expect(err).NotTo(HaveOccurred())

				By("Cleanup the specific resource instance Postgres")
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				controllerReconciler = synchronizer.NewSynchronizer(k8sClient, k8sClient.Scheme(), &PostgresReconciler{Config: &reconcilerConfig})
				_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: resourceKey,
				})
			})

			It("should successfully reconcile the resource", func() {
				By("Reconciling the created resource")
				controllerReconciler := synchronizer.NewSynchronizer(k8sClient, k8sClient.Scheme(), &PostgresReconciler{Config: &reconcilerConfig})

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: resourceKey,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Checking for creation of dependent resource")
				cluster := &acid_zalan_do_v1.Postgresql{}
				err = k8sClient.Get(ctx, clusterKey, cluster)
				Expect(err).NotTo(HaveOccurred())

				// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
				// Example: If you expect a certain status condition after reconciliation, verify it here.
			})
		})

		When("the resource is deleted", func() {

			var controllerReconciler *synchronizer.Synchronizer[*data_nais_io_v1.Postgres, PreparedData]

			BeforeEach(func() {
				By("Ensure the resource is reconciled before deletion")
				controllerReconciler = synchronizer.NewSynchronizer(k8sClient, k8sClient.Scheme(), &PostgresReconciler{Config: &reconcilerConfig})
				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: resourceKey,
				})
				Expect(err).NotTo(HaveOccurred())

				_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: undeletableResourceKey,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Delete undeletable resource")
				resource := &data_nais_io_v1.Postgres{}
				err = k8sClient.Get(ctx, undeletableResourceKey, resource)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

				By("Delete the resource")
				resource = &data_nais_io_v1.Postgres{}
				err = k8sClient.Get(ctx, resourceKey, resource)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			})

			It("should successfully clean up dependent resources", func() {
				By("Reconciling the deleted resource")

				_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: resourceKey,
				})
				Expect(err).NotTo(HaveOccurred())

				_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: undeletableResourceKey,
				})
				Expect(err).NotTo(HaveOccurred())

				By("Checking that dependent resource is no longer found")
				cluster := &acid_zalan_do_v1.Postgresql{}
				err = k8sClient.Get(ctx, clusterKey, cluster)
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				By("Checking that undeletable resource is still present")
				cluster = &acid_zalan_do_v1.Postgresql{}
				err = k8sClient.Get(ctx, undeletableClusterKey, cluster)
				Expect(err).NotTo(HaveOccurred())

				// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
				// Example: If you expect a certain status condition after reconciliation, verify it here.
			})
		})
	})
})
