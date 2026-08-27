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
func Create(scheme *runtime.Scheme, postgres *v1.Postgres, clusterName string) (*networking_v1.NetworkPolicy, error) {
	clusterMatchLabels := map[string]string{"cnpg.io/cluster": clusterName}

	netpol := &networking_v1.NetworkPolicy{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "NetworkPolicy",
			APIVersion: "networking.k8s.io/v1",
		},
		ObjectMeta: objectMeta(postgres, clusterName),
		Spec: networking_v1.NetworkPolicySpec{
			PodSelector: meta_v1.LabelSelector{MatchLabels: clusterMatchLabels},
			Egress: []networking_v1.NetworkPolicyEgressRule{
				{
					To: []networking_v1.NetworkPolicyPeer{
						{PodSelector: &meta_v1.LabelSelector{MatchLabels: clusterMatchLabels}},
					},
				},
			},
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
