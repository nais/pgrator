package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	testPhaseCompleted   = "Completed"
	testPhasePreparing   = "Preparing"
	testResourceType     = "postgres.data.nais.io"
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

var _ = Describe("SetResourceInfo", func() {
	BeforeEach(func() {
		ResourceInfo.Reset()
	})

	It("sets a gauge with correct labels and value 1", func() {
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
		Expect(metrics).To(HaveLen(1))

		m := metrics[0]
		Expect(m.GetGauge().GetValue()).To(Equal(1.0))
		Expect(getLabelValue(m, "resource_type")).To(Equal(testResourceType))
		Expect(getLabelValue(m, "namespace")).To(Equal(testNamespace))
		Expect(getLabelValue(m, "name")).To(Equal(testName))
		Expect(getLabelValue(m, "major_version")).To(Equal(testMajorVersion))
		Expect(getLabelValue(m, "high_availability")).To(Equal(testHighAvailability))
		Expect(getLabelValue(m, "phase")).To(Equal(testPhaseCompleted))
	})

	It("creates a new time series when phase changes (old series still exists)", func() {
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
		Expect(metrics).To(HaveLen(2), "both Preparing and Completed label sets should exist")
	})

	It("handles empty optional labels (e.g. Valkey/OpenSearch)", func() {
		labels := ResourceLabels{
			ResourceType: "valkey.nais.io",
			Namespace:    testNamespace,
			Name:         "my-cache",
			Phase:        testPhaseCompleted,
		}
		SetResourceInfo(labels)

		metrics := collectMetrics(ResourceInfo)
		Expect(metrics).To(HaveLen(1))

		m := metrics[0]
		Expect(getLabelValue(m, "major_version")).To(BeEmpty())
		Expect(getLabelValue(m, "high_availability")).To(BeEmpty())
	})
})

var _ = Describe("RemoveResourceInfo", func() {
	BeforeEach(func() {
		ResourceInfo.Reset()
	})

	It("removes only the matching resource, leaving others intact", func() {
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

		Expect(collectMetrics(ResourceInfo)).To(HaveLen(2))

		RemoveResourceInfo(ResourceLabels{
			ResourceType: testResourceType,
			Namespace:    "team-a",
			Name:         "db-1",
		})

		metrics := collectMetrics(ResourceInfo)
		Expect(metrics).To(HaveLen(1))
		Expect(getLabelValue(metrics[0], "namespace")).To(Equal("team-b"))
	})

	It("removes all phases for a resource via DeletePartialMatch", func() {
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

		Expect(collectMetrics(ResourceInfo)).To(HaveLen(2))

		RemoveResourceInfo(ResourceLabels{
			ResourceType: testResourceType,
			Namespace:    "team-a",
			Name:         "db-1",
		})

		Expect(collectMetrics(ResourceInfo)).To(BeEmpty())
	})
})

var _ = Describe("remove-before-set pattern", func() {
	BeforeEach(func() {
		ResourceInfo.Reset()
	})

	It("prevents stale phase series when remove is called before set", func() {
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
		Expect(metrics).To(HaveLen(1), "no stale phase series should remain")
		Expect(getLabelValue(metrics[0], "phase")).To(Equal(testPhaseCompleted))
	})

	It("prevents stale version series on upgrade", func() {
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
		Expect(metrics).To(HaveLen(1), "no stale version series should remain")
		Expect(getLabelValue(metrics[0], "major_version")).To(Equal(testMajorVersion))
	})
})

var _ = Describe("ObserveReconcileDuration", func() {
	BeforeEach(func() {
		ReconcileDuration.Reset()
	})

	It("records histogram observations per result label", func() {
		ObserveReconcileDuration(testResourceType, "success", 500*time.Millisecond)
		ObserveReconcileDuration(testResourceType, "error", 100*time.Millisecond)

		metrics := collectMetrics(ReconcileDuration)
		Expect(metrics).To(HaveLen(2))

		for _, m := range metrics {
			result := getLabelValue(m, "result")
			Expect(m.GetHistogram().GetSampleCount()).To(BeEquivalentTo(1), "result=%s", result)
			switch result {
			case "success":
				Expect(m.GetHistogram().GetSampleSum()).To(BeNumerically("~", 0.5, 0.1))
			case "error":
				Expect(m.GetHistogram().GetSampleSum()).To(BeNumerically("~", 0.1, 0.01))
			default:
				Fail("unexpected result label: " + result)
			}
		}
	})
})

var _ = Describe("IncReconcileError", func() {
	BeforeEach(func() {
		ReconcileErrors.Reset()
	})

	It("increments counters per phase label", func() {
		IncReconcileError(testResourceType, testNamespace, testPhasePreparing)
		IncReconcileError(testResourceType, testNamespace, testPhasePreparing)
		IncReconcileError(testResourceType, testNamespace, "PerformActions")

		metrics := collectMetrics(ReconcileErrors)
		Expect(metrics).To(HaveLen(2))

		for _, m := range metrics {
			phase := getLabelValue(m, "phase")
			switch phase {
			case testPhasePreparing:
				Expect(m.GetCounter().GetValue()).To(Equal(2.0))
			case "PerformActions":
				Expect(m.GetCounter().GetValue()).To(Equal(1.0))
			default:
				Fail("unexpected phase: " + phase)
			}
		}
	})
})
