package monitoring

import (
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	monitoring_v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("CNPG PrometheusRule", func() {
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

	Describe("CreateCNPGPrometheusRule", func() {
		It("should create a PrometheusRule with CNPG-specific alerts", func() {
			rule := CreateCNPGPrometheusRule(postgres, "my-cluster", "pg-my-team")

			Expect(rule.Kind).To(Equal("PrometheusRule"))
			Expect(rule.Name).To(Equal("pg-my-cluster"))
			Expect(rule.Spec.Groups).To(HaveLen(1))
			Expect(rule.Spec.Groups[0].Name).To(Equal("my-cluster-rules"))
		})

		It("should include replication lag alert", func() {
			rule := CreateCNPGPrometheusRule(postgres, "my-cluster", "pg-my-team")
			rules := rule.Spec.Groups[0].Rules

			replicationAlert := findAlert(rules, "PostgresReplicationLagHigh")
			Expect(replicationAlert).NotTo(BeNil())
			Expect(replicationAlert.Expr.String()).To(ContainSubstring("cnpg_pg_replication_lag"))
			Expect(replicationAlert.Expr.String()).To(ContainSubstring(`cnpg_cluster="my-cluster"`))
		})

		It("should include WAL archiving alert", func() {
			rule := CreateCNPGPrometheusRule(postgres, "my-cluster", "pg-my-team")
			rules := rule.Spec.Groups[0].Rules

			walAlert := findAlert(rules, "PostgresWALArchivingFailed")
			Expect(walAlert).NotTo(BeNil())
			Expect(walAlert.Expr.String()).To(ContainSubstring("cnpg_pg_stat_archiver_failed_count"))
		})

		It("should include backup staleness alert", func() {
			rule := CreateCNPGPrometheusRule(postgres, "my-cluster", "pg-my-team")
			rules := rule.Spec.Groups[0].Rules

			backupAlert := findAlert(rules, "PostgresBackupStale")
			Expect(backupAlert).NotTo(BeNil())
			Expect(backupAlert.Expr.String()).To(ContainSubstring("cnpg_collector_last_available_backup_timestamp"))
		})

		It("should include connection saturation alert", func() {
			rule := CreateCNPGPrometheusRule(postgres, "my-cluster", "pg-my-team")
			rules := rule.Spec.Groups[0].Rules

			connAlert := findAlert(rules, "PostgresConnectionSaturation")
			Expect(connAlert).NotTo(BeNil())
			Expect(connAlert.Expr.String()).To(ContainSubstring("cnpg_backends_total"))
		})

		It("should include all 10 alerts", func() {
			rule := CreateCNPGPrometheusRule(postgres, "my-cluster", "pg-my-team")
			Expect(rule.Spec.Groups[0].Rules).To(HaveLen(10))
		})

		It("should use correct namespace filter for CNPG metrics", func() {
			rule := CreateCNPGPrometheusRule(postgres, "my-cluster", "pg-my-team")
			rules := rule.Spec.Groups[0].Rules

			replicationAlert := findAlert(rules, "PostgresReplicationLagHigh")
			Expect(replicationAlert.Expr.String()).To(ContainSubstring(`namespace="pg-my-team"`))
		})
	})
})

func findAlert(rules []monitoring_v1.Rule, name string) *monitoring_v1.Rule {
	for i := range rules {
		if rules[i].Alert == name {
			return &rules[i]
		}
	}
	return nil
}
