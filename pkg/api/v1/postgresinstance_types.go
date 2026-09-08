package v1

import (
	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresInstanceSpec defines the desired state of a physical Postgres instance.
type PostgresInstanceSpec struct {
	// Postgres is the logical Postgres this physical instance belongs to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Immutable
	Postgres string `json:"postgres"`
}

// PostgresInstanceStatus defines the observed state of a PostgresInstance.
type PostgresInstanceStatus struct {
	api.BaseStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={nais}
// +kubebuilder:printcolumn:name="Postgres",type="string",JSONPath=".spec.postgres"
// +kubebuilder:printcolumn:name="Last reconcile",type="string",JSONPath=".status.reconcileTime"

// PostgresInstance is a concrete, independently running instance of a logical Postgres.
type PostgresInstance struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of PostgresInstance
	// +required
	Spec PostgresInstanceSpec `json:"spec"`

	// status defines the observed state of PostgresInstance
	// +optional
	Status *PostgresInstanceStatus `json:"status,omitempty"`
}

func (p *PostgresInstance) GetCorrelationId() string {
	return p.Annotations[api.DeploymentCorrelationIDAnnotation]
}

func (p *PostgresInstance) GetStatus() api.Status {
	if p.Status == nil {
		p.Status = &PostgresInstanceStatus{}
	}
	return p.Status
}

// +kubebuilder:object:root=true

// PostgresInstanceList contains a list of PostgresInstance.
type PostgresInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresInstance{}, &PostgresInstanceList{})
}
