package fqdnpolicy

import (
	fqdnv1alpha3 "github.com/nais/pgrator/internal/thirdparty/google/networking/v1alpha3"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var _ = Describe("WAL FQDN policy", func() {
	It("allows only the Google endpoints needed by matching CNPG pods", func() {
		scheme := runtime.NewScheme()
		Expect(v1.AddToScheme(scheme)).To(Succeed())
		Expect(fqdnv1alpha3.AddToScheme(scheme)).To(Succeed())
		postgres := &v1.Postgres{
			TypeMeta: metav1.TypeMeta{APIVersion: "nais.io/v1", Kind: "Postgres"},
			ObjectMeta: metav1.ObjectMeta{
				Name: "database", Namespace: "team", UID: "postgres-uid",
			},
		}

		policy, err := Create(scheme, postgres, "database")
		Expect(err).NotTo(HaveOccurred())

		Expect(policy.Name).To(Equal("database-wal"))
		Expect(policy.Spec.PodSelector.MatchLabels).To(Equal(map[string]string{"cnpg.io/cluster": "database"}))
		Expect(policy.Spec.PolicyTypes).To(ConsistOf(networkingv1.PolicyTypeEgress))
		Expect(policy.Spec.Egress).To(HaveLen(2))
		Expect(policy.Spec.Egress[0].To[0].FQDNs).To(Equal([]string{"metadata.google.internal"}))
		Expect(policy.Spec.Egress[0].Ports[0].Protocol).To(HaveValue(Equal(corev1.ProtocolTCP)))
		Expect(policy.Spec.Egress[0].Ports[0].Port.IntValue()).To(Equal(80))
		Expect(policy.Spec.Egress[1].To[0].FQDNs).To(Equal([]string{"private.googleapis.com"}))
		Expect(policy.Spec.Egress[1].Ports[0].Protocol).To(HaveValue(Equal(corev1.ProtocolTCP)))
		Expect(policy.Spec.Egress[1].Ports[0].Port.IntValue()).To(Equal(443))
		Expect(policy.OwnerReferences).To(ContainElement(SatisfyAll(
			HaveField("Name", "database"),
			HaveField("Controller", HaveValue(BeTrue())),
		)))
	})
})
