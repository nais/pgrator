package v1

import (
	"github.com/nais/pgrator/pkg/api"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PostgresResources struct {
	// DiskSize is the disk size for the Postgres cluster.
	// +kubebuilder:default="10Gi"
	// +optional
	DiskSize resource.Quantity `json:"diskSize,omitempty"`

	// Cpu is the CPU resources for the Postgres cluster.
	// +kubebuilder:default="100m"
	// +optional
	Cpu resource.Quantity `json:"cpu,omitempty"`

	// Memory is the memory resources for the Postgres cluster.
	// +kubebuilder:default="512Mi"
	// +optional
	Memory resource.Quantity `json:"memory,omitempty"`
}

type PostgresExtension struct {
	// Name of the Postgres extension to enable.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=tablefunc;citext;pageinspect;hstore;pg_logicalinspect;xml2;file_fdw;pgrowlocks;pg_visibility;pgstattuple;dict_int;pg_freespacemap;tcn;bloom;btree_gin;unaccent;seg;tsm_system_rows;insert_username;dict_xsyn;tsm_system_time;postgres_fdw;autoinc;earthdistance;amcheck;fuzzystrmatch;plpgsql;refint;uuid-ossp;intarray;btree_gist;pg_buffercache;pg_stat_statements;intagg;ltree;cube;pg_walinspect;dblink;pg_prewarm;sslinfo;pgcrypto;moddatetime;pg_trgm;pg_surgery;isn;lo;vector;pgaudit;postgis;timescaledb
	Name string `json:"name"`
}

// PostgresSpec defines the desired state of Postgres
type PostgresSpec struct {
	// Resources configures compute and disk for the Postgres cluster.
	// +kubebuilder:default={}
	// +optional
	Resources PostgresResources `json:"resources,omitempty"`

	// MajorVersion of Postgres to use.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum="18"
	MajorVersion string `json:"majorVersion"`

	// HighAvailability adds a third instance and enables synchronous replication.
	//
	// A cluster always runs a primary and a standby, so failover is available either
	// way. What this adds is the guarantee that an acknowledged commit survives
	// losing the primary, at the cost of write latency.
	// +optional
	HighAvailability bool `json:"highAvailability,omitempty"`

	// Extensions to enable in the Postgres database.
	// +optional
	Extensions []PostgresExtension `json:"extensions,omitempty"`

	// ActiveBranch selects the local branch name within this Postgres for normal workloads.
	// When omitted, pgrator retains the current active branch.
	// +optional
	ActiveBranch string `json:"activeBranch,omitempty"`
}

// PostgresStatus defines the observed state of Postgres.
type PostgresStatus struct {
	api.BaseStatus `json:",inline"`

	// ActiveBranch is the local branch name currently selected by pgrator.
	// +optional
	ActiveBranch string `json:"activeBranch,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories={nais}
// +kubebuilder:printcolumn:name="Major Version",type="string",JSONPath=".spec.majorVersion"
// +kubebuilder:printcolumn:name="Disk Size",type="string",JSONPath=".spec.resources.diskSize"
// +kubebuilder:printcolumn:name="CPU",type="string",JSONPath=".spec.resources.cpu"
// +kubebuilder:printcolumn:name="Memory",type="string",JSONPath=".spec.resources.memory"
// +kubebuilder:printcolumn:name="Active branch",type="string",JSONPath=".status.activeBranch"
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
