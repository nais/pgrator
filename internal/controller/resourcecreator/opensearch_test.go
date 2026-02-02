package resourcecreator

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/nais/pgrator/pkg/api/v1"
)

var _ = Describe("OpenSearch Resource Creator", func() {
	Describe("AivenOpenSearchServiceName", func() {
		It("should return namespaced name in format opensearch-{team}-{name}", func() {
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-opensearch",
					Namespace: "my-team",
				},
			}

			result := AivenOpenSearchServiceName(opensearch)
			Expect(result).To(Equal("opensearch-my-team-my-opensearch"))
		})

		It("should handle different team and instance names", func() {
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "search",
					Namespace: "production",
				},
			}

			result := AivenOpenSearchServiceName(opensearch)
			Expect(result).To(Equal("opensearch-production-search"))
		})

		It("should handle names with hyphens", func() {
			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-app-search",
					Namespace: "my-awesome-team",
				},
			}

			result := AivenOpenSearchServiceName(opensearch)
			Expect(result).To(Equal("opensearch-my-awesome-team-my-app-search"))
		})
	})
})
