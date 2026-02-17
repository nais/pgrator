package v1

import (
	"context"
	"strings"

	"github.com/nais/pgrator/pkg/api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("Valkey Webhook Validation", func() {
	var validator *ValkeyValidator

	BeforeEach(func() {
		validator = &ValkeyValidator{}
	})

	Describe("ValidateCreate", func() {
		It("should allow any valid Valkey resource", func() {
			valkey := &Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-valkey",
					Namespace: "my-team",
				},
				Spec: ValkeySpec{
					Tier:   ValkeyTierSingleNode,
					Memory: ValkeyMemory4GB,
				},
			}

			_, err := validator.ValidateCreate(context.Background(), valkey)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow name at exactly the max length", func() {
			namespace := "my-team"
			// max = 63 - 8 - len("my-team") = 48
			name := strings.Repeat("a", 48)

			valkey := &Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: ValkeySpec{
					Tier:   ValkeyTierSingleNode,
					Memory: ValkeyMemory4GB,
				},
			}

			_, err := validator.ValidateCreate(context.Background(), valkey)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should reject name that is too long", func() {
			namespace := "my-team"
			// max = 63 - 8 - len("my-team") = 48, so 49 should fail
			name := strings.Repeat("a", 49)

			valkey := &Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: ValkeySpec{
					Tier:   ValkeyTierSingleNode,
					Memory: ValkeyMemory4GB,
				},
			}

			_, err := validator.ValidateCreate(context.Background(), valkey)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("metadata.name is too long"))
		})

		It("should reject resource when namespace is excessively long", func() {
			// Choose a namespace long enough that maxNameLength becomes zero or negative
			// maxNameLength = 63 - 8 - len(namespace); len(namespace) = 60 => maxNameLength = -5
			namespace := strings.Repeat("n", 60)
			valkey := &Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "valkey",
					Namespace: namespace,
				},
				Spec: ValkeySpec{
					Tier:   ValkeyTierSingleNode,
					Memory: ValkeyMemory4GB,
				},
			}
			_, err := validator.ValidateCreate(context.Background(), valkey)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("ValidateUpdate", func() {
		It("should allow any update", func() {
			oldObj := &Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-valkey",
					Namespace: "my-team",
				},
				Spec: ValkeySpec{
					Tier:   ValkeyTierSingleNode,
					Memory: ValkeyMemory4GB,
				},
			}

			newObj := &Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-valkey",
					Namespace: "my-team",
				},
				Spec: ValkeySpec{
					Tier:   ValkeyTierHighAvailability,
					Memory: ValkeyMemory8GB,
				},
			}

			_, err := validator.ValidateUpdate(context.Background(), oldObj, newObj)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("ValidateDelete", func() {
		It("should allow deletion when allowDeletion annotation is true", func() {
			obj := &Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-valkey",
					Namespace: "my-team",
					Annotations: map[string]string{
						api.AllowDeletionAnnotation: "true",
					},
				},
				Spec: ValkeySpec{
					Tier:   ValkeyTierSingleNode,
					Memory: ValkeyMemory4GB,
				},
			}

			_, err := validator.ValidateDelete(context.Background(), obj)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should refuse deletion when allowDeletion annotation is missing", func() {
			obj := &Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-valkey",
					Namespace: "my-team",
				},
				Spec: ValkeySpec{
					Tier:   ValkeyTierSingleNode,
					Memory: ValkeyMemory4GB,
				},
			}

			_, err := validator.ValidateDelete(context.Background(), obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("refusing deletion"))
		})

		It("should refuse deletion when allowDeletion annotation is not true", func() {
			obj := &Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-valkey",
					Namespace: "my-team",
					Annotations: map[string]string{
						api.AllowDeletionAnnotation: "false",
					},
				},
				Spec: ValkeySpec{
					Tier:   ValkeyTierSingleNode,
					Memory: ValkeyMemory4GB,
				},
			}

			_, err := validator.ValidateDelete(context.Background(), obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("refusing deletion"))
		})
	})
})
