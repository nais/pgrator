package resourcecreator

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("makeSingleQuery", func() {
	It("should create a basic query without rate", func() {
		result := makeSingleQuery("container_memory_usage_bytes", "pod", []string{
			"container=\"postgres\"",
			"namespace=\"test-namespace\"",
			"pod=~\"test-cluster-[0-9]\"",
		}, false)
		expected := `avg(container_memory_usage_bytes{container="postgres", namespace="test-namespace", pod=~"test-cluster-[0-9]"}) by (pod)`
		Expect(result).To(Equal(expected))
	})

	It("should create a query with additional labels", func() {
		result := makeSingleQuery("kube_pod_container_resource_limits", "pod", []string{
			"container=\"postgres\"",
			"namespace=\"test-namespace\"",
			"pod=~\"test-cluster-[0-9]\"",
			"resource=\"memory\"",
		}, false)
		expected := `avg(kube_pod_container_resource_limits{container="postgres", namespace="test-namespace", pod=~"test-cluster-[0-9]", resource="memory"}) by (pod)`
		Expect(result).To(Equal(expected))
	})

	It("should create a rate query when rate is true", func() {
		result := makeSingleQuery("container_cpu_usage_seconds_total", "pvc", []string{
			"container=\"postgres\"",
			"namespace=\"test-namespace\"",
			"pvc=~\"test-cluster-[0-9]\"",
		}, true)
		expected := `avg(rate(container_cpu_usage_seconds_total{container="postgres", namespace="test-namespace", pvc=~"test-cluster-[0-9]"}[5m])) by (pvc)`
		Expect(result).To(Equal(expected))
	})

	It("should create a rate query with additional labels", func() {
		result := makeSingleQuery("container_cpu_usage_seconds_total", "pod", []string{
			"container=\"postgres\"",
			"namespace=\"test-namespace\"",
			"pod=~\"test-cluster-[0-9]\"",
			"resource=\"cpu\"",
		}, true)
		expected := `avg(rate(container_cpu_usage_seconds_total{container="postgres", namespace="test-namespace", pod=~"test-cluster-[0-9]", resource="cpu"}[5m])) by (pod)`
		Expect(result).To(Equal(expected))
	})

	It("should handle different namespace names", func() {
		result := makeSingleQuery("test_metric", "pod", []string{
			"container=\"postgres\"",
			"namespace=\"prod-environment\"",
			"pod=~\"my-pg-cluster-[0-9]\"",
		}, false)
		expected := `avg(test_metric{container="postgres", namespace="prod-environment", pod=~"my-pg-cluster-[0-9]"}) by (pod)`
		Expect(result).To(Equal(expected))
	})
})
