package access

import (
	"fmt"

	"github.com/nais/pgrator/internal/resourcecreator/cnpg"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	tunnelv1alpha1 "github.com/nais/tunnel-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// tunnelPodLabel is the label tunnel-operator places on gateway pods.
	tunnelPodLabel = "tunnels.nais.io/tunnel"
	// primaryInstanceRole is the CNPG label value for the primary instance.
	primaryInstanceRole = "primary"
	// cnpgClusterLabel is the CNPG cluster label.
	cnpgClusterLabel = "cnpg.io/cluster"
	// cnpgInstanceRoleLabel is the CNPG instance role label.
	cnpgInstanceRoleLabel = "cnpg.io/instanceRole"
)

// TunnelName returns the deterministic Tunnel resource name for a PostgresAccess.
// PostgresAccess resource names are Kubernetes DNS subdomain labels, so they are
// safe to reuse here.
func TunnelName(access *v1.PostgresAccess) string {
	return access.Name
}

// TunnelNetworkPolicyName returns the deterministic NetworkPolicy name that
// permits the tunnel gateway to reach the CNPG primary for one access.
func TunnelNetworkPolicyName(access *v1.PostgresAccess) string {
	suffix := "-tunnel"
	maxNameLen := validation.DNS1123SubdomainMaxLength - len(suffix)
	name := access.Name
	if len(name) > maxNameLen {
		name = name[:maxNameLen]
	}
	return name + suffix
}

// CreateTunnel builds the Tunnel owned by a PostgresAccess.
func CreateTunnel(scheme *runtime.Scheme, access *v1.PostgresAccess) (*tunnelv1alpha1.Tunnel, error) {
	clusterName := cnpg.ClusterNameFor(access.Spec.PostgresInstance)
	tunnel := &tunnelv1alpha1.Tunnel{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Tunnel",
			APIVersion: tunnelv1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      TunnelName(access),
			Namespace: access.Namespace,
			Labels: map[string]string{
				"postgres.nais.io/access": access.Name,
			},
		},
		Spec: tunnelv1alpha1.TunnelSpec{
			TeamSlug: access.Namespace,
			Target: tunnelv1alpha1.TunnelTarget{
				Host: serviceFQDN(clusterName, access.Namespace),
				Port: 5432,
				PodSelector: &tunnelv1alpha1.TunnelTargetPodSelector{
					MatchLabels: map[string]string{
						cnpgClusterLabel:      clusterName,
						cnpgInstanceRoleLabel: primaryInstanceRole,
					},
				},
			},
			ClientPublicKey: access.Spec.ClientWireGuardPublicKey,
		},
	}
	if err := controllerutil.SetControllerReference(access, tunnel, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on Tunnel: %w", err)
	}
	return tunnel, nil
}

// CreateTunnelNetworkPolicy builds a per-access NetworkPolicy owned by
// PostgresAccess. It selects the CNPG primary and permits ingress on TCP/5432
// only from the tunnel-operator gateway pod carrying this access's Tunnel label.
func CreateTunnelNetworkPolicy(scheme *runtime.Scheme, access *v1.PostgresAccess) (*networkingv1.NetworkPolicy, error) {
	clusterName := cnpg.ClusterNameFor(access.Spec.PostgresInstance)
	netpol := &networkingv1.NetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      TunnelNetworkPolicyName(access),
			Namespace: access.Namespace,
			Labels: map[string]string{
				"postgres.nais.io/access": access.Name,
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					cnpgClusterLabel:      clusterName,
					cnpgInstanceRoleLabel: primaryInstanceRole,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{
				{
					From: []networkingv1.NetworkPolicyPeer{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									tunnelPodLabel: TunnelName(access),
								},
							},
						},
					},
					Ports: []networkingv1.NetworkPolicyPort{
						{
							Protocol: ptr(corev1.ProtocolTCP),
							Port:     ptr(intstr.FromInt32(5432)),
						},
					},
				},
			},
		},
	}
	if err := controllerutil.SetControllerReference(access, netpol, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on Tunnel NetworkPolicy: %w", err)
	}
	return netpol, nil
}

func serviceFQDN(clusterName, namespace string) string {
	return fmt.Sprintf("%s-rw.%s.svc.cluster.local", clusterName, namespace)
}

func ptr[T any](v T) *T {
	return &v
}
