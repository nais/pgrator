package v1

import (
	"context"
	"strings"

	"github.com/nais/pgrator/pkg/api"
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
			func(name, namespace string, tier OpenSearchTier, memory OpenSearchMemory, version OpenSearchVersion, storageGB int) {
				opensearch := &OpenSearch{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: namespace,
					},
					Spec: OpenSearchSpec{
						Tier:      tier,
						Memory:    memory,
						Version:   version,
						StorageGB: storageGB,
					},
				}

				_, err := validator.ValidateCreate(context.Background(), opensearch)
				Expect(err).NotTo(HaveOccurred())
			},
			Entry("SingleNode 4GB with valid storage",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 80),
			Entry("HighAvailability 8GB with valid storage",
				"my-opensearch", "my-team",
				OpenSearchTierHighAvailability, OpenSearchMemory8GB, OpenSearchVersionV2_19, 525),
			Entry("hobbyist plan with exact storage",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory2GB, OpenSearchVersionV1, 16),
			Entry("storage at boundary (min) for HA 16GB",
				"my-opensearch", "my-team",
				OpenSearchTierHighAvailability, OpenSearchMemory16GB, OpenSearchVersionV3_3, 1050),
			Entry("storage at boundary (max) for HA 16GB",
				"my-opensearch", "my-team",
				OpenSearchTierHighAvailability, OpenSearchMemory16GB, OpenSearchVersionV3_3, 5250),
			Entry("SingleNode 8GB storage with increment",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory8GB, OpenSearchVersionV2, 185),
			Entry("HighAvailability storage with 30GB increment",
				"my-opensearch", "my-team",
				OpenSearchTierHighAvailability, OpenSearchMemory4GB, OpenSearchVersionV2, 270),
			Entry("Name is at max length (63-len('opensearch-')-len(namespace))=44",
				strings.Repeat("a", 44), "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 80),
		)

		DescribeTable("invalid configurations",
			func(name, namespace string, tier OpenSearchTier, memory OpenSearchMemory, version OpenSearchVersion, storageGB int, expectedError string) {
				opensearch := &OpenSearch{
					ObjectMeta: metav1.ObjectMeta{
						Name:      name,
						Namespace: namespace,
					},
					Spec: OpenSearchSpec{
						Tier:      tier,
						Memory:    memory,
						Version:   version,
						StorageGB: storageGB,
					},
				}

				_, err := validator.ValidateCreate(context.Background(), opensearch)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(expectedError))
			},
			Entry("HighAvailability with 2GB memory (not supported)",
				"my-opensearch", "my-team",
				OpenSearchTierHighAvailability, OpenSearchMemory2GB, OpenSearchVersionV2, 100,
				"invalid tier/memory combination"),
			Entry("storage below minimum for SingleNode 4GB",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 50,
				"storage must be at least 80GB"),
			Entry("storage above maximum for SingleNode 4GB",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 500,
				"storage must be at most 400GB"),
			Entry("storage not in valid increments for SingleNode 4GB",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 85,
				"storage must be in increments of 10GB"),
			Entry("hobbyist plan with wrong storage",
				"my-opensearch", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory2GB, OpenSearchVersionV1, 32,
				"storage must be at most 16GB"),
			Entry("name too long for generated service name",
				"this-is-a-very-long-opensearch-instance-name-that-exceeds-limit", "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 80,
				"metadata.name is too long"),
			Entry("HighAvailability storage not in 30GB increments",
				"my-opensearch", "my-team",
				OpenSearchTierHighAvailability, OpenSearchMemory4GB, OpenSearchVersionV2, 250,
				"storage must be in increments of 30GB"),
			Entry("Name exceeds max length (63-len('opensearch-')-len(namespace))=44",
				strings.Repeat("a", 45), "my-team",
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 80,
				"metadata.name is too long; max length is 44 characters"),
			Entry("Namespace exceeds max length (63-len('opensearch-')-len(namespace))<=0",
				"my-opensearch", strings.Repeat("n", 60),
				OpenSearchTierSingleNode, OpenSearchMemory4GB, OpenSearchVersionV2, 80,
				"metadata.namespace is too long"),
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
					Tier:      OpenSearchTierSingleNode,
					Memory:    OpenSearchMemory4GB,
					Version:   OpenSearchVersionV2,
					StorageGB: 80,
				},
			}

			newObj := &OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-opensearch",
					Namespace: "my-team",
				},
				Spec: OpenSearchSpec{
					Tier:      OpenSearchTierSingleNode,
					Memory:    OpenSearchMemory4GB,
					Version:   OpenSearchVersionV2,
					StorageGB: 90,
				},
			}

			_, err := validator.ValidateUpdate(context.Background(), oldObj, newObj)
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("valid version upgrades",
			func(oldVersion, newVersion OpenSearchVersion) {
				oldObj := &OpenSearch{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-opensearch",
						Namespace: "my-team",
					},
					Spec: OpenSearchSpec{
						Tier:      OpenSearchTierSingleNode,
						Memory:    OpenSearchMemory4GB,
						Version:   oldVersion,
						StorageGB: 80,
					},
				}

				newObj := &OpenSearch{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-opensearch",
						Namespace: "my-team",
					},
					Spec: OpenSearchSpec{
						Tier:      OpenSearchTierSingleNode,
						Memory:    OpenSearchMemory4GB,
						Version:   newVersion,
						StorageGB: 80,
					},
				}

				_, err := validator.ValidateUpdate(context.Background(), oldObj, newObj)
				Expect(err).NotTo(HaveOccurred())
			},
			Entry("V1 to V2", OpenSearchVersionV1, OpenSearchVersionV2),
			Entry("V1 to V2.19", OpenSearchVersionV1, OpenSearchVersionV2_19),
			Entry("V2 to V2.19", OpenSearchVersionV2, OpenSearchVersionV2_19),
			Entry("V2.19 to V3.3", OpenSearchVersionV2_19, OpenSearchVersionV3_3),
			Entry("same version V1", OpenSearchVersionV1, OpenSearchVersionV1),
			Entry("same version V2", OpenSearchVersionV2, OpenSearchVersionV2),
			Entry("same version V2.19", OpenSearchVersionV2_19, OpenSearchVersionV2_19),
			Entry("same version V3.3", OpenSearchVersionV3_3, OpenSearchVersionV3_3),
		)

		DescribeTable("invalid version upgrades",
			func(oldVersion, newVersion OpenSearchVersion, expectedError string) {
				oldObj := &OpenSearch{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-opensearch",
						Namespace: "my-team",
					},
					Spec: OpenSearchSpec{
						Tier:      OpenSearchTierSingleNode,
						Memory:    OpenSearchMemory4GB,
						Version:   oldVersion,
						StorageGB: 80,
					},
				}

				newObj := &OpenSearch{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-opensearch",
						Namespace: "my-team",
					},
					Spec: OpenSearchSpec{
						Tier:      OpenSearchTierSingleNode,
						Memory:    OpenSearchMemory4GB,
						Version:   newVersion,
						StorageGB: 80,
					},
				}

				_, err := validator.ValidateUpdate(context.Background(), oldObj, newObj)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal(expectedError))
			},
			Entry("V1 to V3.3 (skipping versions)",
				OpenSearchVersionV1, OpenSearchVersionV3_3,
				"validation failed: cannot change OpenSearch version from 1 to 3.3: new version must be one of [2, 2.19]"),
			Entry("V2 to V3.3 (skipping V2.19)",
				OpenSearchVersionV2, OpenSearchVersionV3_3,
				"validation failed: cannot change OpenSearch version from 2 to 3.3: new version must be one of [2.19]"),
			Entry("V3.3 to V2 (downgrade)",
				OpenSearchVersionV3_3, OpenSearchVersionV2,
				"validation failed: cannot change OpenSearch version from 3.3 to 2: no further upgrades available"),
			Entry("V3.3 to V1 (downgrade)",
				OpenSearchVersionV3_3, OpenSearchVersionV1,
				"validation failed: cannot change OpenSearch version from 3.3 to 1: no further upgrades available"),
			Entry("V2.19 to V1 (downgrade)",
				OpenSearchVersionV2_19, OpenSearchVersionV1,
				"validation failed: cannot change OpenSearch version from 2.19 to 1: new version must be one of [3.3]"),
			Entry("V2 to V1 (downgrade)",
				OpenSearchVersionV2, OpenSearchVersionV1,
				"validation failed: cannot change OpenSearch version from 2 to 1: new version must be one of [2.19]"),
		)
	})

	Describe("ValidateDelete", func() {
		It("should allow deletion when annotation is present and true", func() {
			obj := &OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-opensearch",
					Namespace: "my-team",
					Annotations: map[string]string{
						api.AllowDeletionAnnotation: "true",
					},
				},
				Spec: OpenSearchSpec{
					Tier:      OpenSearchTierSingleNode,
					Memory:    OpenSearchMemory4GB,
					Version:   OpenSearchVersionV2,
					StorageGB: 80,
				},
			}

			_, err := validator.ValidateDelete(context.Background(), obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should refuse deletion when annotation is missing", func() {
			obj := &OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-opensearch",
					Namespace: "my-team",
				},
				Spec: OpenSearchSpec{
					Tier:      OpenSearchTierSingleNode,
					Memory:    OpenSearchMemory4GB,
					Version:   OpenSearchVersionV2,
					StorageGB: 80,
				},
			}

			_, err := validator.ValidateDelete(context.Background(), obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("nais.io/allowDeletion"))
		})

		It("should refuse deletion when annotation is set to false", func() {
			obj := &OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-opensearch",
					Namespace: "my-team",
					Annotations: map[string]string{
						api.AllowDeletionAnnotation: "false",
					},
				},
				Spec: OpenSearchSpec{
					Tier:      OpenSearchTierSingleNode,
					Memory:    OpenSearchMemory4GB,
					Version:   OpenSearchVersionV2,
					StorageGB: 80,
				},
			}

			_, err := validator.ValidateDelete(context.Background(), obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("nais.io/allowDeletion"))
		})
	})
})
