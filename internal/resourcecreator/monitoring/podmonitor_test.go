package monitoring

import (
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("PodMonitor", func() {
	var postgres *data_nais_io_v1.Postgres

	BeforeEach(func() {
		postgres = &data_nais_io_v1.Postgres{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-app-db",
				Namespace: "my-team",
			},
			Spec: data_nais_io_v1.PostgresSpec{
				Cluster: data_nais_io_v1.PostgresCluster{
					MajorVersion: "16",
					Resources: data_nais_io_v1.PostgresResources{
						DiskSize: resource.MustParse("10Gi"),
						Memory:   resource.MustParse("1Gi"),
						Cpu:      resource.MustParse("500m"),
					},
				},
			},
		}
	})

	Describe("CreatePodMonitor", func() {
		It("should create a PodMonitor with correct metadata", func() {
			pm := CreateCNPGPodMonitor(postgres, "my-cluster", "pg-my-team")

			Expect(pm.Kind).To(Equal("PodMonitor"))
			Expect(pm.APIVersion).To(Equal("monitoring.coreos.com/v1"))
			Expect(pm.Name).To(Equal("pg-my-cluster"))
			Expect(pm.Namespace).To(Equal("pg-my-team"))
		})

		It("should have prometheus=tenant label for tenant Prometheus ingestion", func() {
			pm := CreateCNPGPodMonitor(postgres, "my-cluster", "pg-my-team")

			Expect(pm.Labels).To(HaveKeyWithValue("prometheus", "tenant"))
		})

		It("should select pods by cnpg.io/cluster label", func() {
			pm := CreateCNPGPodMonitor(postgres, "my-cluster", "pg-my-team")

			Expect(pm.Spec.Selector.MatchLabels).To(HaveKeyWithValue("cnpg.io/cluster", "my-cluster"))
		})

		It("should scrape the metrics port", func() {
			pm := CreateCNPGPodMonitor(postgres, "my-cluster", "pg-my-team")

			Expect(pm.Spec.PodMetricsEndpoints).To(HaveLen(1))
			Expect(*pm.Spec.PodMetricsEndpoints[0].Port).To(Equal("metrics"))
		})
	})

	Describe("MinimalPodMonitor", func() {
		It("should create a minimal PodMonitor for deletion", func() {
			pm := MinimalCNPGPodMonitor(postgres, "my-cluster", "pg-my-team")

			Expect(pm.Name).To(Equal("pg-my-cluster"))
			Expect(pm.Namespace).To(Equal("pg-my-team"))
			Expect(pm.Spec.PodMetricsEndpoints).To(BeEmpty())
		})
	})
})
