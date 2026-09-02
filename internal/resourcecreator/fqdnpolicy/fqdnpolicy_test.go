package fqdnpolicy

import (
	"testing"

	fqdnv1alpha3 "github.com/nais/pgrator/internal/thirdparty/google/networking/v1alpha3"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestCreateAllowsOnlyGoogleEndpointsForMatchingCNPGPods(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(v1): %v", err)
	}
	if err := fqdnv1alpha3.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(fqdnv1alpha3): %v", err)
	}
	postgres := &v1.Postgres{
		TypeMeta: metav1.TypeMeta{APIVersion: "nais.io/v1", Kind: "Postgres"},
		ObjectMeta: metav1.ObjectMeta{
			Name: "database", Namespace: "team", UID: "postgres-uid",
		},
	}

	policy, err := Create(scheme, postgres, "database")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if policy.Name != "database-wal" {
		t.Errorf("policy.Name = %q, want %q", policy.Name, "database-wal")
	}
	wantSelector := map[string]string{"cnpg.io/cluster": "database"}
	if len(policy.Spec.PodSelector.MatchLabels) != len(wantSelector) || policy.Spec.PodSelector.MatchLabels["cnpg.io/cluster"] != "database" {
		t.Errorf("PodSelector.MatchLabels = %v, want %v", policy.Spec.PodSelector.MatchLabels, wantSelector)
	}
	if len(policy.Spec.PolicyTypes) != 1 || policy.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Errorf("PolicyTypes = %v, want [%v]", policy.Spec.PolicyTypes, networkingv1.PolicyTypeEgress)
	}
	if len(policy.Spec.Egress) != 2 {
		t.Fatalf("len(Egress) = %d, want 2", len(policy.Spec.Egress))
	}

	first := policy.Spec.Egress[0]
	if got := first.To[0].FQDNs; len(got) != 1 || got[0] != "metadata.google.internal" {
		t.Errorf("Egress[0].To[0].FQDNs = %v, want [metadata.google.internal]", got)
	}
	if first.Ports[0].Protocol == nil || *first.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("Egress[0].Ports[0].Protocol = %v, want %v", first.Ports[0].Protocol, corev1.ProtocolTCP)
	}
	if first.Ports[0].Port.IntValue() != 80 {
		t.Errorf("Egress[0].Ports[0].Port = %d, want 80", first.Ports[0].Port.IntValue())
	}

	second := policy.Spec.Egress[1]
	if got := second.To[0].FQDNs; len(got) != 1 || got[0] != "private.googleapis.com" {
		t.Errorf("Egress[1].To[0].FQDNs = %v, want [private.googleapis.com]", got)
	}
	if second.Ports[0].Protocol == nil || *second.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("Egress[1].Ports[0].Protocol = %v, want %v", second.Ports[0].Protocol, corev1.ProtocolTCP)
	}
	if second.Ports[0].Port.IntValue() != 443 {
		t.Errorf("Egress[1].Ports[0].Port = %d, want 443", second.Ports[0].Port.IntValue())
	}

	var found bool
	for _, ref := range policy.OwnerReferences {
		if ref.Name == "database" && ref.Controller != nil && *ref.Controller {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("OwnerReferences = %+v, want an entry with Name=database and Controller=true", policy.OwnerReferences)
	}
}
