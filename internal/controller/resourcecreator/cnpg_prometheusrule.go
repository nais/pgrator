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

	prometheusRule.Spec = monitoring_v1.PrometheusRuleSpec{
		Groups: []monitoring_v1.RuleGroup{
			{
				Name: fmt.Sprintf("%s-rules", pgClusterName),
				Rules: []monitoring_v1.Rule{
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
					{
						Alert: "PostgresMemoryUsageHigh",
						Expr: intstr.FromString(makeQuery(
							makeSingleQuery("container_memory_working_set_bytes", "pod", []string{
								"container=\"postgres\"",
								fmt.Sprintf("namespace=\"%s\"", pgNamespace),
								fmt.Sprintf("pod=~\"%s-[0-9]\"", pgClusterName),
							}, false),
							makeSingleQuery("kube_pod_container_resource_limits", "pod", []string{
								"container=\"postgres\"",
								fmt.Sprintf("namespace=\"%s\"", pgNamespace),
								fmt.Sprintf("pod=~\"%s-[0-9]\"", pgClusterName),
								"resource=\"memory\"",
							}, false),
							"> 0.9")),
						For: ptr.To(monitoring_v1.Duration("5m")),
						Labels: map[string]string{
							"severity":  "warning",
							"namespace": alertNamespace,
						},
						Annotations: map[string]string{
							"summary":     "PostgreSQL memory usage is high",
							"description": fmt.Sprintf("Memory usage for PostgreSQL instance %s is above 90%%.", pgClusterName),
							"action":      "Increase requested resources.",
						},
					},
					{
						Alert: "PostgresCpuUsageHigh",
						Expr: intstr.FromString(makeQuery(
							makeSingleQuery("container_cpu_usage_seconds_total", "pod", []string{
								"container=\"postgres\"",
								fmt.Sprintf("namespace=\"%s\"", pgNamespace),
								fmt.Sprintf("pod=~\"%s-[0-9]\"", pgClusterName),
							}, true),
							makeSingleQuery("kube_pod_container_resource_limits", "pod", []string{
								"container=\"postgres\"",
								fmt.Sprintf("namespace=\"%s\"", pgNamespace),
								fmt.Sprintf("pod=~\"%s-[0-9]\"", pgClusterName),
								"resource=\"cpu\"",
							}, false),
							"> 0.9")),
						For: ptr.To(monitoring_v1.Duration("5m")),
						Labels: map[string]string{
							"severity":  "warning",
							"namespace": alertNamespace,
						},
						Annotations: map[string]string{
							"summary":     "PostgreSQL CPU usage is high",
							"description": fmt.Sprintf("CPU usage for PostgreSQL instance %s is above 90%%.", pgClusterName),
							"action":      "Increase requested resources.",
						},
					},
					{
						Alert: "PostgresDiskIsFull",
						Expr: intstr.FromString(makeQuery(
							makeSingleQuery("kubelet_volume_stats_used_bytes", "persistentvolumeclaim", []string{
								fmt.Sprintf("namespace=\"%s\"", pgNamespace),
								fmt.Sprintf("persistentvolumeclaim=~\"pgdata-%s-[0-9]\"", pgClusterName),
							}, false),
							makeSingleQuery("kubelet_volume_stats_capacity_bytes", "persistentvolumeclaim", []string{
								fmt.Sprintf("namespace=\"%s\"", pgNamespace),
								fmt.Sprintf("persistentvolumeclaim=~\"pgdata-%s-[0-9]\"", pgClusterName),
							}, false),
							"> 0.99")),
						For: ptr.To(monitoring_v1.Duration("5m")),
						Labels: map[string]string{
							"severity":  "critical",
							"namespace": alertNamespace,
						},
						Annotations: map[string]string{
							"summary":     "PostgreSQL Disk is full",
							"description": fmt.Sprintf("Disk for PostgreSQL instance %s is full.", pgClusterName),
							"action":      "Increase requested resources.",
						},
					},
					{
						Alert: "PostgresDiskUsageHigh",
						Expr: intstr.FromString(makeQuery(
							makeSingleQuery("kubelet_volume_stats_used_bytes", "persistentvolumeclaim", []string{
								fmt.Sprintf("namespace=\"%s\"", pgNamespace),
								fmt.Sprintf("persistentvolumeclaim=~\"pgdata-%s-[0-9]\"", pgClusterName),
							}, false),
							makeSingleQuery("kubelet_volume_stats_capacity_bytes", "persistentvolumeclaim", []string{
								fmt.Sprintf("namespace=\"%s\"", pgNamespace),
								fmt.Sprintf("persistentvolumeclaim=~\"pgdata-%s-[0-9]\"", pgClusterName),
							}, false),
							"> 0.9")),
						For: ptr.To(monitoring_v1.Duration("5m")),
						Labels: map[string]string{
							"severity":  "warning",
							"namespace": alertNamespace,
						},
						Annotations: map[string]string{
							"summary":     "PostgreSQL Disk usage is high",
							"description": fmt.Sprintf("Disk usage for PostgreSQL instance %s is above 90%%.", pgClusterName),
							"action":      "Increase requested resources.",
						},
					},
					{
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
					{
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
				},
			},
		},
	}
	return prometheusRule
}
