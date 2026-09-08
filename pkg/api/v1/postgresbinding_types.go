package v1

import (
	"crypto/sha256"
	"fmt"
	"unicode/utf8"

	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresBindingCredential is a connection credential a workload receives.
type PostgresBindingCredential string

// PostgresBindingRole is retained as a Go alias while consumers migrate to the
// credential collection. It is not a PostgresBinding spec field anymore.
type PostgresBindingRole = PostgresBindingCredential

// PostgresBindingWorkloadType identifies the kind of workload granted access.
type PostgresBindingWorkloadType string

const (
	// PostgresBindingCredentialRead grants membership in the <app>_read group role.
	PostgresBindingCredentialRead PostgresBindingCredential = "read"
	// PostgresBindingCredentialReadWrite grants membership in the <app>_readwrite group role.
	PostgresBindingCredentialReadWrite PostgresBindingCredential = "readwrite"
	// PostgresBindingCredentialAdmin connects as the durable database owner.
	PostgresBindingCredentialAdmin PostgresBindingCredential = "admin"

	// Deprecated aliases retained for Go callers during the in-place v1 transition.
	PostgresBindingRoleRead      = PostgresBindingCredentialRead
	PostgresBindingRoleReadWrite = PostgresBindingCredentialReadWrite
	PostgresBindingRoleAdmin     = PostgresBindingCredentialAdmin

	// PostgresBindingWorkloadTypeApplication identifies an Application workload.
	PostgresBindingWorkloadTypeApplication PostgresBindingWorkloadType = "application"
	// PostgresBindingWorkloadTypeJob identifies a Naisjob workload.
	PostgresBindingWorkloadTypeJob PostgresBindingWorkloadType = "job"
)

// PostgresBindingWorkload identifies the workload granted access.
type PostgresBindingWorkload struct {
	// Name is the name of the Application or Naisjob.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Type identifies whether the workload is an Application or Naisjob.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=application;job
	Type PostgresBindingWorkloadType `json:"type"`
}

// PostgresBindingConsumer identifies what is granted access to Postgres.
//
// +kubebuilder:validation:XValidation:rule="has(self.workload)",message="workload consumer is required"
type PostgresBindingConsumer struct {
	// Workload identifies the Application or Naisjob granted access. Its name is
	// the readable prefix of the login role shown in pg_stat_activity and the audit
	// log.
	Workload *PostgresBindingWorkload `json:"workload,omitempty"`
}

// PostgresBindingSpec defines the desired state of PostgresBinding.
type PostgresBindingSpec struct {
	// Postgres is the name of the Postgres instance to bind to. The instance must
	// live in the same namespace: bindings never cross team boundaries.
	// +kubebuilder:validation:Required
	Postgres string `json:"postgres"`

	// Consumer identifies what is granted access.
	// +kubebuilder:validation:Required
	Consumer PostgresBindingConsumer `json:"consumer"`

	// Credentials is the exact, mutable collection of connection credentials the
	// workload receives. Naiserator expands uses.postgres.role before creating the
	// binding, so pgrator never has to infer workload mounts from a shorthand.
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	// +kubebuilder:validation:items:Enum=admin;read;readwrite
	Credentials []PostgresBindingCredential `json:"credentials"`
}

// PostgresBindingStatus defines the observed state of PostgresBinding.
type PostgresBindingStatus struct {
	api.BaseStatus `json:",inline"`
}

const (
	// ownerRole is the durable database owner created at provisioning time.
	ownerRole = "app"
)

// RoleName returns the database login role for credential and the identity that
// appears in pg_stat_activity and the audit log. Admin reuses the durable owner;
// read and readwrite use workload-scoped login roles.
func (p *PostgresBinding) RoleName(credential PostgresBindingCredential) string {
	if credential == PostgresBindingCredentialAdmin {
		return ownerRole
	}

	suffix := string(credential)
	name := p.Spec.Consumer.Workload.Name + "-" + suffix
	if len(name) <= 63 {
		return name
	}

	hash := sha256.Sum256([]byte(name))
	hashText := fmt.Sprintf("%x", hash[:8])
	prefixBytes := 63 - len(suffix) - len(hashText) - 2
	prefix := p.Spec.Consumer.Workload.Name
	for len(prefix) > prefixBytes {
		_, size := utf8.DecodeLastRuneInString(prefix)
		prefix = prefix[:len(prefix)-size]
	}
	return fmt.Sprintf("%s-%s-%s", prefix, hashText, suffix)
}

// HasCredential reports whether credential is requested by this binding.
func (p *PostgresBinding) HasCredential(credential PostgresBindingCredential) bool {
	for _, requested := range p.Spec.Credentials {
		if requested == credential {
			return true
		}
	}
	return false
}

// ConnectionEnvPrefix is the role-specific portion of an environment variable
// name. Admin intentionally remains unprefixed for compatibility.
func ConnectionEnvPrefix(credential PostgresBindingCredential) string {
	switch credential {
	case PostgresBindingCredentialRead:
		return "READ_"
	case PostgresBindingCredentialReadWrite:
		return "READWRITE_"
	default:
		return ""
	}
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories={nais}
// +kubebuilder:printcolumn:name="Postgres",type="string",JSONPath=".spec.postgres"
// +kubebuilder:printcolumn:name="Workload",type="string",JSONPath=".spec.consumer.workload.name"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.consumer.workload.type"
// +kubebuilder:printcolumn:name="Credentials",type="string",JSONPath=".spec.credentials"
// +kubebuilder:printcolumn:name="Last reconcile",type="string",JSONPath=".status.reconcileTime"

// PostgresBinding grants a consumer access to a Postgres instance in the same
// namespace. It results in a database role authenticated by a client certificate,
// plus the Secrets the consumer needs to connect.
type PostgresBinding struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of PostgresBinding
	// +required
	// Postgres and consumer identity are immutable. Credentials may change as the
	// workload's uses.postgres declaration changes.
	// +kubebuilder:validation:XValidation:rule="self.postgres == oldSelf.postgres && self.consumer == oldSelf.consumer",message="postgres and consumer are immutable"
	Spec PostgresBindingSpec `json:"spec"`

	// status defines the observed state of PostgresBinding
	// +optional
	Status *PostgresBindingStatus `json:"status,omitempty"`
}

func (p *PostgresBinding) GetCorrelationId() string {
	return p.Annotations[api.DeploymentCorrelationIDAnnotation]
}

func (p *PostgresBinding) GetStatus() api.Status {
	if p.Status == nil {
		p.Status = &PostgresBindingStatus{}
	}
	return p.Status
}

// +kubebuilder:object:root=true

// PostgresBindingList contains a list of PostgresBinding
type PostgresBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresBinding{}, &PostgresBindingList{})
}
