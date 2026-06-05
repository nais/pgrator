package valkey

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/nais/pgrator/pkg/api/v1"
)

var _ = Describe("Valkey Resource Creator", func() {
	Describe("ServiceName", func() {
		It("should return namespaced name in format valkey-{team}-{name}", func() {
			obj := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-valkey",
					Namespace: "my-team",
				},
			}

			result := ServiceName(obj)
			Expect(result).To(Equal("valkey-my-team-my-valkey"))
		})

		It("should handle different team and instance names", func() {
			obj := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cache",
					Namespace: "production",
				},
			}

			result := ServiceName(obj)
			Expect(result).To(Equal("valkey-production-cache"))
		})

		It("should handle names with hyphens", func() {
			obj := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-app-cache",
					Namespace: "my-awesome-team",
				},
			}

			result := ServiceName(obj)
			Expect(result).To(Equal("valkey-my-awesome-team-my-app-cache"))
		})
	})
})
