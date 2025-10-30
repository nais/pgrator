package resourcecreator

import (
	"fmt"

	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	monitoring_v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func MinimalPrometheusRule(postgres *data_nais_io_v1.Postgres, pgClusterName string, pgNamespace string) *monitoring_v1.PrometheusRule {
	objectMeta := CreateObjectMeta(postgres)
	objectMeta.Name = pgClusterName

	return &monitoring_v1.PrometheusRule{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PrometheusRule",
			APIVersion: "monitoring.coreos.com/v1",
		},
		ObjectMeta: objectMeta,
	}
}

func CreatePrometheusRuleSpec(postgres *data_nais_io_v1.Postgres, pgClusterName string, pgNamespace string) *monitoring_v1.PrometheusRule {
	prometheusRule := MinimalPrometheusRule(postgres, pgClusterName, pgNamespace)

	prometheusRule.Spec = monitoring_v1.PrometheusRuleSpec{
		Groups: []monitoring_v1.RuleGroup{
			{
				Name: fmt.Sprintf("%s-rules", pgClusterName),
				Rules: []monitoring_v1.Rule{
					{
						Alert: "PostgresMemoryUsageHigh",
						Expr: intstrFromString(fmt.Sprintf(
							"(avg(container_memory_usage_bytes{container=\"postgres\", namespace=\"%s\", pod=~\"%s-[0-9]\"}) by (pod) / avg(kube_pod_container_resource_limits{container=\"postgres\", resource=\"memory\", namespace=\"%s\", pod=~\"%s-[0-9]\"}) by (pod)) > 0.9", pgNamespace, pgClusterName, pgNamespace, pgClusterName)),
						For: ptr.To(monitoring_v1.Duration("10s")),
						Labels: map[string]string{
							"severity": "warning",
						},
						Annotations: map[string]string{
							"summary":     "PostgreSQL memory usage is high",
							"description": fmt.Sprintf("Memory usage for PostgreSQL instance %s is above 90%%.", pgClusterName),
							"action":      "?",
						},
					},
				},
			},
		},
	}
	return prometheusRule
}

func intstrFromString(sprintf string) intstr.IntOrString {
	return intstr.IntOrString{
		Type:   intstr.String,
		StrVal: sprintf,
	}
}
