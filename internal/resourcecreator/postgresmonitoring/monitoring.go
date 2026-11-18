// Package postgresmonitoring builds namespace-wide Postgres monitoring resources.
package postgresmonitoring

import (
	_ "embed"
	"strings"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"sigs.k8s.io/yaml"
)

const (
	RuleName     = "nais-postgres-alerts"
	ManagedLabel = "postgres.nais.io/namespace-monitoring"
)

//go:embed alerts.yaml
var alerts string

// Rule renders the platform alerts for all databases in one namespace.
func Rule(namespace string) (*monitoringv1.PrometheusRule, error) {
	rule := &monitoringv1.PrometheusRule{}
	if err := yaml.UnmarshalStrict([]byte(strings.ReplaceAll(alerts, "__NAMESPACE__", namespace)), rule); err != nil {
		return nil, err
	}
	rule.Namespace = namespace
	rule.Labels = map[string]string{ManagedLabel: "true"}
	return rule, nil
}
