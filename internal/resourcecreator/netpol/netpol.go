package netpol

import (
	"github.com/nais/pgrator/internal/resourcecreator"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	networking_v1 "k8s.io/api/networking/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func Minimal(postgres *data_nais_io_v1.Postgres, pgClusterName string, pgNamespace string) *networking_v1.NetworkPolicy {
	objectMeta := resourcecreator.CreateObjectMeta(postgres)
	objectMeta.Name = pgClusterName
	objectMeta.Namespace = pgNamespace

	return &networking_v1.NetworkPolicy{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: objectMeta,
	}
}

func CreateCNPG(postgres *data_nais_io_v1.Postgres, pgClusterName string, pgNamespace string) *networking_v1.NetworkPolicy {
	netpol := Minimal(postgres, pgClusterName, pgNamespace)
	operatorName := "cloudnative-pg"
	clusterMatchLabels := map[string]string{
		"cnpg.io/cluster": pgClusterName,
	}

	spec := createNetworkPolicySpec(clusterMatchLabels, operatorName)
	netpol.Spec = spec
	return netpol
}

func CreateZalando(postgres *data_nais_io_v1.Postgres, pgClusterName string, pgNamespace string) *networking_v1.NetworkPolicy {
	netpol := Minimal(postgres, pgClusterName, pgNamespace)
	operatorName := "postgres-operator"
	clusterMatchLabels := map[string]string{
		"cluster-name": pgClusterName,
	}

	spec := createNetworkPolicySpec(clusterMatchLabels, operatorName)
	netpol.Spec = spec
	return netpol
}

func createNetworkPolicySpec(clusterMatchLabels map[string]string, operatorName string) networking_v1.NetworkPolicySpec {
	spec := networking_v1.NetworkPolicySpec{
		PodSelector: meta_v1.LabelSelector{
			MatchLabels: clusterMatchLabels,
		},
		Egress: []networking_v1.NetworkPolicyEgressRule{
			{
				To: []networking_v1.NetworkPolicyPeer{
					{
						PodSelector: &meta_v1.LabelSelector{
							MatchLabels: clusterMatchLabels,
						},
					},
				},
			},
		},
		Ingress: []networking_v1.NetworkPolicyIngressRule{
			{
				From: []networking_v1.NetworkPolicyPeer{
					{
						PodSelector: &meta_v1.LabelSelector{
							MatchLabels: clusterMatchLabels,
						},
					},
					{
						NamespaceSelector: &meta_v1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": "nais-system",
							},
						},
						PodSelector: &meta_v1.LabelSelector{
							MatchLabels: map[string]string{
								"app.kubernetes.io/name": operatorName,
							},
						},
					},
				},
			},
			{
				From: []networking_v1.NetworkPolicyPeer{
					{
						NamespaceSelector: &meta_v1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": "nais-system",
							},
						},
						PodSelector: &meta_v1.LabelSelector{
							MatchLabels: map[string]string{
								"app.kubernetes.io/name": "prometheus",
							},
						},
					},
					{
						NamespaceSelector: &meta_v1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": "nais-system",
							},
						},
						PodSelector: &meta_v1.LabelSelector{
							MatchLabels: map[string]string{
								"app.kubernetes.io/name": "alloy",
							},
						},
					},
				},
				Ports: []networking_v1.NetworkPolicyPort{
					{
						Port: new(intstr.FromString("metrics")),
					},
				},
			},
		},
		PolicyTypes: []networking_v1.PolicyType{
			networking_v1.PolicyTypeEgress,
			networking_v1.PolicyTypeIngress,
		},
	}
	return spec
}
