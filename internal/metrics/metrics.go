package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// ResourceInfo is a gauge that is always set to 1 for each managed resource.
	// Use count(pgrator_resource_info) to get total managed resources.
	ResourceInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "pgrator",
			Name:      "resource_info",
			Help:      "Information about managed resources. Always 1 per resource.",
		},
		[]string{"resource_type", "namespace", "name", "major_version", "high_availability", "phase"},
	)

	// ReconcileDuration tracks the duration of reconciliation loops.
	ReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "pgrator",
			Name:      "reconcile_duration_seconds",
			Help:      "Duration of reconciliation in seconds.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"resource_type", "result"},
	)

	// ReconcileErrors counts reconciliation errors by phase.
	ReconcileErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "pgrator",
			Name:      "reconcile_errors_total",
			Help:      "Total number of reconciliation errors.",
		},
		[]string{"resource_type", "namespace", "phase"},
	)
)

func init() {
	metrics.Registry.MustRegister(ResourceInfo, ReconcileDuration, ReconcileErrors)
}

// ResourceLabels holds the labels for a managed resource.
type ResourceLabels struct {
	ResourceType     string
	Namespace        string
	Name             string
	MajorVersion     string
	HighAvailability string
	Phase            string
}

// SetResourceInfo sets the info gauge for a managed resource.
func SetResourceInfo(labels ResourceLabels) {
	ResourceInfo.With(prometheus.Labels{
		"resource_type":     labels.ResourceType,
		"namespace":         labels.Namespace,
		"name":              labels.Name,
		"major_version":     labels.MajorVersion,
		"high_availability": labels.HighAvailability,
		"phase":             labels.Phase,
	}).Set(1)
}

// RemoveResourceInfo removes the info gauge for a deleted resource.
func RemoveResourceInfo(labels ResourceLabels) {
	ResourceInfo.DeletePartialMatch(prometheus.Labels{
		"resource_type": labels.ResourceType,
		"namespace":     labels.Namespace,
		"name":          labels.Name,
	})
}

// ObserveReconcileDuration records the duration of a reconciliation.
func ObserveReconcileDuration(resourceType string, result string, duration time.Duration) {
	ReconcileDuration.With(prometheus.Labels{
		"resource_type": resourceType,
		"result":        result,
	}).Observe(duration.Seconds())
}

// IncReconcileError increments the error counter for a reconciliation phase.
func IncReconcileError(resourceType, namespace, phase string) {
	ReconcileErrors.With(prometheus.Labels{
		"resource_type": resourceType,
		"namespace":     namespace,
		"phase":         phase,
	}).Inc()
}
