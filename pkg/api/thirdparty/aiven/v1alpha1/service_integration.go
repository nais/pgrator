package aiven_v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(
		&ServiceIntegration{},
		&ServiceIntegrationList{},
	)
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type ServiceIntegration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ServiceIntegrationSpec   `json:"spec,omitempty"`
	Status            ServiceIntegrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ServiceIntegrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceIntegration `json:"items"`
}

// ServiceIntegrationSpec defines the desired state of ServiceIntegration
type ServiceIntegrationSpec struct {
	// Identifies the project this resource belongs to
	Project string `json:"project"`

	// Aiven authentication secret reference
	AuthSecretRef *AuthSecretReference `json:"authSecretRef,omitempty"`

	// Type of the service integration
	IntegrationType string `json:"integrationType"`

	// Source endpoint for the integration (if any)
	SourceEndpointID string `json:"sourceEndpointID,omitempty"`

	// Source service for the integration (if any)
	SourceServiceName string `json:"sourceServiceName,omitempty"`

	// Source project for the integration (if any)
	SourceProjectName string `json:"sourceProjectName,omitempty"`

	// Destination endpoint for the integration (if any)
	DestinationEndpointID string `json:"destinationEndpointId,omitempty"`

	// Destination service for the integration (if any)
	DestinationServiceName string `json:"destinationServiceName,omitempty"`

	// Destination project for the integration (if any)
	DestinationProjectName string `json:"destinationProjectName,omitempty"`

	// Metrics configuration values
	MetricsUserConfig *MetricsUserConfig `json:"metrics,omitempty"`
}

// MetricsUserConfig for metrics integration
type MetricsUserConfig struct {
	// Name of the database where to store metrics
	Database *string `json:"database,omitempty"`
	// Number of days to keep metrics
	RetentionDays *int `json:"retention_days,omitempty"`
	// Name of a user that can write to the database
	RoUsername *string `json:"ro_username,omitempty"`
	// Username used for metrics
	Username *string `json:"username,omitempty"`
}

// ServiceIntegrationStatus defines the observed state of ServiceIntegration
type ServiceIntegrationStatus struct {
	// Conditions represent the latest available observations of an ServiceIntegration state
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Service integration ID
	ID string `json:"id,omitempty"`
}
