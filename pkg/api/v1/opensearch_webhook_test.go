package v1

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("OpenSearch Webhook Validation", func() {
	var validator *OpenSearchValidator

	BeforeEach(func() {
		validator = &OpenSearchValidator{}
	})

	Describe("ValidateCreate", func() {
		DescribeTable("valid configurations",
			func(name, namespace string, tier OpenSearchTier, memory OpenSearchMemory, version OpenSearchMajorVersion, storageGB int) {
				opensearch := &OpenSearch{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: namespace,
					},
					Spec: OpenSearchSpec{
						Tier:         tier,
						Memory:       memory,
						MajorVersion: version,
						StorageGB:    storageGB,
					},
				}

				_, err := validator.ValidateCreate(context.Background(), opensearch)
				Expect(err).NotTo(HaveOccurred())
			},
			Entry("SingleNode 4GB with valid storage",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchMajorVersionV2, 80),
			Entry("HighAvailability 8GB with valid storage",
				"my-opensearch", "my-team",
				OpenSearchTierHighAvailability, OpenSearchMemory8GB, OpenSearchMajorVersionV2_19, 525),
			Entry("hobbyist plan with exact storage",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory2GB, OpenSearchMajorVersionV1, 16),
			Entry("storage at boundary (min) for HA 16GB",
				"my-opensearch", "my-team",
				OpenSearchTierHighAvailability, OpenSearchMemory16GB, OpenSearchMajorVersionV3_3, 1050),
			Entry("storage at boundary (max) for HA 16GB",
				"my-opensearch", "my-team",
				OpenSearchTierHighAvailability, OpenSearchMemory16GB, OpenSearchMajorVersionV3_3, 5250),
			Entry("SingleNode 8GB storage with increment",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory8GB, OpenSearchMajorVersionV2, 185),
			Entry("HighAvailability storage with 30GB increment",
				"my-opensearch", "my-team",
				OpenSearchTierHighAvailability, OpenSearchMemory4GB, OpenSearchMajorVersionV2, 270),
		)

		DescribeTable("invalid configurations",
			func(name, namespace string, tier OpenSearchTier, memory OpenSearchMemory, version OpenSearchMajorVersion, storageGB int, expectedError string) {
				opensearch := &OpenSearch{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: namespace,
					},
					Spec: OpenSearchSpec{
						Tier:         tier,
						Memory:       memory,
						MajorVersion: version,
						StorageGB:    storageGB,
					},
				}

				_, err := validator.ValidateCreate(context.Background(), opensearch)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedError))
			},
			Entry("HighAvailability with 2GB memory (not supported)",
				"my-opensearch", "my-team",
				OpenSearchTierHighAvailability, OpenSearchMemory2GB, OpenSearchMajorVersionV2, 100,
				"invalid tier/memory combination"),
			Entry("storage below minimum for SingleNode 4GB",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchMajorVersionV2, 50,
				"storage must be at least 80GB"),
			Entry("storage above maximum for SingleNode 4GB",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchMajorVersionV2, 500,
				"storage must be at most 400GB"),
			Entry("storage not in valid increments for SingleNode 4GB",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchMajorVersionV2, 85,
				"storage must be in increments of 10GB"),
			Entry("hobbyist plan with wrong storage",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory2GB, OpenSearchMajorVersionV1, 32,
				"storage for hobbyist plan must be exactly 16GB"),
			Entry("name too long for generated service name",
				"this-is-a-very-long-opensearch-instance-name-that-exceeds-limit", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchMajorVersionV2, 80,
				"metadata.name is too long"),
			Entry("HighAvailability storage not in 30GB increments",
				"my-opensearch", "my-team",
				OpenSearchTierHighAvailability, OpenSearchMemory4GB, OpenSearchMajorVersionV2, 250,
				"storage must be in increments of 30GB"),
		)
	})

	Describe("ValidateUpdate", func() {
		It("should allow valid storage update", func() {
			oldObj := &OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-opensearch",
					Namespace: "my-team",
				},
				Spec: OpenSearchSpec{
					Tier:         OpenSearchTierSingleNode,
					Memory:       OpenSearchMemory4GB,
					MajorVersion: OpenSearchMajorVersionV2,
					StorageGB:    80,
				},
			}

			newObj := &OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-opensearch",
					Namespace: "my-team",
				},
				Spec: OpenSearchSpec{
					Tier:         OpenSearchTierSingleNode,
					Memory:       OpenSearchMemory4GB,
					MajorVersion: OpenSearchMajorVersionV2,
					StorageGB:    90,
				},
			}

			_, err := validator.ValidateUpdate(context.Background(), oldObj, newObj)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("ValidateDelete", func() {
		It("should allow deletion", func() {
			obj := &OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-opensearch",
					Namespace: "my-team",
				},
				Spec: OpenSearchSpec{
					Tier:         OpenSearchTierSingleNode,
					Memory:       OpenSearchMemory4GB,
					MajorVersion: OpenSearchMajorVersionV2,
					StorageGB:    80,
				},
			}

			_, err := validator.ValidateDelete(context.Background(), obj)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
