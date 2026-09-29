package access

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nais/pgrator/internal/initscheme"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	initscheme.InitScheme(s)
	return s
}

func TestRelayProofMatchesRawTokenDigest(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 43 {
		t.Fatalf("token length = %d", len(token))
	}
	digest, err := TokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != 64 {
		t.Errorf("digest length = %d", len(digest))
	}
	for _, bad := range []string{"", "not-base64!", strings.Repeat("a", 43)} {
		if _, err := TokenDigest(bad); err == nil {
			t.Errorf("accepted invalid token %q", bad)
		}
	}
}

func TestRelayAccessContainsOnlyHashedProof(t *testing.T) {
	a := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "my-access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{PostgresBranch: "orders"}}
	token, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := TokenDigest(token)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := CreateTokenSecret(testScheme(t), a, token)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := CreateRelayAccess(testScheme(t), a, digest)
	if err != nil {
		t.Fatal(err)
	}
	if secret.Type != corev1.SecretTypeOpaque || string(secret.Data[TokenKey]) != token {
		t.Fatal("token not persisted in separate opaque Secret")
	}
	if relay.GetName() != a.Name || relay.GetAPIVersion() != "nais.io/v1alpha1" || relay.Object["spec"].(map[string]any)["tokenSHA256"] != digest {
		t.Errorf("relay mapping = %+v", relay.Object)
	}
	target := relay.Object["spec"].(map[string]any)["target"].(map[string]any)
	if target["serviceName"] != "pg-orders-rw" || target["port"] != int64(5432) {
		t.Errorf("target = %v", target)
	}
	encoded, err := json.Marshal(relay.Object)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), token) {
		t.Fatal("plaintext leaked into mapping")
	}
	if metav1.GetControllerOf(relay) == nil || metav1.GetControllerOf(secret) == nil {
		t.Error("artifacts must be controlled by PostgresAccess")
	}
}

func TestRelayIngressIsNarrow(t *testing.T) {
	a := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "my-access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{PostgresBranch: "orders"}}
	policy, err := CreateRelayNetworkPolicy(testScheme(t), a)
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Spec.PolicyTypes) != 1 || policy.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Errorf("types = %v", policy.Spec.PolicyTypes)
	}
	if policy.Spec.PodSelector.MatchLabels["cnpg.io/cluster"] != "pg-orders" || policy.Spec.PodSelector.MatchLabels["cnpg.io/instanceRole"] != "primary" {
		t.Error("must select selected primary")
	}
	rule := policy.Spec.Ingress[0]
	peer := rule.From[0]
	if peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "nais-system" || peer.PodSelector.MatchLabels["app.kubernetes.io/name"] != "relay" || peer.PodSelector.MatchLabels["app.kubernetes.io/instance"] != "relay" {
		t.Errorf("peer = %v", peer)
	}
	if len(rule.Ports) != 1 || rule.Ports[0].Port.IntValue() != 5432 || *rule.Ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("ports = %v", rule.Ports)
	}
	if metav1.GetControllerOf(policy) == nil {
		t.Error("must be owned by access")
	}
}
