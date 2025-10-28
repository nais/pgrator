package resourcecreator

import (
	"fmt"

	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	monv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateMinimalPrometheusRule(postgres *data_nais_io_v1.Postgres, pgClusterName, pgNamespace string) *monv1.PrometheusRule {
	objectMeta := CreateObjectMeta(postgres)
	objectMeta.Name = fmt.Sprintf("%s-alerts", pgClusterName)
	objectMeta.Namespace = pgNamespace

	return &monv1.PrometheusRule{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PrometheusRule",
			APIVersion: "monitoring.coreos.com/v1",
		},
		ObjectMeta: objectMeta,
	}
}

func PrometheusRuleForPostgres(postgres *data_nais_io_v1.Postgres, pgClusterName, pgNamespace string) *monv1.PrometheusRule {
	rule := CreateMinimalPrometheusRule(postgres, pgClusterName, pgNamespace)
	rule.Spec = monv1.PrometheusRuleSpec{
		Groups: []monv1.RuleGroup{
			{
				Name: "postgres-resource-usage",
				Rules: []monv1.Rule{
					makeHighCPUUsageRule(pgClusterName, pgNamespace),
					makeHighMemoryUsageRule(pgClusterName, pgNamespace),
					makeDiskUsageHighRule(pgClusterName, pgNamespace),
				},
			},
		},
	}
	return rule
}

func makeHighCPUUsageRule(pgClusterName, pgNamespace string) monv1.Rule {
	return monv1.Rule{}
}

func makeHighMemoryUsageRule(pgClusterName, pgNamespace string) monv1.Rule {
	return monv1.Rule{}
}

func makeDiskUsageHighRule(pgClusterName, pgNamespace string) monv1.Rule {
	return monv1.Rule{}
}
