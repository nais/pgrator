// Package postgresmonitoring builds namespace-wide Postgres monitoring resources.
package postgresmonitoring

import (
	_ "embed"
	"fmt"
	"slices"
	"strings"

	"github.com/nais/pgrator/internal/resourcecreator/cnpg"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/yaml"
)

const (
	RuleName     = "nais-postgres-alerts"
	ManagedLabel = "postgres.nais.io/namespace-monitoring"
	ManagedValue = "true"

	// InitializedCondition records the first time the Postgres's CNPG cluster
	// was observed Initialized and Ready. Its message pins the cluster UID.
	InitializedCondition = "DatabaseInitialized"

	sourceURL = "https://github.com/nais/pgrator/blob/main/internal/resourcecreator/postgresmonitoring/alerts.yaml"
)

// Options describe the pgrator deployment, not database health.
type Options struct {
	BackupsEnabled bool
}

//go:embed alerts.yaml
var alerts string

// Rule renders the platform alerts for the namespace, plus an inventory of its
// live databases. The inventory lets alerts fire without pods or scrape targets.
func Rule(namespace string, options Options, databases ...v1.Postgres) (*monitoringv1.PrometheusRule, error) {
	rule := &monitoringv1.PrometheusRule{}
	if err := yaml.UnmarshalStrict([]byte(strings.ReplaceAll(alerts, "__NAMESPACE__", namespace)), rule); err != nil {
		return nil, err
	}
	rule.Namespace = namespace
	rule.Labels = map[string]string{ManagedLabel: ManagedValue}
	// Tell whoever receives an alert where it comes from and where to change it.
	for g := range rule.Spec.Groups {
		for r := range rule.Spec.Groups[g].Rules {
			alert := &rule.Spec.Groups[g].Rules[r]
			if alert.Alert == "" {
				continue // recording rule
			}
			if alert.Annotations == nil {
				alert.Annotations = map[string]string{}
			}
			alert.Annotations["source"] = sourceURL
			alert.Annotations["managed_by"] = "pgrator"
			alert.Annotations["configuration"] = "Managed by pgrator. Change the alert in the source repository; edits to this PrometheusRule are overwritten."
		}
	}
	if inventory := inventory(namespace, options, databases); len(inventory.Rules) > 0 {
		rule.Spec.Groups = append([]monitoringv1.RuleGroup{inventory}, rule.Spec.Groups...)
	}
	return rule, nil
}

func inventory(namespace string, options Options, databases []v1.Postgres) monitoringv1.RuleGroup {
	group := monitoringv1.RuleGroup{Name: "nais-postgres-inventory"}
	databases = slices.Clone(databases)
	slices.SortFunc(databases, func(a, b v1.Postgres) int { return strings.Compare(a.Name, b.Name) })
	for _, pg := range databases {
		// Intentional deletion removes the database from alerting immediately.
		if pg.Namespace != namespace || !pg.DeletionTimestamp.IsZero() {
			continue
		}
		record := func(name string, value int64) {
			group.Rules = append(group.Rules, monitoringv1.Rule{
				Record: name,
				Expr:   intstr.FromString(fmt.Sprintf("vector(%d)", value)),
				Labels: map[string]string{"namespace": namespace, "postgres": pg.Name},
			})
		}
		created := int64(0)
		if !pg.CreationTimestamp.IsZero() {
			created = pg.CreationTimestamp.Unix()
		}
		record("nais_postgres_desired_instances", int64(cnpg.DesiredInstances(pg.Spec.HighAvailability)))
		record("nais_postgres_created_timestamp_seconds", created)
		record("nais_postgres_initialized_timestamp_seconds", initializedAt(pg))
		record("nais_postgres_recovery_requested", boolNumber(pg.Spec.Recovery != nil))
		record("nais_postgres_backup_enabled", boolNumber(options.BackupsEnabled && !pg.Spec.BackupDisabled()))
	}
	return group
}

// initializedAt is 0 unless the milestone belongs to the current CNPG cluster.
func initializedAt(pg v1.Postgres) int64 {
	if pg.Status == nil || pg.Status.Ownership == nil || pg.Status.Ownership.ClusterUID == "" {
		return 0
	}
	condition := meta.FindStatusCondition(pg.Status.Conditions, InitializedCondition)
	if condition == nil || condition.Status != metav1.ConditionTrue || condition.Message != pg.Status.Ownership.ClusterUID {
		return 0
	}
	return condition.LastTransitionTime.Unix()
}

func boolNumber(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
