package v1

import (
	"strings"

	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresBindingRole is the level of access a workload is granted.
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

// PostgresBindingSpec defines the desired state of PostgresBinding.
type PostgresBindingSpec struct {
	// Postgres is the name of the Postgres instance to bind to. The instance must
	// live in the same namespace: bindings never cross team boundaries.
	// +kubebuilder:validation:Required
	Postgres string `json:"postgres"`

	// Workload identifies the Application or Naisjob granted access. Its name is
	// also the name of the database role, so it is what shows up in pg_stat_activity
	// and the audit log.
	// +kubebuilder:validation:Required
	Workload PostgresBindingWorkload `json:"workload"`

	// SecretName is the complete name of the client-certificate Secret naiserator
	// mounts into the workload. It must be unique per binding so one workload can
	// hold multiple roles for the same Postgres instance. CloudNativePG derives
	// this name by appending "-client-cert" to the DatabaseRole name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*-client-cert$`
	SecretName string `json:"secretName"`

	// Role is the level of access granted to the workload.
	// +kubebuilder:default="admin"
	// +kubebuilder:validation:Enum=read;readwrite;admin
	// +optional
	Role PostgresBindingRole `json:"role,omitempty"`
}

// PostgresBindingStatus defines the observed state of PostgresBinding.
type PostgresBindingStatus struct {
	api.BaseStatus `json:",inline"`
}

const (
	// ClientCertMountPath and CAMountPath are where a workload must mount the two
	// certificate Secrets. The paths are baked into the PGSSL* variables in the
	// config Secret, so a consumer that mounts them elsewhere will not connect.
	ClientCertMountPath = "/var/run/secrets/nais.io/postgres/client"
	CAMountPath         = "/var/run/secrets/nais.io/postgres/ca"

	// ownerRole is the durable database owner created at provisioning time.
	ownerRole = "app"
)

// RoleName is the database role the workload authenticates as, and therefore the
// identity that shows up in pg_stat_activity and the audit log.
//
// Admin bindings reuse the durable owner so that database objects keep a stable
// owner across deploys; every other role is named after the workload.
func (p *PostgresBinding) RoleName() string {
	if p.Spec.Role == PostgresBindingRoleAdmin {
		return ownerRole
	}
	return p.Spec.Workload.Name
}

// DatabaseRoleName is the CloudNativePG DatabaseRole name that produces SecretName.
func (p *PostgresBinding) DatabaseRoleName() string {
	return strings.TrimSuffix(p.Spec.SecretName, "-client-cert")
}

// ClientCertSecretName is the Secret a workload mounts at ClientCertMountPath.
//
// It is issued and renewed by CloudNativePG and mounted directly rather than
// copied, so that private key material is never duplicated and a renewed
// certificate reaches the workload without pgrator being in the path.
func (p *PostgresBinding) ClientCertSecretName() string {
	return p.Spec.SecretName
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories={nais}
// +kubebuilder:printcolumn:name="Postgres",type="string",JSONPath=".spec.postgres"
// +kubebuilder:printcolumn:name="Workload",type="string",JSONPath=".spec.workload.name"
// +kubebuilder:printcolumn:name="Type",type="string",JSONPath=".spec.workload.type"
// +kubebuilder:printcolumn:name="Role",type="string",JSONPath=".spec.role"
// +kubebuilder:printcolumn:name="Last reconcile",type="string",JSONPath=".status.reconcileTime"

// PostgresBinding grants a workload access to a Postgres instance in the same
// namespace. It results in a database role authenticated by a client certificate,
// plus the Secrets a workload needs to connect.
type PostgresBinding struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of PostgresBinding
	// +required
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
