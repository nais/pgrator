package v1

import (
	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
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

	// Configuration contains PostgreSQL settings observed on the branch's backing
	// resource. It is absent until that resource is available. Configured values
	// are not necessarily active in the running PostgreSQL instances yet.
	// +optional
	Configuration *PostgresBranchConfiguration `json:"configuration,omitempty"`
}

// PostgresBranchConfiguration describes explicitly configured PostgreSQL settings.
type PostgresBranchConfiguration = PostgresConfiguration

// LegacyPostgresTypeMeta supports the deprecated rendering/test identity without
// embedding metav1.TypeMeta, which controller-gen treats as a CRD root regardless
// of object:root=false. It must not be used by new API resources.
type LegacyPostgresTypeMeta metav1.TypeMeta

// PostgresBranch is retained temporarily as a physical-rendering identity and
// for historical controller tests. It is not a published Kubernetes resource.
// +kubebuilder:object:root=false
type PostgresBranch struct {
	LegacyPostgresTypeMeta `json:",inline"`

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

func (p *PostgresBranch) GetObjectKind() schema.ObjectKind {
	return (*metav1.TypeMeta)(&p.LegacyPostgresTypeMeta)
}

func (p *PostgresBranch) GetStatus() api.Status {
	if p.Status == nil {
		p.Status = &PostgresBranchStatus{}
	}
	return p.Status
}

// +kubebuilder:object:root=false

// PostgresBranchList contains a list of PostgresBranch.
type PostgresBranchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresBranch `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresBranch{}, &PostgresBranchList{})
}

// DeepCopyObject supports historical fake-client fixtures without making this
// deprecated rendering type a controller-gen CRD root.
func (p *PostgresBranch) DeepCopyObject() runtime.Object { return p.DeepCopy() }

func (p *PostgresBranchList) DeepCopyObject() runtime.Object { return p.DeepCopy() }
