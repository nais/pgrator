package controller

import (
	"context"

	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	iam_google_v1beta1 "github.com/nais/liberator/pkg/apis/iam.cnrm.cloud.google.com/v1beta1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/synchronizer"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	core_v1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	resourceNamespace        = "default"
	postgresNamespace        = "pg-default"
	deletableName            = "deletable-resource"
	undeletableName          = "undeletable-resource"
	serviceAccountsNamespace = "serviceaccounts"
)

var (
	deletableResourceKey = types.NamespacedName{
		Name:      deletableName,
		Namespace: resourceNamespace,
	}

	undeletableResourceKey = types.NamespacedName{
		Name:      undeletableName,
		Namespace: resourceNamespace,
	}

	deletableClusterKey = types.NamespacedName{
		Name:      deletableName,
		Namespace: postgresNamespace,
	}

	undeletableClusterKey = types.NamespacedName{
		Name:      undeletableName,
		Namespace: postgresNamespace,
	}
)

var _ = Describe("Postgres Controller", func() {
	Context("When reconciling a resource", func() {
		reconcilerConfig := config.Config{
			PrometheusRulesDisabled: true,
		}
		var controllerReconciler *synchronizer.Synchronizer[*data_nais_io_v1.Postgres, PreparedData]

		ctx := context.Background()

		BeforeEach(func() {
			By("creating the synchronizer for postgres")
			controllerReconciler = synchronizer.NewSynchronizer(k8sClient, k8sClient.Scheme(), &PostgresReconciler{Config: &reconcilerConfig})

			By("creating the postgres namespace")
			ensureNamespaceExists(postgresNamespace)

			By("creating the serviceaccounts namespace")
			ensureNamespaceExists(serviceAccountsNamespace)

			By("creating the custom resource for the Kind Postgres")
			ensurePostgresExists(deletableResourceKey, true)

			By("creating an undeletable resource for the Kind Postgres")
			ensurePostgresExists(undeletableResourceKey, false)
		})

		When("the resource is created", func() {
			AfterEach(func() {
				resource := &data_nais_io_v1.Postgres{}
				err := k8sClient.Get(ctx, deletableResourceKey, resource)
				Expect(err).NotTo(HaveOccurred())

				By("Cleanup the specific resource instance Postgres")
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
				_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: deletableResourceKey,
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("should successfully reconcile the resource", func() {
				By("Reconciling the created resource")
				ensureReconciled(deletableResourceKey, controllerReconciler)

				By("Checking for creation of dependent resource")
				cluster := &acid_zalan_do_v1.Postgresql{}
				err := k8sClient.Get(ctx, deletableClusterKey, cluster)
				Expect(err).NotTo(HaveOccurred())

				netpol := &v1.NetworkPolicy{}
				err = k8sClient.Get(ctx, deletableClusterKey, netpol)
				Expect(err).NotTo(HaveOccurred())

				iamList := &iam_google_v1beta1.IAMPolicyMemberList{}
				err = k8sClient.List(ctx, iamList, client.InNamespace(serviceAccountsNamespace))
				Expect(err).NotTo(HaveOccurred())
				Expect(iamList.Items).NotTo(BeEmpty())

				// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
				// Example: If you expect a certain status condition after reconciliation, verify it here.
			})
		})

		When("the resource is deleted", func() {
			It("should successfully clean up dependent resources when deletion is allowed", func() {
				By("Ensure the resource is reconciled before deletion")
				ensureReconciled(deletableResourceKey, controllerReconciler)

				By("Delete the resource")
				resource := &data_nais_io_v1.Postgres{}
				err := k8sClient.Get(ctx, deletableResourceKey, resource)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

				By("Reconcile the deleted resource")
				ensureReconciled(deletableResourceKey, controllerReconciler)

				By("Checking that the resource is deleted")
				test := &data_nais_io_v1.Postgres{}
				err = k8sClient.Get(ctx, deletableResourceKey, test)
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				By("Checking that deletable cluster is no longer found")
				cluster := &acid_zalan_do_v1.Postgresql{}
				err = k8sClient.Get(ctx, deletableClusterKey, cluster)
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				By("Checking that dependent resources are deleted")
				netpol := &v1.NetworkPolicy{}
				err = k8sClient.Get(ctx, deletableClusterKey, netpol)
				Expect(err).To(HaveOccurred())
				Expect(apierrors.IsNotFound(err)).To(BeTrue())

				iamList := &iam_google_v1beta1.IAMPolicyMemberList{}
				err = k8sClient.List(ctx, iamList, client.InNamespace(serviceAccountsNamespace))
				Expect(err).NotTo(HaveOccurred())
				Expect(iamList.Items).NotTo(BeEmpty())

				// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
				// Example: If you expect a certain status condition after reconciliation, verify it here.
			})

			It("should orphan dependent resources when deletion is not allowed", func() {
				By("Ensure the resource is reconciled before deletion")
				ensureReconciled(undeletableResourceKey, controllerReconciler)

				By("Delete undeletable resource")
				resource := &data_nais_io_v1.Postgres{}
				err := k8sClient.Get(ctx, undeletableResourceKey, resource)
				Expect(err).NotTo(HaveOccurred())
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

				By("Reconcile the deleted resource")
				ensureReconciled(undeletableResourceKey, controllerReconciler)

				By("Checking that undeletable cluster is still present")
				cluster := &acid_zalan_do_v1.Postgresql{}
				err = k8sClient.Get(ctx, undeletableClusterKey, cluster)
				Expect(err).NotTo(HaveOccurred())

				By("Checking dependent resources are still present")
				netpol := &v1.NetworkPolicy{}
				err = k8sClient.Get(ctx, undeletableClusterKey, netpol)
				Expect(err).NotTo(HaveOccurred())

				iamList := &iam_google_v1beta1.IAMPolicyMemberList{}
				err = k8sClient.List(ctx, iamList, client.InNamespace(serviceAccountsNamespace))
				Expect(err).NotTo(HaveOccurred())
				Expect(iamList.Items).NotTo(BeEmpty())

				// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
				// Example: If you expect a certain status condition after reconciliation, verify it here.
			})
		})
	})
})

func ensureReconciled(key types.NamespacedName, controllerReconciler *synchronizer.Synchronizer[*data_nais_io_v1.Postgres, PreparedData]) {
	_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
		NamespacedName: key,
	})
	Expect(err).NotTo(HaveOccurred())
}

func ensurePostgresExists(key types.NamespacedName, allowDeletion bool) {
	postgres := &data_nais_io_v1.Postgres{}
	err := k8sClient.Get(ctx, key, postgres)
	if err != nil && apierrors.IsNotFound(err) {
		postgres = &data_nais_io_v1.Postgres{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Spec: data_nais_io_v1.PostgresSpec{
				Cluster: data_nais_io_v1.PostgresCluster{
					Resources: data_nais_io_v1.PostgresResources{
						DiskSize: resource.MustParse("1G"),
						Cpu:      resource.MustParse("1"),
						Memory:   resource.MustParse("1G"),
					},
					MajorVersion:  "17",
					AllowDeletion: allowDeletion,
				},
			},
		}
		err = k8sClient.Create(ctx, postgres)
		Expect(err).To(Succeed())
	}
	Expect(err).NotTo(HaveOccurred())
}

func ensureNamespaceExists(name string) {
	namespace := &core_v1.Namespace{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, namespace)
	if err != nil && apierrors.IsNotFound(err) {
		namespace = &core_v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
			},
		}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
	}
}
