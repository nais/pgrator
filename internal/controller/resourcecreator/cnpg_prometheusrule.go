package resourcecreator

import (
	"fmt"

	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	monitoring_v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func CreateCNPGPrometheusRuleSpec(postgres *data_nais_io_v1.Postgres, pgClusterName, pgNamespace string) *monitoring_v1.PrometheusRule {
	prometheusRule := MinimalPrometheusRule(postgres, pgClusterName)
	alertNamespace := prometheusRule.GetNamespace()

	clusterFilter := fmt.Sprintf("namespace=\"%s\", cnpg_cluster=\"%s\"", pgNamespace, pgClusterName)

	// CNPG-native alerts using the built-in metrics exporter
	cnpgRules := []monitoring_v1.Rule{
		{
			Alert: "PostgresReplicationLagHigh",
			Expr:  intstr.FromString(fmt.Sprintf("max(cnpg_pg_replication_lag{%s}) > 30", clusterFilter)),
			For:   ptr.To(monitoring_v1.Duration("5m")),
			Labels: map[string]string{
				"severity":  "warning",
				"namespace": alertNamespace,
			},
			Annotations: map[string]string{
				"summary":     "PostgreSQL replication lag is high",
				"description": fmt.Sprintf("Replication lag for PostgreSQL cluster %s exceeds 30 seconds.", pgClusterName),
				"action":      "Investigate replica performance and network connectivity.",
			},
		},
		{
			Alert: "PostgresWALArchivingFailed",
			Expr:  intstr.FromString(fmt.Sprintf("increase(cnpg_pg_stat_archiver_failed_count{%s}[10m]) > 0", clusterFilter)),
			For:   ptr.To(monitoring_v1.Duration("1m")),
			Labels: map[string]string{
				"severity":  "warning",
				"namespace": alertNamespace,
			},
			Annotations: map[string]string{
				"summary":     "PostgreSQL WAL archiving is failing",
				"description": fmt.Sprintf("WAL archiving for PostgreSQL cluster %s has failed in the last 10 minutes.", pgClusterName),
				"action":      "Check backup bucket permissions and network connectivity.",
			},
		},
		{
			Alert: "PostgresBackupStale",
			Expr:  intstr.FromString(fmt.Sprintf("max(cnpg_collector_last_available_backup_timestamp{%s}) < (time() - 86400 * 2)", clusterFilter)),
			For:   ptr.To(monitoring_v1.Duration("30m")),
			Labels: map[string]string{
				"severity":  "warning",
				"namespace": alertNamespace,
			},
			Annotations: map[string]string{
				"summary":     "PostgreSQL backup is stale",
				"description": fmt.Sprintf("No successful backup for PostgreSQL cluster %s in the last 48 hours.", pgClusterName),
				"action":      "Verify scheduled backups and barman-cloud plugin configuration.",
			},
		},
		{
			Alert: "PostgresConnectionSaturation",
			Expr:  intstr.FromString(fmt.Sprintf("max(cnpg_backends_total{%s}) / max(cnpg_pg_settings_setting{%s, name=\"max_connections\"}) > 0.8", clusterFilter, clusterFilter)),
			For:   ptr.To(monitoring_v1.Duration("5m")),
			Labels: map[string]string{
				"severity":  "warning",
				"namespace": alertNamespace,
			},
			Annotations: map[string]string{
				"summary":     "PostgreSQL connections near limit",
				"description": fmt.Sprintf("PostgreSQL cluster %s is using more than 80%% of available connections.", pgClusterName),
				"action":      "Review connection pooling configuration or increase max_connections.",
			},
		},
	}

	// Combine CNPG-native alerts with shared container/volume alerts
	rules := append(cnpgRules, containerVolumeAlertRules(pgClusterName, pgNamespace, alertNamespace)...)
	rules = append(rules,
		monitoring_v1.Rule{
			Alert: "ClusterIsDown",
			Expr:  intstr.FromString(fmt.Sprintf("count(cnpg_collector_up{%s}) == 0 or absent(cnpg_collector_up{%s})", clusterFilter, clusterFilter)),
			For:   ptr.To(monitoring_v1.Duration("5m")),
			Labels: map[string]string{
				"severity":  "critical",
				"namespace": alertNamespace,
			},
			Annotations: map[string]string{
				"summary":     "PostgreSQL cluster is down",
				"description": fmt.Sprintf("The PostgreSQL instance %s has no running instances.", pgClusterName),
				"action":      "Check pod status and events in the namespace.",
			},
		},
		monitoring_v1.Rule{
			Alert: "MissingClusterInstance",
			Expr:  intstr.FromString(fmt.Sprintf("count(cnpg_collector_up{%s}) < 2", clusterFilter)),
			For:   ptr.To(monitoring_v1.Duration("10m")),
			Labels: map[string]string{
				"severity":  "warning",
				"namespace": alertNamespace,
			},
			Annotations: map[string]string{
				"summary":     "PostgreSQL cluster is missing pods",
				"description": fmt.Sprintf("The PostgreSQL instance %s has fewer than 2 running pods.", pgClusterName),
				"action":      "Check pod status and pending PVCs.",
			},
		},
	)

	prometheusRule.Spec = monitoring_v1.PrometheusRuleSpec{
		Groups: []monitoring_v1.RuleGroup{
			{
				Name:  fmt.Sprintf("%s-rules", pgClusterName),
				Rules: rules,
			},
		},
	}
	return prometheusRule
}
