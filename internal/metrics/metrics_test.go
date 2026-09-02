package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

const (
	testPhaseCompleted   = "Completed"
	testPhasePreparing   = "Preparing"
	testResourceType     = "postgres.nais.io"
	testNamespace        = "my-team"
	testName             = "my-db"
	testMajorVersion     = "16"
	testHighAvailability = "true"
)

func collectMetrics(c prometheus.Collector) []*dto.Metric {
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
	t.Run("sets a gauge with correct labels and value 1", func(t *testing.T) {
		ResourceInfo.Reset()

		labels := ResourceLabels{
			ResourceType:     testResourceType,
			Namespace:        testNamespace,
			Name:             testName,
			MajorVersion:     testMajorVersion,
			HighAvailability: testHighAvailability,
			Phase:            testPhaseCompleted,
		}
		SetResourceInfo(labels)

		metrics := collectMetrics(ResourceInfo)
		if len(metrics) != 1 {
			t.Fatalf("expected 1 metric, got %d", len(metrics))
		}

		m := metrics[0]
		if got := m.GetGauge().GetValue(); got != 1.0 {
			t.Errorf("gauge value = %v, want 1.0", got)
		}
		if got := getLabelValue(m, "resource_type"); got != testResourceType {
			t.Errorf("resource_type = %q, want %q", got, testResourceType)
		}
		if got := getLabelValue(m, "namespace"); got != testNamespace {
			t.Errorf("namespace = %q, want %q", got, testNamespace)
		}
		if got := getLabelValue(m, "name"); got != testName {
			t.Errorf("name = %q, want %q", got, testName)
		}
		if got := getLabelValue(m, "major_version"); got != testMajorVersion {
			t.Errorf("major_version = %q, want %q", got, testMajorVersion)
		}
		if got := getLabelValue(m, "high_availability"); got != testHighAvailability {
			t.Errorf("high_availability = %q, want %q", got, testHighAvailability)
		}
		if got := getLabelValue(m, "phase"); got != testPhaseCompleted {
			t.Errorf("phase = %q, want %q", got, testPhaseCompleted)
		}
	})

	t.Run("creates a new time series when phase changes (old series still exists)", func(t *testing.T) {
		ResourceInfo.Reset()

		labels := ResourceLabels{
			ResourceType:     testResourceType,
			Namespace:        testNamespace,
			Name:             testName,
			MajorVersion:     testMajorVersion,
			HighAvailability: "false",
			Phase:            testPhasePreparing,
		}
		SetResourceInfo(labels)

		labels.Phase = testPhaseCompleted
		SetResourceInfo(labels)

		metrics := collectMetrics(ResourceInfo)
		if len(metrics) != 2 {
			t.Errorf("expected both Preparing and Completed label sets to exist, got %d metrics", len(metrics))
		}
	})

	t.Run("handles empty optional labels (e.g. Valkey/OpenSearch)", func(t *testing.T) {
		ResourceInfo.Reset()

		labels := ResourceLabels{
			ResourceType: "valkey.nais.io",
			Namespace:    testNamespace,
			Name:         "my-cache",
			Phase:        testPhaseCompleted,
		}
		SetResourceInfo(labels)

		metrics := collectMetrics(ResourceInfo)
		if len(metrics) != 1 {
			t.Fatalf("expected 1 metric, got %d", len(metrics))
		}

		m := metrics[0]
		if got := getLabelValue(m, "major_version"); got != "" {
			t.Errorf("major_version = %q, want empty", got)
		}
		if got := getLabelValue(m, "high_availability"); got != "" {
			t.Errorf("high_availability = %q, want empty", got)
		}
	})
}

func TestRemoveResourceInfo(t *testing.T) {
	t.Run("removes only the matching resource, leaving others intact", func(t *testing.T) {
		ResourceInfo.Reset()

		SetResourceInfo(ResourceLabels{
			ResourceType:     testResourceType,
			Namespace:        "team-a",
			Name:             "db-1",
			MajorVersion:     testMajorVersion,
			HighAvailability: "true",
			Phase:            testPhaseCompleted,
		})
		SetResourceInfo(ResourceLabels{
			ResourceType:     testResourceType,
			Namespace:        "team-b",
			Name:             "db-2",
			MajorVersion:     "17",
			HighAvailability: "false",
			Phase:            testPhaseCompleted,
		})

		if got := len(collectMetrics(ResourceInfo)); got != 2 {
			t.Fatalf("expected 2 metrics before removal, got %d", got)
		}

		RemoveResourceInfo(ResourceLabels{
			ResourceType: testResourceType,
			Namespace:    "team-a",
			Name:         "db-1",
		})

		metrics := collectMetrics(ResourceInfo)
		if len(metrics) != 1 {
			t.Fatalf("expected 1 metric after removal, got %d", len(metrics))
		}
		if got := getLabelValue(metrics[0], "namespace"); got != "team-b" {
			t.Errorf("namespace = %q, want %q", got, "team-b")
		}
	})

	t.Run("removes all phases for a resource via DeletePartialMatch", func(t *testing.T) {
		ResourceInfo.Reset()

		SetResourceInfo(ResourceLabels{
			ResourceType:     testResourceType,
			Namespace:        "team-a",
			Name:             "db-1",
			MajorVersion:     testMajorVersion,
			HighAvailability: "true",
			Phase:            testPhasePreparing,
		})
		SetResourceInfo(ResourceLabels{
			ResourceType:     testResourceType,
			Namespace:        "team-a",
			Name:             "db-1",
			MajorVersion:     testMajorVersion,
			HighAvailability: "true",
			Phase:            testPhaseCompleted,
		})

		if got := len(collectMetrics(ResourceInfo)); got != 2 {
			t.Fatalf("expected 2 metrics before removal, got %d", got)
		}

		RemoveResourceInfo(ResourceLabels{
			ResourceType: testResourceType,
			Namespace:    "team-a",
			Name:         "db-1",
		})

		if got := len(collectMetrics(ResourceInfo)); got != 0 {
			t.Errorf("expected 0 metrics after removal, got %d", got)
		}
	})
}

