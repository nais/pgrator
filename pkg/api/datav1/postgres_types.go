package datav1

import (
	"github.com/nais/pgrator/pkg/api"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PostgresResources struct {
	// Disk size for the Postgres cluster.
	// +kubebuilder:validation:required
	DiskSize resource.Quantity `json:"diskSize"`

	// CPU resources for the Postgres cluster.
	// +kubebuilder:validation:required
	Cpu resource.Quantity `json:"cpu"`

	// Memory resources for the Postgres cluster.
	// +kubebuilder:validation:required
	Memory resource.Quantity `json:"memory"`
}

// +kubebuilder:validation:Enum=read;write;function;role;ddl;misc;misc_set;all
type PostgresAuditStatementClass string

type PostgresAudit struct {
	// Enable audit logging for the Postgres cluster.
	Enabled bool `json:"enabled,omitempty"`

	// Statement classes to log.
	// +kubebuilder:default={"write","ddl","role"}
	StatementClasses []PostgresAuditStatementClass `json:"statementClasses,omitempty"`
}

type PostgresCluster struct {
	Resources PostgresResources `json:"resources"`

	// Major version of Postgres to use.
	// +kubebuilder:validation:required
	// +kubebuilder:validation:Enum="18";"17";"16"
	MajorVersion string `json:"majorVersion"`

	// High availability cluster.
	HighAvailability bool `json:"highAvailability,omitempty"`

	// Allow deletion of the Postgres cluster when the application is deleted.
	AllowDeletion bool `json:"allowDeletion,omitempty"`

	// Configure audit logging for the Postgres cluster.
	Audit *PostgresAudit `json:"audit,omitempty"`
}

type PostgresExtension struct {
	// Name of the Postgres extension to enable.
	// +kubebuilder:validation:required
	Name string `json:"name"`
}

type PostgresDatabase struct {
	// Collation for the Postgres database.
	// +kubebuilder:validation:Enum=nb_NO;en_US
	Collation string `json:"collation,omitempty"`

	// Extensions to enable in the Postgres database.
	Extensions []PostgresExtension `json:"extensions,omitempty"`
}

type Maintenance struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=7
	Day int `json:"day,omitempty"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=23
	Hour *int `json:"hour,omitempty"` // must use pointer here to be able to distinguish between no value and value 0 from user.
}

// PostgresSpec defines the desired state of Postgres
type PostgresSpec struct {
	// Cluster configures the Postgres cluster
	Cluster PostgresCluster `json:"cluster"`

	// Database configures the Postgres database.
	Database *PostgresDatabase `json:"database,omitempty"`

	// MaintenanceWindow configures the maintenance window for the Postgres cluster.
	MaintenanceWindow *Maintenance `json:"maintenanceWindow,omitempty"`
}

// PostgresStatus defines the observed state of Postgres.
type PostgresStatus struct {
	api.BaseStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories={nais}
// +kubebuilder:printcolumn:name="Major Version",type="string",JSONPath=".spec.cluster.majorVersion"
// +kubebuilder:printcolumn:name="Disk Size",type="string",JSONPath=".spec.cluster.resources.diskSize"
// +kubebuilder:printcolumn:name="CPU",type="string",JSONPath=".spec.cluster.resources.cpu"
// +kubebuilder:printcolumn:name="Memory",type="string",JSONPath=".spec.cluster.resources.memory"
// +kubebuilder:printcolumn:name="Last reconcile",type="string",JSONPath=".status.reconcileTime"

// Postgres is the Schema for the postgres API
type Postgres struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of Postgres
	// +required
	Spec PostgresSpec `json:"spec"`

	// status defines the observed state of Postgres
	// +optional
	Status *PostgresStatus `json:"status,omitempty"`
}

func (p *Postgres) GetCorrelationId() string {
	return p.Annotations[api.DeploymentCorrelationIDAnnotation]
}

func (p *Postgres) GetStatus() api.Status {
	if p.Status == nil {
		p.Status = &PostgresStatus{}
	}
	return p.Status
}

// +kubebuilder:object:root=true

// PostgresList contains a list of Postgres
type PostgresList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Postgres `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Postgres{}, &PostgresList{})
}

func (p *Postgres) ApplyDefaults() error {
	return nil
}
