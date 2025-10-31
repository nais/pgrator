package resourcecreator

import (
	"fmt"
	"strings"

	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	monitoring_v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func MinimalPrometheusRule(postgres *data_nais_io_v1.Postgres, pgClusterName string) *monitoring_v1.PrometheusRule {
	objectMeta := CreateObjectMeta(postgres)
	objectMeta.Name = fmt.Sprintf("pg-%s", pgClusterName)

	return &monitoring_v1.PrometheusRule{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PrometheusRule",
			APIVersion: "monitoring.coreos.com/v1",
		},
		ObjectMeta: objectMeta,
	}
}

func CreatePrometheusRuleSpec(postgres *data_nais_io_v1.Postgres, pgClusterName string, pgNamespace string) *monitoring_v1.PrometheusRule {
	prometheusRule := MinimalPrometheusRule(postgres, pgClusterName)

	prometheusRule.Spec = monitoring_v1.PrometheusRuleSpec{
		Groups: []monitoring_v1.RuleGroup{
			{
				Name: fmt.Sprintf("%s-rules", pgClusterName),
				Rules: []monitoring_v1.Rule{
					{
						Alert: "PostgresMemoryUsageHigh",
						Expr: intstr.FromString(makeQuery(
							makeSingleQuery("container_memory_usage_bytes", "pod", []string{
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
							"severity": "warning",
						},
						Annotations: map[string]string{
							"summary":     "PostgreSQL memory usage is high",
							"description": fmt.Sprintf("Memory usage for PostgreSQL instance %s is above 90%%.", pgClusterName),
							"action":      "Increase requested resources",
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
							"severity": "warning",
						},
						Annotations: map[string]string{
							"summary":     "PostgreSQL CPU usage is high",
							"description": fmt.Sprintf("CPU usage for PostgreSQL instance %s is above 90%%.", pgClusterName),
							"action":      "Increase requested resources",
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
							"severity": "critical",
						},
						Annotations: map[string]string{
							"summary":     "PostgreSQL Disk is full",
							"description": fmt.Sprintf("Disk for PostgreSQL instance %s is full.", pgClusterName),
							"action":      "Increase requested resources",
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
							"severity": "warning",
						},
						Annotations: map[string]string{
							"summary":     "PostgreSQL Disk usage is high",
							"description": fmt.Sprintf("Disk usage for PostgreSQL instance %s is above 90%%.", pgClusterName),
							"action":      "Increase requested resources",
						},
					},
					{
						Alert: "ClusterIsDown",
						Expr:  intstr.FromString(fmt.Sprintf("sum(up{namespace=\"%s\", pod=~\"%s-[0-9]\"}) < 1", pgNamespace, pgClusterName)),
						For:   ptr.To(monitoring_v1.Duration("5m")),
						Labels: map[string]string{
							"severity": "critical",
						},
						Annotations: map[string]string{
							"summary":     "PostgreSQL cluster is down",
							"description": fmt.Sprintf("The PostgreSQL instance %s is down.", pgClusterName),
							"action":      "Investigate causes",
						},
					},
					{
						Alert: "MissingClusterInstance",
						Expr:  intstr.FromString(fmt.Sprintf("sum(up{namespace=\"%s\", pod=~\"%s-[0-9]\"}) < 2", pgNamespace, pgClusterName)),
						For:   ptr.To(monitoring_v1.Duration("10m")),
						Labels: map[string]string{
							"severity": "warning",
						},
						Annotations: map[string]string{
							"summary":     "PostgreSQL cluster is missing pods",
							"description": fmt.Sprintf("The PostgreSQL instance %s has only 1 live pod.", pgClusterName),
							"action":      "Investigate causes",
						},
					},
				},
			},
		},
	}
	return prometheusRule
}

func makeQuery(numeratorQuery, denominatorQuery, limit string) string {
	return fmt.Sprintf("(%s / %s) %s", numeratorQuery, denominatorQuery, limit)
}

func makeSingleQuery(metric string, groupBy string, labels []string, rate bool) string {
	query := fmt.Sprintf("%s{%s}", metric, strings.Join(labels, ", "))

	if rate {
		query = fmt.Sprintf("rate(%s[5m])", query)
	}

	return fmt.Sprintf("avg(%s) by (%s)", query, groupBy)
}
