package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	ReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "pgrator_reconcile_duration_seconds",
			Help:    "Duration of reconciliations by resource type",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"resource"},
	)

	ReconcileErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgrator_reconcile_errors_total",
			Help: "Number of reconcile errors by resource type",
		},
		[]string{"resource"},
	)

	ReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgrator_reconciliations_total",
			Help: "Total number of reconciliations by resource type",
		},
		[]string{"resource"},
	)

	ReconcileSuccess = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "pgrator_reconcile_success_total",
			Help: "Number of successful reconciles by resource type",
		},
		[]string{"resource"},
	)
)

func init() {
	metrics.Registry.MustRegister(ReconcileDuration, ReconcileErrors, ReconcileTotal, ReconcileSuccess)
}
