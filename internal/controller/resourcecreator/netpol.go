package resourcecreator

import (
	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	network_v1 "k8s.io/api/networking/v1"
	v2 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func MinimalNetpol(postgres *data_nais_io_v1.Postgres, pgClusterName string, pgNamespace string) *network_v1.NetworkPolicy {
	objectMeta := CreateObjectMeta(postgres)
	objectMeta.Name = pgClusterName
	objectMeta.Namespace = pgNamespace

	if postgres.Spec.Cluster.AllowDeletion {
		objectMeta.Annotations[allowDeletionAnnotation] = pgClusterName
	}

	return &network_v1.NetworkPolicy{
		TypeMeta: v2.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: objectMeta,
	}
}

func CreatePostgresNetworkPolicySpec(postgres *data_nais_io_v1.Postgres, pgClusterName string, pgNamespace string) *network_v1.NetworkPolicy {
	netpol := MinimalNetpol(postgres, pgClusterName, pgNamespace)

	spec := network_v1.NetworkPolicySpec{
		PodSelector: v2.LabelSelector{
			MatchLabels: map[string]string{
				"application": "spilo",
				"app":         postgres.GetName(),
			},
		},
		Egress: []network_v1.NetworkPolicyEgressRule{
			{
				To: []network_v1.NetworkPolicyPeer{
					{
						PodSelector: &v2.LabelSelector{
							MatchLabels: map[string]string{
								"application": "spilo",
								"app":         postgres.GetName(),
							},
						},
					},
				},
			},
		},
		Ingress: []network_v1.NetworkPolicyIngressRule{
			{
				From: []network_v1.NetworkPolicyPeer{
					{
						PodSelector: &v2.LabelSelector{
							MatchLabels: map[string]string{
								"application": "spilo",
								"app":         postgres.GetName(),
							},
						},
					},
				},
			},
			{
				From: []network_v1.NetworkPolicyPeer{
					{
						PodSelector: &v2.LabelSelector{
							MatchLabels: map[string]string{
								"application": "db-connection-pooler",
								"app":         postgres.GetName(),
							},
						},
					},
				},
			},
			{
				From: []network_v1.NetworkPolicyPeer{
					{
						NamespaceSelector: &v2.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": "nais-system",
							},
						},
						PodSelector: &v2.LabelSelector{
							MatchLabels: map[string]string{
								"app.kubernetes.io/name": "postgres-operator",
							},
						},
					},
				},
			},
			{
				From: []network_v1.NetworkPolicyPeer{
					{
						NamespaceSelector: &v2.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": "nais-system",
							},
						},
						PodSelector: &v2.LabelSelector{
							MatchLabels: map[string]string{
								"app.kubernetes.io/name": "prometheus",
							},
						},
					},
				},
			},
			{
				From: []network_v1.NetworkPolicyPeer{
					{
						NamespaceSelector: &v2.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": postgres.GetNamespace(),
							},
						},
						PodSelector: &v2.LabelSelector{
							MatchLabels: map[string]string{
								"app": postgres.GetName(),
							},
						},
					},
				},
			},
		},
		PolicyTypes: []network_v1.PolicyType{
			network_v1.PolicyTypeEgress,
			network_v1.PolicyTypeIngress,
		},
	}
	netpol.Spec = spec
	return netpol
}
