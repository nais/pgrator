// Package v1alpha3 contains the API types used by Google's FQDNNetworkPolicy.
// +kubebuilder:object:generate=true
// +kubebuilder:skip
package v1alpha3

import (
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	GroupVersion  = schema.GroupVersion{Group: "networking.gke.io", Version: "v1alpha3"}
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}
	AddToScheme   = SchemeBuilder.AddToScheme
)

func init() {
	SchemeBuilder.Register(&FQDNNetworkPolicy{}, &FQDNNetworkPolicyList{})
}

// +kubebuilder:object:root=true
type FQDNNetworkPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              FQDNNetworkPolicySpec `json:"spec,omitempty"`
}

type FQDNNetworkPolicySpec struct {
	PodSelector metav1.LabelSelector          `json:"podSelector"`
	Egress      []FQDNNetworkPolicyEgressRule `json:"egress,omitempty"`
	PolicyTypes []networkingv1.PolicyType     `json:"policyTypes,omitempty"`
}

type FQDNNetworkPolicyEgressRule struct {
	Ports []networkingv1.NetworkPolicyPort `json:"ports,omitempty"`
	To    []FQDNNetworkPolicyPeer          `json:"to"`
}

type FQDNNetworkPolicyPeer struct {
	FQDNs []string `json:"fqdns"`
}

// +kubebuilder:object:root=true
type FQDNNetworkPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []FQDNNetworkPolicy `json:"items"`
}
