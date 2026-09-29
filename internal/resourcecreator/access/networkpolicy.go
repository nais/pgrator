package access

import (
	"fmt"

	"github.com/nais/pgrator/internal/resourcecreator/cnpg"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func RelayNetworkPolicyName(access *v1.PostgresAccess) string {
	return boundedName(access.Name, "-relay", validation.DNS1123SubdomainMaxLength)
}

// CreateRelayNetworkPolicy permits only the relay workload to reach the
// selected instance's primary on PostgreSQL's TCP port.
func CreateRelayNetworkPolicy(scheme *runtime.Scheme, access *v1.PostgresAccess) (*networkingv1.NetworkPolicy, error) {
	policy := &networkingv1.NetworkPolicy{
		TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy"},
		ObjectMeta: metav1.ObjectMeta{Name: RelayNetworkPolicyName(access), Namespace: access.Namespace},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				"cnpg.io/cluster": cnpg.ClusterNameFor(access.Spec.PostgresBranch), "cnpg.io/instanceRole": "primary",
			}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "nais-system"}},
					PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "relay", "app.kubernetes.io/instance": "relay"}},
				}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: new(corev1.ProtocolTCP), Port: new(intstr.FromInt32(5432))}},
			}},
		},
	}
	if err := controllerutil.SetControllerReference(access, policy, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on relay NetworkPolicy: %w", err)
	}
	return policy, nil
}
