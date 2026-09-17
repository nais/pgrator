package access

import (
	"strings"
	"testing"

	"github.com/nais/pgrator/internal/initscheme"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
)

const postgresAccessKind = "PostgresAccess"

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	initscheme.InitScheme(s)
	return s
}

func TestCreateTunnelOwnedByPostgresAccess(t *testing.T) {
	access := &v1.PostgresAccess{
		ObjectMeta: metav1.ObjectMeta{Name: "my-access", Namespace: "team"},
		Spec: v1.PostgresAccessSpec{
			PostgresInstance:         "orders",
			Username:                 "user@nav.no",
			ClientWireGuardPublicKey: "client-pub",
		},
	}
	tunnel, err := CreateTunnel(testScheme(t), access)
	if err != nil {
		t.Fatalf("CreateTunnel: %v", err)
	}
	if tunnel.Name != access.Name {
		t.Errorf("tunnel name = %q, want %q", tunnel.Name, access.Name)
	}
	if tunnel.Namespace != access.Namespace {
		t.Errorf("tunnel namespace = %q, want %q", tunnel.Namespace, access.Namespace)
	}
	if tunnel.Spec.TeamSlug != access.Namespace {
		t.Errorf("tunnel teamSlug = %q, want %q", tunnel.Spec.TeamSlug, access.Namespace)
	}
	if tunnel.Spec.ClientPublicKey != access.Spec.ClientWireGuardPublicKey {
		t.Errorf("tunnel clientPublicKey = %q, want %q", tunnel.Spec.ClientPublicKey, access.Spec.ClientWireGuardPublicKey)
	}
	if tunnel.Spec.Target.Host != "pg-orders-rw.team.svc.cluster.local" {
		t.Errorf("tunnel target host = %q, want %q", tunnel.Spec.Target.Host, "pg-orders-rw.team.svc.cluster.local")
	}
	if tunnel.Spec.Target.Port != 5432 {
		t.Errorf("tunnel target port = %d, want 5432", tunnel.Spec.Target.Port)
	}
	if tunnel.Spec.ActiveDeadlineSeconds != nil {
		t.Errorf("tunnel activeDeadlineSeconds = %d, want nil; PostgresAccess expiry owns lifecycle", *tunnel.Spec.ActiveDeadlineSeconds)
	}
	if tunnel.Spec.Target.ResolvedIP != "" {
		t.Error("tunnel target must not use resolvedIP")
	}
	if tunnel.Spec.Target.PodSelector == nil {
		t.Fatal("tunnel target podSelector is nil")
	}
	if got := tunnel.Spec.Target.PodSelector.MatchLabels["cnpg.io/cluster"]; got != "pg-orders" {
		t.Errorf("tunnel podSelector cluster = %q, want pg-orders", got)
	}
	if got := tunnel.Spec.Target.PodSelector.MatchLabels["cnpg.io/instanceRole"]; got != "primary" {
		t.Errorf("tunnel podSelector role = %q, want primary", got)
	}
	refs := metav1.GetControllerOf(tunnel)
	if refs == nil {
		t.Fatal("Tunnel must be owned by PostgresAccess")
	}
	if refs.Kind != postgresAccessKind || refs.Name != access.Name {
		t.Errorf("tunnel owner = %s/%s, want PostgresAccess/%s", refs.Kind, refs.Name, access.Name)
	}
}

