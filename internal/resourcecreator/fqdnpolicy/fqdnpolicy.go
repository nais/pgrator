// Package fqdnpolicy builds the FQDNNetworkPolicy needed by CNPG's WAL archiver.
package fqdnpolicy

import (
	fqdnv1alpha3 "github.com/nais/pgrator/internal/thirdparty/google/networking/v1alpha3"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const nameSuffix = "-wal"

// Create builds the policy that allows CNPG pods to reach Google services used
// by Workload Identity and Cloud Storage while archiving WAL files.
func Create(scheme *runtime.Scheme, postgres *v1.Postgres, clusterName string) (*fqdnv1alpha3.FQDNNetworkPolicy, error) {
	policy := &fqdnv1alpha3.FQDNNetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: fqdnv1alpha3.GroupVersion.String(),
			Kind:       "FQDNNetworkPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName + nameSuffix,
			Namespace: postgres.GetNamespace(),
			Labels: map[string]string{
				"postgres.nais.io/name": postgres.GetName(),
			},
		},
		Spec: fqdnv1alpha3.FQDNNetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				"cnpg.io/cluster": clusterName,
			}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []fqdnv1alpha3.FQDNNetworkPolicyEgressRule{
				egressRule("metadata.google.internal", 80),
				egressRule("private.googleapis.com", 443),
			},
		},
	}

	if err := controllerutil.SetControllerReference(postgres, policy, scheme); err != nil {
		return nil, err
	}
	return policy, nil
}

func egressRule(host string, port int) fqdnv1alpha3.FQDNNetworkPolicyEgressRule {
	return fqdnv1alpha3.FQDNNetworkPolicyEgressRule{
		Ports: []networkingv1.NetworkPolicyPort{{
			Protocol: new(corev1.ProtocolTCP),
			Port:     new(intstr.FromInt32(int32(port))),
		}},
		To: []fqdnv1alpha3.FQDNNetworkPolicyPeer{{FQDNs: []string{host}}},
	}
}