func TestRemoveBeforeSetPattern(t *testing.T) {
	t.Run("prevents stale phase series when remove is called before set", func(t *testing.T) {
		ResourceInfo.Reset()

		labels := ResourceLabels{
			ResourceType:     testResourceType,
			Namespace:        testNamespace,
			Name:             testName,
			MajorVersion:     testMajorVersion,
			HighAvailability: "true",
			Phase:            testPhasePreparing,
		}
		SetResourceInfo(labels)

		RemoveResourceInfo(ResourceLabels{
			ResourceType: testResourceType,
			Namespace:    testNamespace,
			Name:         testName,
		})
		labels.Phase = testPhaseCompleted
		SetResourceInfo(labels)

		metrics := collectMetrics(ResourceInfo)
		if len(metrics) != 1 {
			t.Fatalf("no stale phase series should remain, got %d metrics", len(metrics))
		}
		if got := getLabelValue(metrics[0], "phase"); got != testPhaseCompleted {
			t.Errorf("phase = %q, want %q", got, testPhaseCompleted)
		}
	})

	t.Run("prevents stale version series on upgrade", func(t *testing.T) {
		ResourceInfo.Reset()

		SetResourceInfo(ResourceLabels{
			ResourceType:     testResourceType,
			Namespace:        testNamespace,
			Name:             testName,
			MajorVersion:     "15",
			HighAvailability: "true",
			Phase:            testPhaseCompleted,
		})

		RemoveResourceInfo(ResourceLabels{
			ResourceType: testResourceType,
			Namespace:    testNamespace,
			Name:         testName,
		})
		SetResourceInfo(ResourceLabels{
			ResourceType:     testResourceType,
			Namespace:        testNamespace,
			Name:             testName,
			MajorVersion:     testMajorVersion,
			HighAvailability: "true",
			Phase:            testPhaseCompleted,
		})

		metrics := collectMetrics(ResourceInfo)
		if len(metrics) != 1 {
			t.Fatalf("no stale version series should remain, got %d metrics", len(metrics))
		}
		if got := getLabelValue(metrics[0], "major_version"); got != testMajorVersion {
			t.Errorf("major_version = %q, want %q", got, testMajorVersion)
		}
	})
}

func TestObserveReconcileDuration(t *testing.T) {
	ReconcileDuration.Reset()

	ObserveReconcileDuration(testResourceType, "success", 500*time.Millisecond)
	ObserveReconcileDuration(testResourceType, "error", 100*time.Millisecond)

	metrics := collectMetrics(ReconcileDuration)
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}

	for _, m := range metrics {
		result := getLabelValue(m, "result")
		if got := m.GetHistogram().GetSampleCount(); got != 1 {
			t.Errorf("result=%s: sample count = %d, want 1", result, got)
		}
		switch result {
		case "success":
			if got := m.GetHistogram().GetSampleSum(); got < 0.4 || got > 0.6 {
				t.Errorf("success sample sum = %v, want ~0.5", got)
			}
		case "error":
			if got := m.GetHistogram().GetSampleSum(); got < 0.09 || got > 0.11 {
				t.Errorf("error sample sum = %v, want ~0.1", got)
			}
		default:
			t.Fatalf("unexpected result label: %s", result)
		}
	}
}

func TestIncReconcileError(t *testing.T) {
	ReconcileErrors.Reset()

	IncReconcileError(testResourceType, testNamespace, testPhasePreparing)
	IncReconcileError(testResourceType, testNamespace, testPhasePreparing)
	IncReconcileError(testResourceType, testNamespace, "PerformActions")

	metrics := collectMetrics(ReconcileErrors)
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}

	for _, m := range metrics {
		phase := getLabelValue(m, "phase")
		switch phase {
		case testPhasePreparing:
			if got := m.GetCounter().GetValue(); got != 2.0 {
				t.Errorf("phase=%s: counter value = %v, want 2.0", phase, got)
			}
		case "PerformActions":
			if got := m.GetCounter().GetValue(); got != 1.0 {
				t.Errorf("phase=%s: counter value = %v, want 1.0", phase, got)
			}
		default:
			t.Fatalf("unexpected phase: %s", phase)
		}
	}
}
