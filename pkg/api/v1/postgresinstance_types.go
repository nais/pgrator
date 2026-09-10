package v1

import (
	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresInstanceBootstrap describes how a physical instance is initialized.
type PostgresInstanceBootstrap struct {
	// Recovery initializes the instance from another instance's archive.
	// +optional
	Recovery *PostgresInstanceRecovery `json:"recovery,omitempty"`
}

// PostgresInstanceRecovery identifies an immutable point-in-time recovery source.
type PostgresInstanceRecovery struct {
	// SourceInstance is the physical instance whose archive is recovered.
	// +kubebuilder:validation:MinLength=1
	SourceInstance string `json:"sourceInstance"`

	// TargetTime is the UTC point in time to recover to.
	TargetTime metav1.Time `json:"targetTime"`
}

// PostgresInstanceSpec defines the desired state of a physical Postgres instance.
type PostgresInstanceSpec struct {
	// Postgres is the logical Postgres this physical instance belongs to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Immutable
	Postgres string `json:"postgres"`

	// Bootstrap describes how this physical instance is initialized. It is immutable
	// because it is the instance's durable bootstrap provenance.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="bootstrap is immutable"
	Bootstrap *PostgresInstanceBootstrap `json:"bootstrap,omitempty"`
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
