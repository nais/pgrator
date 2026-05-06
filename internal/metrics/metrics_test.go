package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func collectGaugeVec(g *prometheus.GaugeVec) []*dto.Metric {
	ch := make(chan prometheus.Metric, 100)
	g.Collect(ch)
	close(ch)

	var metrics []*dto.Metric
	for m := range ch {
		d := &dto.Metric{}
		_ = m.Write(d)
		metrics = append(metrics, d)
	}
	return metrics
}

func collectHistogramVec(h *prometheus.HistogramVec) []*dto.Metric {
	ch := make(chan prometheus.Metric, 100)
	h.Collect(ch)
	close(ch)

	var metrics []*dto.Metric
	for m := range ch {
		d := &dto.Metric{}
		_ = m.Write(d)
		metrics = append(metrics, d)
	}
	return metrics
}

func collectCounterVec(c *prometheus.CounterVec) []*dto.Metric {
	ch := make(chan prometheus.Metric, 100)
	c.Collect(ch)
	close(ch)

	var metrics []*dto.Metric
	for m := range ch {
		d := &dto.Metric{}
		_ = m.Write(d)
		metrics = append(metrics, d)
	}
	return metrics
}

func getLabelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

func TestSetResourceInfo(t *testing.T) {
	// Reset the gauge for test isolation
	ResourceInfo.Reset()

	labels := ResourceLabels{
		ResourceType:     "postgres.data.nais.io",
		Namespace:        "my-team",
		Name:             "my-db",
		MajorVersion:     "16",
		HighAvailability: "true",
		Phase:            "Completed",
	}

	SetResourceInfo(labels)

	metrics := collectGaugeVec(ResourceInfo)
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}

	m := metrics[0]
	if m.GetGauge().GetValue() != 1 {
		t.Errorf("expected gauge value 1, got %f", m.GetGauge().GetValue())
	}
	if got := getLabelValue(m, "resource_type"); got != "postgres.data.nais.io" {
		t.Errorf("expected resource_type=postgres.data.nais.io, got %s", got)
	}
	if got := getLabelValue(m, "namespace"); got != "my-team" {
		t.Errorf("expected namespace=my-team, got %s", got)
	}
	if got := getLabelValue(m, "name"); got != "my-db" {
		t.Errorf("expected name=my-db, got %s", got)
	}
	if got := getLabelValue(m, "major_version"); got != "16" {
		t.Errorf("expected major_version=16, got %s", got)
	}
	if got := getLabelValue(m, "high_availability"); got != "true" {
		t.Errorf("expected high_availability=true, got %s", got)
	}
	if got := getLabelValue(m, "phase"); got != "Completed" {
		t.Errorf("expected phase=Completed, got %s", got)
	}
}

func TestSetResourceInfo_UpdatesExisting(t *testing.T) {
	ResourceInfo.Reset()

	labels := ResourceLabels{
		ResourceType:     "postgres.data.nais.io",
		Namespace:        "my-team",
		Name:             "my-db",
		MajorVersion:     "16",
		HighAvailability: "false",
		Phase:            "Preparing",
	}
	SetResourceInfo(labels)

	// Update with new phase — this creates a new time series (different label set)
	// The old one with phase=Preparing will still exist
	labels.Phase = "Completed"
	SetResourceInfo(labels)

	metrics := collectGaugeVec(ResourceInfo)
	// Both label sets exist (Preparing and Completed)
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics (old + new phase), got %d", len(metrics))
	}
}

func TestRemoveResourceInfo(t *testing.T) {
	ResourceInfo.Reset()

	// Set two resources
	SetResourceInfo(ResourceLabels{
		ResourceType:     "postgres.data.nais.io",
		Namespace:        "team-a",
		Name:             "db-1",
		MajorVersion:     "16",
		HighAvailability: "true",
		Phase:            "Completed",
	})
	SetResourceInfo(ResourceLabels{
		ResourceType:     "postgres.data.nais.io",
		Namespace:        "team-b",
		Name:             "db-2",
		MajorVersion:     "17",
		HighAvailability: "false",
		Phase:            "Completed",
	})

	metrics := collectGaugeVec(ResourceInfo)
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}

	// Remove one resource
	RemoveResourceInfo(ResourceLabels{
		ResourceType: "postgres.data.nais.io",
		Namespace:    "team-a",
		Name:         "db-1",
	})

	metrics = collectGaugeVec(ResourceInfo)
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric after removal, got %d", len(metrics))
	}

	// Verify remaining metric is team-b
	if got := getLabelValue(metrics[0], "namespace"); got != "team-b" {
		t.Errorf("expected remaining metric namespace=team-b, got %s", got)
	}
}

func TestRemoveResourceInfo_RemovesAllPhases(t *testing.T) {
	ResourceInfo.Reset()

	// A resource that was updated with different phases will have multiple series
	SetResourceInfo(ResourceLabels{
		ResourceType:     "postgres.data.nais.io",
		Namespace:        "team-a",
		Name:             "db-1",
		MajorVersion:     "16",
		HighAvailability: "true",
		Phase:            "Preparing",
	})
	SetResourceInfo(ResourceLabels{
		ResourceType:     "postgres.data.nais.io",
		Namespace:        "team-a",
		Name:             "db-1",
		MajorVersion:     "16",
		HighAvailability: "true",
		Phase:            "Completed",
	})

	metrics := collectGaugeVec(ResourceInfo)
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics (two phases), got %d", len(metrics))
	}

	// DeletePartialMatch should remove both
	RemoveResourceInfo(ResourceLabels{
		ResourceType: "postgres.data.nais.io",
		Namespace:    "team-a",
		Name:         "db-1",
	})

	metrics = collectGaugeVec(ResourceInfo)
	if len(metrics) != 0 {
		t.Fatalf("expected 0 metrics after removal, got %d", len(metrics))
	}
}

