package resourcecreator

import (
	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	networking_v1 "k8s.io/api/networking/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func MinimalNetpol(postgres *data_nais_io_v1.Postgres, pgClusterName string, pgNamespace string) *networking_v1.NetworkPolicy {
	objectMeta := CreateObjectMeta(postgres)
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

func CreatePostgresNetworkPolicySpec(postgres *data_nais_io_v1.Postgres, pgClusterName string, pgNamespace string) *networking_v1.NetworkPolicy {
	netpol := MinimalNetpol(postgres, pgClusterName, pgNamespace)

	spec := networking_v1.NetworkPolicySpec{
		PodSelector: meta_v1.LabelSelector{
			MatchLabels: map[string]string{
				"cluster-name": pgClusterName,
			},
		},
		Egress: []networking_v1.NetworkPolicyEgressRule{
			{
				To: []networking_v1.NetworkPolicyPeer{
					{
						PodSelector: &meta_v1.LabelSelector{
							MatchLabels: map[string]string{
								"cluster-name": pgClusterName,
							},
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
							MatchLabels: map[string]string{
								"cluster-name": pgClusterName,
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
								"app.kubernetes.io/name": "postgres-operator",
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
				},
			},
			{
				From: []networking_v1.NetworkPolicyPeer{
					{
						NamespaceSelector: &meta_v1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": postgres.GetNamespace(),
							},
						},
						PodSelector: &meta_v1.LabelSelector{
							MatchLabels: map[string]string{
								"cluster-name": pgClusterName,
							},
						},
					},
				},
			},
		},
		PolicyTypes: []networking_v1.PolicyType{
			networking_v1.PolicyTypeEgress,
			networking_v1.PolicyTypeIngress,
		},
	}
	netpol.Spec = spec
	return netpol
}
