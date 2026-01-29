package controller

import (
	"context"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/synchronizer"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/pkg/api/thirdparty/google/v1beta1"
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
	resourceNamespace        = "team"
	postgresNamespace        = "pg-team"
	serviceAccountsNamespace = "serviceaccounts"
	deletableName            = "deletable-resource"
	undeletableName          = "undeletable-resource"
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
			GoogleProjectID:         "cluster-project",
		}
		var postgresController *PostgresReconciler
		var controllerReconciler *synchronizer.Synchronizer[*data_nais_io_v1.Postgres, PreparedData]

		ctx := context.Background()

		BeforeEach(func() {
			By("creating the synchronizer for postgres")
			postgresController = &PostgresReconciler{Config: &reconcilerConfig, Recorder: recorder, Scheme: k8sClient.Scheme()}
			controllerReconciler = synchronizer.NewSynchronizer(k8sClient, k8sClient.Scheme(), postgresController, recorder)

			By("creating the resource namespace")
			ensureNamespaceExists(resourceNamespace, "test-project")

			By("creating the serviceaccounts namespace")
			ensureNamespaceExists(serviceAccountsNamespace, "cluster-project")

			By("creating the postgres namespace")
			ensureNamespaceExists(postgresNamespace, "test-project")

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
				ensureReconciled(undeletableResourceKey, controllerReconciler)

				By("Checking for creation of dependent resource")
				cluster := &acid_zalan_do_v1.Postgresql{}
				err := k8sClient.Get(ctx, deletableClusterKey, cluster)
				Expect(err).NotTo(HaveOccurred())

				netpol := &v1.NetworkPolicy{}
				err = k8sClient.Get(ctx, deletableClusterKey, netpol)
				Expect(err).NotTo(HaveOccurred())

				iamList := &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMemberList{}
				err = k8sClient.List(ctx, iamList, client.InNamespace(resourceNamespace))
				Expect(err).NotTo(HaveOccurred())
				Expect(iamList.Items).NotTo(BeEmpty())

				sa := &core_v1.ServiceAccount{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: "postgres-pod", Namespace: postgresNamespace}, sa)
				Expect(sa.Annotations["iam.gke.io/gcp-service-account"]).To(Equal("postgres-pod@test-project.iam.gserviceaccount.com"))
				Expect(err).NotTo(HaveOccurred())
				Expect(controllerReconciler.GetOwnerAnnotations(sa)).To(ContainElement(deletableResourceKey.String()))
				Expect(controllerReconciler.GetOwnerAnnotations(sa)).To(ContainElement(undeletableResourceKey.String()))

				iamsa := &iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: "postgres-pod", Namespace: resourceNamespace}, iamsa)
				Expect(err).NotTo(HaveOccurred())
				Expect(iamsa.Spec.DisplayName).To(Equal("Postgres Pod Service Account for team \"team\""))

				policyMemberWI := &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: "postgres-pod-wi-user", Namespace: resourceNamespace}, policyMemberWI)
				Expect(err).NotTo(HaveOccurred())
				Expect(policyMemberWI.Spec.Member).To(Equal("serviceAccount:cluster-project.svc.id.goog[pg-team/postgres-pod]"))
				Expect(policyMemberWI.Spec.Role).To(Equal("roles/iam.workloadIdentityUser"))
			})
		})

		When("the resource is deleted", func() {
			It("should successfully clean up dependent resources when deletion is allowed", func() {
				By("Ensure the resource is reconciled before deletion")
				ensureReconciled(deletableResourceKey, controllerReconciler)
				ensureReconciled(undeletableResourceKey, controllerReconciler)

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

				By("Checking that shared resource is not deleted")
				iamList := &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMemberList{}
				err = k8sClient.List(ctx, iamList, client.InNamespace(resourceNamespace))
				Expect(err).NotTo(HaveOccurred())
				Expect(iamList.Items).To(HaveLen(1))
				iamPolicyMember := iamList.Items[0]
				Expect(controllerReconciler.GetOwnerAnnotations(&iamPolicyMember)).NotTo(ContainElement(deletableResourceKey.String()))
				Expect(controllerReconciler.HasOwnerAnnotation(&iamPolicyMember, resource)).To(BeFalse())
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

				By("Checking that shared resource is not deleted")
				iamList := &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMemberList{}
				err = k8sClient.List(ctx, iamList, client.InNamespace(resourceNamespace))
				Expect(err).NotTo(HaveOccurred())
				Expect(iamList.Items).To(HaveLen(1))
				iamPolicyMember := iamList.Items[0]
				Expect(controllerReconciler.HasOwnerAnnotation(&iamPolicyMember, resource)).To(BeTrue())
			})
		})
	})

	Context("When reconciling with WalGsBucket configured", func() {
		reconcilerConfig := config.Config{
			PrometheusRulesDisabled: true,
			WalGsBucket:             "postgres-backup-bucket",
			GoogleProjectID:         "cluster-project",
		}
		var controllerReconciler *synchronizer.Synchronizer[*data_nais_io_v1.Postgres, PreparedData]

		ctx := context.Background()

		BeforeEach(func() {
			By("creating the synchronizer for postgres with WalGsBucket")
			controllerReconciler = synchronizer.NewSynchronizer(k8sClient, k8sClient.Scheme(), &PostgresReconciler{Config: &reconcilerConfig, Recorder: recorder, Scheme: k8sClient.Scheme()}, recorder)

			By("ensuring the resource namespace has required labels")
			ensureNamespaceExists(resourceNamespace, "test-project")

			By("creating the serviceaccounts namespace")
			ensureNamespaceExists(serviceAccountsNamespace, "cluster-project")

			By("creating the postgres namespace")
			ensureNamespaceExists(postgresNamespace, "test-project")

			By("creating the custom resource for the Kind Postgres")
			ensurePostgresExists(deletableResourceKey, true)
		})

		It("should create storage bucket IAM policy member", func() {
			By("Reconciling the created resource")
			ensureReconciled(deletableResourceKey, controllerReconciler)

			By("Checking for creation of storage bucket IAM policy member")
			policyMemberStorage := &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "pg-gcs-team-e4138be1", Namespace: serviceAccountsNamespace}, policyMemberStorage)
			Expect(err).NotTo(HaveOccurred())
			Expect(policyMemberStorage.Spec.Member).To(Equal("serviceAccount:postgres-pod@test-project.iam.gserviceaccount.com"))
			Expect(policyMemberStorage.Spec.Role).To(Equal("roles/storage.objectUser"))
			Expect(*policyMemberStorage.Spec.ResourceRef.External).To(Equal("postgres-backup-bucket"))
		})
	})

	Context("When reconciling with ResyncIAMPermissions enabled", func() {
		It("should use CreateOrUpdate for service accounts to ensure they are synced", func() {
			reconcilerConfig := config.Config{
				PrometheusRulesDisabled: true,
				WalGsBucket:             "postgres-backup-bucket",
				GoogleProjectID:         "cluster-project",
				ResyncIAMPermissions:    true,
			}
			reconciler := synchronizer.NewSynchronizer(k8sClient, k8sClient.Scheme(), &PostgresReconciler{Config: &reconcilerConfig, Recorder: recorder, Scheme: k8sClient.Scheme()}, recorder)

			ensureNamespaceExists(resourceNamespace, "test-project")
			ensureNamespaceExists(serviceAccountsNamespace, "cluster-project")
			ensureNamespaceExists(postgresNamespace, "test-project")

			key := types.NamespacedName{Name: "resync-test", Namespace: resourceNamespace}
			ensurePostgresExists(key, true)

			By("cleaning up any existing service account from previous tests")
			existingKsa := &core_v1.ServiceAccount{}
			if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "postgres-pod", Namespace: postgresNamespace}, existingKsa); err == nil {
				Expect(k8sClient.Delete(context.Background(), existingKsa)).To(Succeed())
			}

			By("pre-creating Kubernetes service account with incomplete annotations")
			ksa := &core_v1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "postgres-pod",
					Namespace: postgresNamespace,
				},
			}
			Expect(k8sClient.Create(context.Background(), ksa)).To(Succeed())

			By("reconciling")
			ensureReconciled(key, reconciler)

			By("verifying IAM annotation was added via CreateOrUpdate")
			Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(ksa), ksa)).To(Succeed())
			Expect(ksa.Annotations).To(HaveKey("iam.gke.io/gcp-service-account"))
		})
	})

	Context("When namespace uses annotation instead of label for project ID", func() {
		const annotationNamespace = "annotation-team"
		const annotationPostgresNamespace = "pg-annotation-team"

		reconcilerConfig := config.Config{
			PrometheusRulesDisabled: true,
			GoogleProjectID:         "cluster-project",
		}
		var controllerReconciler *synchronizer.Synchronizer[*data_nais_io_v1.Postgres, PreparedData]

		ctx := context.Background()

		BeforeEach(func() {
			By("creating the synchronizer for postgres")
			controllerReconciler = synchronizer.NewSynchronizer(k8sClient, k8sClient.Scheme(), &PostgresReconciler{Config: &reconcilerConfig, Recorder: recorder, Scheme: k8sClient.Scheme()}, recorder)

			By("creating the resource namespace with annotation instead of label")
			ensureNamespaceWithAnnotationExists(annotationNamespace, "annotation-project")

			By("creating the serviceaccounts namespace")
			ensureNamespaceExists(serviceAccountsNamespace, "cluster-project")

			By("creating the postgres namespace")
			ensureNamespaceWithAnnotationExists(annotationPostgresNamespace, "annotation-project")

			By("creating the custom resource for the Kind Postgres")
			key := types.NamespacedName{Name: "annotation-test", Namespace: annotationNamespace}
			ensurePostgresExists(key, true)
		})

		AfterEach(func() {
			By("Cleanup the resource")
			resource := &data_nais_io_v1.Postgres{}
			key := types.NamespacedName{Name: "annotation-test", Namespace: annotationNamespace}
			err := k8sClient.Get(ctx, key, resource)
			if err == nil {
				Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			}
		})

		It("should successfully reconcile using cnrm.cloud.google.com/project-id annotation", func() {
			key := types.NamespacedName{Name: "annotation-test", Namespace: annotationNamespace}

			By("Reconciling the created resource")
			ensureReconciled(key, controllerReconciler)

			By("Checking that service account was created with correct project ID from annotation")
			sa := &core_v1.ServiceAccount{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "postgres-pod", Namespace: annotationPostgresNamespace}, sa)
			Expect(err).NotTo(HaveOccurred())
			Expect(sa.Annotations["iam.gke.io/gcp-service-account"]).To(Equal("postgres-pod@annotation-project.iam.gserviceaccount.com"))
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

func ensureNamespaceExists(name string, projectID string) {
	namespace := &core_v1.Namespace{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, namespace)
	if err != nil && apierrors.IsNotFound(err) {
		namespace = &core_v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					ProjectIDLabel: projectID,
				},
			},
		}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
	}
}

func ensureNamespaceWithAnnotationExists(name string, projectID string) {
	namespace := &core_v1.Namespace{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: name}, namespace)
	if err != nil && apierrors.IsNotFound(err) {
		namespace = &core_v1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Annotations: map[string]string{
					ProjectIDAnnotationFallback: projectID,
				},
			},
		}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
	}
}