func TestObserveReconcileDuration(t *testing.T) {
	ReconcileDuration.Reset()

	ObserveReconcileDuration("postgres.data.nais.io", "success", 500*time.Millisecond)
	ObserveReconcileDuration("postgres.data.nais.io", "error", 100*time.Millisecond)

	metrics := collectHistogramVec(ReconcileDuration)
	if len(metrics) != 2 {
		t.Fatalf("expected 2 histogram metrics, got %d", len(metrics))
	}

	for _, m := range metrics {
		result := getLabelValue(m, "result")
		count := m.GetHistogram().GetSampleCount()
		if count != 1 {
			t.Errorf("expected 1 sample for result=%s, got %d", result, count)
		}
		switch result {
		case "success":
			sum := m.GetHistogram().GetSampleSum()
			if sum < 0.4 || sum > 0.6 {
				t.Errorf("expected sum ~0.5 for success, got %f", sum)
			}
		case "error":
			sum := m.GetHistogram().GetSampleSum()
			if sum < 0.09 || sum > 0.11 {
				t.Errorf("expected sum ~0.1 for error, got %f", sum)
			}
		}
	}
}

func TestIncReconcileError(t *testing.T) {
	ReconcileErrors.Reset()

	IncReconcileError("postgres.data.nais.io", "my-team", "Preparing")
	IncReconcileError("postgres.data.nais.io", "my-team", "Preparing")
	IncReconcileError("postgres.data.nais.io", "my-team", "PerformActions")

	metrics := collectCounterVec(ReconcileErrors)
	if len(metrics) != 2 {
		t.Fatalf("expected 2 counter metrics, got %d", len(metrics))
	}

	for _, m := range metrics {
		phase := getLabelValue(m, "phase")
		value := m.GetCounter().GetValue()
		switch phase {
		case "Preparing":
			if value != 2 {
				t.Errorf("expected Preparing counter=2, got %f", value)
			}
		case "PerformActions":
			if value != 1 {
				t.Errorf("expected PerformActions counter=1, got %f", value)
			}
		default:
			t.Errorf("unexpected phase: %s", phase)
		}
	}
}

func TestSetResourceInfo_EmptyOptionalLabels(t *testing.T) {
	ResourceInfo.Reset()

	// Valkey/OpenSearch won't have major_version or high_availability
	labels := ResourceLabels{
		ResourceType: "valkey.nais.io",
		Namespace:    "my-team",
		Name:         "my-cache",
		Phase:        "Completed",
	}
	SetResourceInfo(labels)

	metrics := collectGaugeVec(ResourceInfo)
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}

	m := metrics[0]
	if got := getLabelValue(m, "major_version"); got != "" {
		t.Errorf("expected empty major_version, got %s", got)
	}
	if got := getLabelValue(m, "high_availability"); got != "" {
		t.Errorf("expected empty high_availability, got %s", got)
	}
}

func TestRemoveBeforeSet_PreventsStalePhase(t *testing.T) {
	ResourceInfo.Reset()

	// Simulate the synchronizer pattern: remove then set
	labels := ResourceLabels{
		ResourceType:     "postgres.data.nais.io",
		Namespace:        "my-team",
		Name:             "my-db",
		MajorVersion:     "16",
		HighAvailability: "true",
		Phase:            "Preparing",
	}
	SetResourceInfo(labels)

	// Now simulate updateResourceInfoMetric which removes first
	RemoveResourceInfo(ResourceLabels{
		ResourceType: "postgres.data.nais.io",
		Namespace:    "my-team",
		Name:         "my-db",
	})
	labels.Phase = "Completed"
	SetResourceInfo(labels)

	metrics := collectGaugeVec(ResourceInfo)
	if len(metrics) != 1 {
		t.Fatalf("expected exactly 1 metric (no stale phase), got %d", len(metrics))
	}
	if got := getLabelValue(metrics[0], "phase"); got != "Completed" {
		t.Errorf("expected phase=Completed, got %s", got)
	}
}

func TestRemoveBeforeSet_PreventsStaleVersion(t *testing.T) {
	ResourceInfo.Reset()

	// Simulate a version upgrade: 15 → 16
	SetResourceInfo(ResourceLabels{
		ResourceType:     "postgres.data.nais.io",
		Namespace:        "my-team",
		Name:             "my-db",
		MajorVersion:     "15",
		HighAvailability: "true",
		Phase:            "Completed",
	})

	// Upgrade: remove old, set new
	RemoveResourceInfo(ResourceLabels{
		ResourceType: "postgres.data.nais.io",
		Namespace:    "my-team",
		Name:         "my-db",
	})
	SetResourceInfo(ResourceLabels{
		ResourceType:     "postgres.data.nais.io",
		Namespace:        "my-team",
		Name:             "my-db",
		MajorVersion:     "16",
		HighAvailability: "true",
		Phase:            "Completed",
	})

	metrics := collectGaugeVec(ResourceInfo)
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric (no stale version), got %d", len(metrics))
	}
	if got := getLabelValue(metrics[0], "major_version"); got != "16" {
		t.Errorf("expected major_version=16, got %s", got)
	}
}
