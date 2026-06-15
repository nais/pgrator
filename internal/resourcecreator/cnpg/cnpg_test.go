package cnpg

import (
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/nais/pgrator/internal/config"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
)

const (
	clusterName         = "my-db"
	namespace           = "my-team"
	walBucketPrefix     = "my-backup-bucket"
	gsaName             = "gsa-name"
	teamGoogleProjectID = "team-google-project-id"
	storageBucketName   = "storage-bucket-name"
)

var _ = Describe("CNPG Resource Creator", func() {
	var (
		postgres *data_nais_io_v1.Postgres
		cfg      *config.Config
	)

	BeforeEach(func() {
		postgres = &data_nais_io_v1.Postgres{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: namespace,
			},
			Spec: data_nais_io_v1.PostgresSpec{
				Cluster: data_nais_io_v1.PostgresCluster{
					MajorVersion:     "18",
					HighAvailability: false,
					Resources: data_nais_io_v1.PostgresResources{
						DiskSize: resource.MustParse("10Gi"),
						Cpu:      resource.MustParse("500m"),
						Memory:   resource.MustParse("1Gi"),
					},
				},
			},
		}
		cfg = &config.Config{
			GoogleProjectID: "my-gcp-project",
			CNPG: config.CNPG{
				ImageCatalogName: "postgresql",
				StorageClass:     "hyperdisk-balanced",
				WalBucketPrefix:  walBucketPrefix,
			},
		}
	})

	Describe("CreateClusterSpec", func() {
		It("should create a valid cluster with default settings", func() {
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).NotTo(HaveOccurred())
			Expect(cluster).NotTo(BeNil())

			Expect(cluster.Name).To(Equal(clusterName))
			Expect(cluster.Namespace).To(Equal(namespace))
			Expect(cluster.Spec.Instances).To(Equal(2))
			Expect(cluster.Spec.MinSyncReplicas).To(Equal(0))
			Expect(cluster.Spec.MaxSyncReplicas).To(Equal(0))
		})

		It("should set HA instances when HighAvailability is true", func() {
			postgres.Spec.Cluster.HighAvailability = true
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).NotTo(HaveOccurred())

			Expect(cluster.Spec.Instances).To(Equal(3))
			Expect(cluster.Spec.MinSyncReplicas).To(Equal(1))
			Expect(cluster.Spec.MaxSyncReplicas).To(Equal(1))
		})

		It("should use ImageCatalogRef with correct major version", func() {
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).NotTo(HaveOccurred())

			Expect(cluster.Spec.ImageCatalogRef).NotTo(BeNil())
			Expect(cluster.Spec.ImageCatalogRef.Major).To(Equal(18))
			Expect(cluster.Spec.ImageCatalogRef.Name).To(Equal("postgresql"))
			Expect(cluster.Spec.ImageCatalogRef.Kind).To(Equal("ClusterImageCatalog"))
		})

		It("should return error for invalid major version", func() {
			postgres.Spec.Cluster.MajorVersion = "invalid"
			_, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid major version"))
		})

		It("should set storage class when configured", func() {
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).NotTo(HaveOccurred())

			Expect(cluster.Spec.StorageConfiguration.StorageClass).NotTo(BeNil())
			Expect(*cluster.Spec.StorageConfiguration.StorageClass).To(Equal("hyperdisk-balanced"))
		})

		It("should leave storage class nil when not configured", func() {
			cfg.CNPG.StorageClass = ""
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).NotTo(HaveOccurred())

			Expect(cluster.Spec.StorageConfiguration.StorageClass).To(BeNil())
		})

		It("should set ServiceAccountTemplate with google service account annotation", func() {
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).NotTo(HaveOccurred())

			Expect(cluster.Spec.ServiceAccountName).To(BeEmpty())
			Expect(cluster.Spec.ServiceAccountTemplate).NotTo(BeNil())
			Expect(cluster.Spec.ServiceAccountTemplate.Metadata.Name).To(Equal(clusterName))
			Expect(cluster.Spec.ServiceAccountTemplate.Metadata.Annotations["iam.gke.io/gcp-service-account"]).To(ContainSubstring(gsaName))
			Expect(cluster.Spec.ServiceAccountTemplate.Metadata.Annotations["iam.gke.io/gcp-service-account"]).To(ContainSubstring(teamGoogleProjectID))
			Expect(cluster.Spec.ServiceAccountTemplate.Metadata.Annotations["iam.gke.io/gcp-service-account"]).To(HaveSuffix("iam.gserviceaccount.com"))
		})

		It("should configure barman-cloud plugin when bucketname is given", func() {
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).NotTo(HaveOccurred())

			Expect(cluster.Spec.Plugins).To(HaveLen(1))
			Expect(cluster.Spec.Plugins[0].Name).To(Equal(barmanPluginName))
			Expect(*cluster.Spec.Plugins[0].IsWALArchiver).To(BeTrue())
		})

		It("should not configure plugins when bucketname is empty", func() {
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, "")
			Expect(err).NotTo(HaveOccurred())

			Expect(cluster.Spec.Plugins).To(BeEmpty())
		})

		It("should not set collation from spec", func() {
			postgres.Spec.Database = &data_nais_io_v1.PostgresDatabase{
				Collation: "nb_NO",
			}
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).NotTo(HaveOccurred())

			Expect(cluster.Spec.Bootstrap.InitDB.LocaleCollate).To(BeEmpty())
			Expect(cluster.Spec.Bootstrap.InitDB.LocaleCType).To(BeEmpty())
		})

		It("should enforce minimum 4Gi disk for hyperdisk-balanced", func() {
			postgres.Spec.Cluster.Resources.DiskSize = resource.MustParse("500Mi")
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).NotTo(HaveOccurred())

			Expect(cluster.Spec.StorageConfiguration.Size).To(Equal("4Gi"))
		})

		It("should disable superuser access", func() {
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).NotTo(HaveOccurred())

			Expect(cluster.Spec.EnableSuperuserAccess).NotTo(BeNil())
			Expect(*cluster.Spec.EnableSuperuserAccess).To(BeFalse())
		})

		It("should set node affinity for postgres nodes", func() {
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).NotTo(HaveOccurred())

			Expect(cluster.Spec.Affinity.NodeSelector).To(HaveKeyWithValue("cloud.google.com/compute-class", "n4-machines"))
			Expect(*cluster.Spec.Affinity.EnablePodAntiAffinity).To(BeTrue())
		})

		It("should set memory limit to 4x request", func() {
			cluster, err := CreateClusterSpec(postgres, cfg, clusterName, namespace, gsaName, teamGoogleProjectID, storageBucketName)
			Expect(err).NotTo(HaveOccurred())

			memLimit := cluster.Spec.Resources.Limits.Memory()
			Expect(memLimit.String()).To(Equal("4Gi"))
		})
	})

	Describe("CreateScheduledBackup", func() {
		It("should create a backup targeting the correct cluster", func() {
			backup := CreateScheduledBackup(postgres, clusterName, namespace)
			Expect(backup.Spec.Cluster.Name).To(Equal(clusterName))
			Expect(backup.Spec.Method).To(Equal(cnpgv1.BackupMethodPlugin))
			Expect(backup.Spec.PluginConfiguration.Name).To(Equal(barmanPluginName))
		})
	})

	Describe("CreatePooler", func() {
		It("should create a pooler with PgBouncer in transaction mode", func() {
			pooler := CreatePooler(postgres, clusterName, namespace)
			Expect(pooler.Name).To(Equal(fmt.Sprintf("%s-pooler", clusterName)))
			Expect(pooler.Namespace).To(Equal(namespace))
			Expect(pooler.Spec.Cluster.Name).To(Equal(clusterName))
			Expect(pooler.Spec.PgBouncer.PoolMode).To(Equal(cnpgv1.PgBouncerPoolModeTransaction))
			Expect(*pooler.Spec.Instances).To(Equal(int32(2)))
		})
	})
})
