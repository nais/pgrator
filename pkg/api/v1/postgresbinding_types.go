package v1

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresBindingRole is the level of access a consumer is granted.
type PostgresBindingRole string

// PostgresBindingWorkloadType identifies the kind of workload granted access.
type PostgresBindingWorkloadType string

const (
	// PostgresBindingRoleRead grants membership in the <app>_read group role.
	PostgresBindingRoleRead PostgresBindingRole = "read"
	// PostgresBindingRoleReadWrite grants membership in the <app>_readwrite group role.
	PostgresBindingRoleReadWrite PostgresBindingRole = "readwrite"
	// PostgresBindingRoleAdmin connects as the durable database owner. This is the
	// default, because the common case is a workload owning its own database and
	// running its own migrations.
	PostgresBindingRoleAdmin PostgresBindingRole = "admin"

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

	// SecretName is the complete name of the client-certificate Secret consumed by
	// the binding's consumer. It must be unique per binding so one consumer can hold
	// multiple roles for the same Postgres instance. CloudNativePG derives this name
	// by appending "-client-cert" to the DatabaseRole name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*-client-cert$`
	SecretName string `json:"secretName"`

	// Role is the level of access granted to the consumer.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=read;readwrite;admin
	Role PostgresBindingRole `json:"role"`
}

// PostgresBindingStatus defines the observed state of PostgresBinding.
type PostgresBindingStatus struct {
	api.BaseStatus `json:",inline"`
}

const (
	// ownerRole is the durable database owner created at provisioning time.
	ownerRole = "app"
)

// RoleName is the database role the consumer authenticates as and the identity
// that shows up in pg_stat_activity and the audit log.
//
// Admin bindings reuse the durable owner so that database objects keep a stable
// owner across deploys. Read and readwrite bindings use distinct login roles.
func (p *PostgresBinding) RoleName() string {
	if p.Spec.Role == PostgresBindingRoleAdmin {
		return ownerRole
	}

	suffix := string(p.Spec.Role)
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

// DatabaseRoleName is the CloudNativePG DatabaseRole name that produces SecretName.
func (p *PostgresBinding) DatabaseRoleName() string {
	return strings.TrimSuffix(p.Spec.SecretName, "-client-cert")
}

// ClientCertSecretName is the CNPG-managed Secret containing the client certificate.
//
// It is issued and renewed by CloudNativePG and mounted directly rather than
// copied, so that private key material is never duplicated and a renewed
// certificate reaches the consumer without pgrator being in the path.
func (p *PostgresBinding) ClientCertSecretName() string {
	return p.Spec.SecretName
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories={nais}
// +kubebuilder:printcolumn:name="Postgres",type="string",JSONPath=".spec.postgres"
// +kubebuilder:printcolumn:name="Workload",type="string",JSONPath=".spec.consumer.workload.name"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.consumer.workload.type"
// +kubebuilder:printcolumn:name="Role",type="string",JSONPath=".spec.role"
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
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable"
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
