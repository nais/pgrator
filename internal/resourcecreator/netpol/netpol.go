// Package netpol builds the NetworkPolicy that isolates a CNPG Postgres cluster,
// allowing intra-cluster traffic, the CloudNativePG operator, and metrics scraping.
package netpol

import (
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	networking_v1 "k8s.io/api/networking/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const nameLabel = "postgres.nais.io/name"

func objectMeta(postgres *v1.Postgres, name string) meta_v1.ObjectMeta {
	var annotations map[string]string
	if postgres.GetCorrelationId() != "" {
		annotations = map[string]string{
			api.DeploymentCorrelationIDAnnotation: postgres.GetCorrelationId(),
		}
	}
	return meta_v1.ObjectMeta{
		Name:        name,
		Namespace:   postgres.GetNamespace(),
		Labels:      map[string]string{nameLabel: postgres.GetName()},
		Annotations: annotations,
	}
}

// Create builds the NetworkPolicy for a CNPG cluster owned by the Postgres resource.
// apiServerIP (a CIDR, e.g. "172.16.4.2/32") is allowed for egress so the CNPG
// instance manager can reach the Kubernetes API server; pass "" to omit it.
func Create(scheme *runtime.Scheme, postgres *v1.Postgres, clusterName, apiServerIP string) (*networking_v1.NetworkPolicy, error) {
	clusterMatchLabels := map[string]string{"cnpg.io/cluster": clusterName}

	egress := []networking_v1.NetworkPolicyEgressRule{
		// Intra-cluster traffic (streaming replication between instances).
		{
			To: []networking_v1.NetworkPolicyPeer{
				{PodSelector: &meta_v1.LabelSelector{MatchLabels: clusterMatchLabels}},
			},
		},
		// DNS resolution (kube-dns in any namespace).
		{
			To: []networking_v1.NetworkPolicyPeer{
				{
					NamespaceSelector: &meta_v1.LabelSelector{},
					PodSelector: &meta_v1.LabelSelector{
						MatchLabels: map[string]string{"k8s-app": "kube-dns"},
					},
				},
			},
		},
	}
	// Kubernetes API server: the CNPG instance manager watches the Cluster resource.
	if apiServerIP != "" {
		egress = append(egress, networking_v1.NetworkPolicyEgressRule{
			To: []networking_v1.NetworkPolicyPeer{
				{IPBlock: &networking_v1.IPBlock{CIDR: apiServerIP}},
			},
		})
	}

	netpol := &networking_v1.NetworkPolicy{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: objectMeta(postgres, clusterName),
		Spec: networking_v1.NetworkPolicySpec{
			PodSelector: meta_v1.LabelSelector{MatchLabels: clusterMatchLabels},
			PolicyTypes: []networking_v1.PolicyType{
				networking_v1.PolicyTypeEgress,
				networking_v1.PolicyTypeIngress,
			},
			Egress: egress,
			Ingress: []networking_v1.NetworkPolicyIngressRule{
				{
					From: []networking_v1.NetworkPolicyPeer{
						{PodSelector: &meta_v1.LabelSelector{MatchLabels: clusterMatchLabels}},
						naisSystemPeer("cloudnative-pg"),
					},
				},
				{
					From: []networking_v1.NetworkPolicyPeer{
						naisSystemPeer("prometheus"),
						naisSystemPeer("alloy"),
					},
					Ports: []networking_v1.NetworkPolicyPort{
						{Port: ptr.To(intstr.FromString("metrics"))},
					},
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(postgres, netpol, scheme); err != nil {
		return nil, err
	}
	return netpol, nil
}

func naisSystemPeer(appName string) networking_v1.NetworkPolicyPeer {
	return networking_v1.NetworkPolicyPeer{
		NamespaceSelector: &meta_v1.LabelSelector{
			MatchLabels: map[string]string{"kubernetes.io/metadata.name": "nais-system"},
		},
		PodSelector: &meta_v1.LabelSelector{
			MatchLabels: map[string]string{"app.kubernetes.io/name": appName},
		},
	}
}
