package v1

import (
	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresBranchBootstrap describes how a physical branch is initialized.
type PostgresBranchBootstrap struct {
	// Recovery initializes the instance from another branch's archive.
	// +optional
	Recovery *PostgresBranchRecovery `json:"recovery,omitempty"`
}

// PostgresBranchRecovery identifies an immutable point-in-time recovery source.
type PostgresBranchRecovery struct {
	// SourceBranch is the local name of the branch whose archive is recovered.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	SourceBranch string `json:"sourceBranch"`

	// TargetTime is the UTC point in time to recover to.
	TargetTime metav1.Time `json:"targetTime"`
}

// PostgresBranchSpec defines the desired state of a physical Postgres branch.
type PostgresBranchSpec struct {
	// Postgres is the logical Postgres this physical branch belongs to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="postgres is immutable"
	Postgres string `json:"postgres"`

	// BranchName is the local name within Postgres. The object name must be
	// PostgresBranchObjectName(postgres, branchName).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="branchName is immutable"
	BranchName string `json:"branchName"`

	// Bootstrap describes how this physical branch is initialized. It is immutable
	// because it is the branch's durable bootstrap provenance.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="bootstrap is immutable"
	Bootstrap *PostgresBranchBootstrap `json:"bootstrap,omitempty"`
}

// PostgresBranchStatus defines the observed state of a PostgresBranch.
type PostgresBranchStatus struct {
	api.BaseStatus `json:",inline"`

	// ClusterName is the name of the observed CNPG Cluster owned by this branch.
	// It is empty until the cluster exists.
	// +optional
	ClusterName string `json:"clusterName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=postgresbranches,scope=Namespaced,categories={nais}
// +kubebuilder:printcolumn:name="Postgres",type="string",JSONPath=".spec.postgres"
// +kubebuilder:printcolumn:name="Last reconcile",type="string",JSONPath=".status.reconcileTime"

// PostgresBranch is a concrete, independently running instance of a logical Postgres.
type PostgresBranch struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of PostgresBranch
	// +required
	Spec PostgresBranchSpec `json:"spec"`

	// status defines the observed state of PostgresBranch
	// +optional
	Status *PostgresBranchStatus `json:"status,omitempty"`
}

func (p *PostgresBranch) GetCorrelationId() string {
	return p.Annotations[api.DeploymentCorrelationIDAnnotation]
}

func (p *PostgresBranch) GetStatus() api.Status {
	if p.Status == nil {
		p.Status = &PostgresBranchStatus{}
	}
	return p.Status
}

// +kubebuilder:object:root=true

// PostgresBranchList contains a list of PostgresBranch.
type PostgresBranchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresBranch `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresBranch{}, &PostgresBranchList{})
}