func TestCreateTunnelNetworkPolicyPermitsOnlyGatewayPods(t *testing.T) {
	access := &v1.PostgresAccess{
		ObjectMeta: metav1.ObjectMeta{Name: "my-access", Namespace: "team"},
		Spec:       v1.PostgresAccessSpec{PostgresInstance: "orders"},
	}
	netpol, err := CreateTunnelNetworkPolicy(testScheme(t), access)
	if err != nil {
		t.Fatalf("CreateTunnelNetworkPolicy: %v", err)
	}
	if netpol.Name != "my-access-tunnel" {
		t.Errorf("network policy name = %q, want %q", netpol.Name, "my-access-tunnel")
	}
	if len(netpol.Spec.PolicyTypes) != 1 || netpol.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Errorf("network policy policyTypes = %v, want [Ingress]", netpol.Spec.PolicyTypes)
	}
	if netpol.Spec.PodSelector.MatchLabels["cnpg.io/cluster"] != "pg-orders" {
		t.Error("network policy must select the CNPG primary")
	}
	if len(netpol.Spec.Ingress) != 1 {
		t.Fatalf("network policy ingress rules = %d, want 1", len(netpol.Spec.Ingress))
	}
	from := netpol.Spec.Ingress[0].From
	if len(from) != 1 || from[0].PodSelector == nil {
		t.Fatalf("ingress from = %v, want single podSelector", from)
	}
	peerLabels := from[0].PodSelector.MatchLabels
	if got := peerLabels["tunnels.nais.io/tunnel"]; got != access.Name {
		t.Errorf("ingress peer label = %q, want %q", got, access.Name)
	}
	if got := peerLabels["app.kubernetes.io/managed-by"]; got != "tunnel-operator" {
		t.Errorf("ingress peer managed-by = %q, want tunnel-operator", got)
	}
	if got := peerLabels["app.kubernetes.io/component"]; got != "tunnel-gateway" {
		t.Errorf("ingress peer component = %q, want tunnel-gateway; the policy must admit only tunnel-operator gateway pods", got)
	}
	ports := netpol.Spec.Ingress[0].Ports
	if len(ports) != 1 || ports[0].Protocol == nil || *ports[0].Protocol != corev1.ProtocolTCP || ports[0].Port.IntValue() != 5432 {
		t.Errorf("ingress ports = %v, want TCP/5432", ports)
	}
	refs := metav1.GetControllerOf(netpol)
	if refs == nil || refs.Kind != postgresAccessKind || refs.Name != access.Name {
		t.Error("network policy must be owned by PostgresAccess")
	}
}

// Long access names must still produce valid, distinct child resource names:
// the Tunnel name becomes a gateway pod label value (max 63), and names that
// share a long common prefix must not collide after shortening.
func TestAccessChildNamesBoundedAndCollisionResistant(t *testing.T) {
	prefix := strings.Repeat("a", 240)
	first := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: prefix + "-first", Namespace: "team"}}
	second := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: prefix + "-second", Namespace: "team"}}

	if got, want := TunnelName(first), 63; len(got) > want {
		t.Errorf("TunnelName() length = %d, want <= %d (label value limit)", len(got), want)
	}
	if TunnelName(first) == TunnelName(second) {
		t.Error("TunnelName() collides for access names sharing a long prefix")
	}

	if got, want := TunnelNetworkPolicyName(first), validation.DNS1123SubdomainMaxLength; len(got) > want {
		t.Errorf("TunnelNetworkPolicyName() length = %d, want <= %d", len(got), want)
	}
	if TunnelNetworkPolicyName(first) == TunnelNetworkPolicyName(second) {
		t.Error("TunnelNetworkPolicyName() collides for access names sharing a long prefix")
	}
	if !strings.HasSuffix(TunnelNetworkPolicyName(first), "-tunnel") {
		t.Errorf("TunnelNetworkPolicyName() = %q, want -tunnel suffix", TunnelNetworkPolicyName(first))
	}

	if got, want := CredentialSecretName(first), validation.DNS1123SubdomainMaxLength; len(got) > want {
		t.Errorf("CredentialSecretName() length = %d, want <= %d", len(got), want)
	}
	if CredentialSecretName(first) == CredentialSecretName(second) {
		t.Error("CredentialSecretName() collides for access names sharing a long prefix")
	}
	if !strings.HasSuffix(CredentialSecretName(first), "-credentials") {
		t.Errorf("CredentialSecretName() = %q, want -credentials suffix", CredentialSecretName(first))
	}
}
