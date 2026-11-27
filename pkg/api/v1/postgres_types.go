package v1

import (
	"github.com/nais/pgrator/pkg/api"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PostgresResources struct {
	// DiskSize is the disk size for the Postgres cluster. Minimum is 5Gi.
	// +kubebuilder:default="10Gi"
	// +kubebuilder:validation:XValidation:rule="quantity(string(self)).compareTo(quantity('5Gi')) >= 0",message="diskSize must be at least 5Gi"
	// +optional
	DiskSize resource.Quantity `json:"diskSize,omitzero"`

	// Cpu is the CPU resources for the Postgres cluster.
	// +kubebuilder:default="100m"
	// +optional
	Cpu resource.Quantity `json:"cpu,omitzero"`

	// Memory is the memory resources for the Postgres cluster.
	// +kubebuilder:default="512Mi"
	// +optional
	Memory resource.Quantity `json:"memory,omitzero"`
}

// PostgresExtension is an extension requested for the application database.
type PostgresExtension struct {
	// Name of the extension, as used in CREATE EXTENSION. postgis and timescaledb are mounted
	// from the configured CNPG image catalog (timescaledb is also preloaded); the rest are bundled
	// with the Postgres image. pg_stat_statements and pgaudit are managed by the platform and
	// cannot be requested.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=postgis;timescaledb;amcheck;bloom;btree_gin;btree_gist;citext;cube;earthdistance;fuzzystrmatch;hstore;intarray;isn;lo;ltree;pageinspect;pg_buffercache;pg_freespacemap;pg_logicalinspect;pg_prewarm;pg_trgm;pg_visibility;pg_walinspect;pgcrypto;pgrowlocks;pgstattuple;seg;sslinfo;tablefunc;tcn;tsm_system_rows;tsm_system_time;unaccent;vector
	Name string `json:"name"`
}

// PostgresPITRPolicy configures the continuous point-in-time recovery (PITR)
// window of a Postgres: WAL archiving and nightly base backups. PITR is always
// on; without this section the recovery window is 2 days.
type PostgresPITRPolicy struct {
	// WindowDays is how many days back in time the database can be recovered to.
	// Older recovery data (base backups and WAL) is removed by barman, which checks
	// periodically. The base backup preceding the window is kept, so data up to a day
	// older may remain. Allowed range is 2 to 30 days.
	// Defaults to 2 when omitted.
	// +kubebuilder:validation:Minimum=2
	// +kubebuilder:validation:Maximum=30
	// +optional
	WindowDays *int32 `json:"windowDays,omitempty"`
}

// PostgresBackupPolicy is deprecated: use spec.pitr.windowDays for the
// point-in-time recovery window. Retained so existing manifests and API clients
// continue working while migrating to spec.pitr.windowDays.
type PostgresBackupPolicy struct {
	// RetentionDays is deprecated: use spec.pitr.windowDays instead.
	// +kubebuilder:validation:Minimum=2
	// +kubebuilder:validation:Maximum=30
	// +optional
	// +nais:doc:Deprecated=true
	// +nais:doc:Hidden=true
	RetentionDays *int32 `json:"retentionDays,omitempty"`
}

// PostgresRecoveryConfig is the legacy inline recovery field on Postgres.spec.
// Deprecated: new restores use a companion PostgresRecovery resource.
type PostgresRecoveryConfig = PostgresPointInTimeRecovery

// PostgresSpec defines the desired state of Postgres
// +kubebuilder:validation:XValidation:rule="!has(self.recovery) || has(oldSelf.recovery)",message="recovery can only be selected at creation"
type PostgresSpec struct {
	// Recovery is deprecated: new restores use a companion PostgresRecovery
	// resource so Postgres.spec stays identical for fresh and restored databases.
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="recovery is immutable"
	// +nais:doc:Deprecated=true
	// +nais:doc:Hidden=true
	Recovery *PostgresPointInTimeRecovery `json:"recovery,omitempty"`

	// Resources configures compute and disk for the Postgres cluster.
	// +kubebuilder:default={}
	// +optional
	Resources PostgresResources `json:"resources,omitempty"`

	// MajorVersion of Postgres to use.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum="18"
	MajorVersion string `json:"majorVersion"`

	// PITR configures the continuous point-in-time recovery window.
	// +optional
	PITR *PostgresPITRPolicy `json:"pitr,omitempty"`

	// Backup is deprecated for PITR configuration; use spec.pitr.windowDays.
	// +optional
	// +nais:doc:Deprecated=true
	// +nais:doc:Hidden=true
	Backup *PostgresBackupPolicy `json:"backup,omitempty"`

	// HighAvailability adds a third instance and enables synchronous replication.
	//
	// A cluster always runs a primary and a standby, so failover is available either
	// way. What this adds is the guarantee that an acknowledged commit survives
	// losing the primary, at the cost of write latency.
	// +optional
	HighAvailability bool `json:"highAvailability,omitempty"`

	// AutoExplain logs JSON execution plans for statements taking at least one second.
	// Includes actual row counts and buffer usage, with per-node timing and parameter
	// values disabled. Adds instrumentation overhead. Enabling or disabling it on
	// an existing cluster requires a rolling restart.
	// +optional
	AutoExplain bool `json:"autoExplain,omitempty"`

	// Extensions to activate in the application database. Removing an entry drops the extension
	// (without CASCADE); postgis and timescaledb stay mounted and preloaded until the drop
	// succeeds. The drop fails, and is retried, while other objects depend on the extension.
	// +listType=map
	// +listMapKey=name
	// +optional
	Extensions []PostgresExtension `json:"extensions,omitempty"`

	// ActiveBranch is deprecated: branches were retired. Only `main` (or empty)
	// is accepted; the field is kept so existing manifests stay valid.
	// +optional
	ActiveBranch string `json:"activeBranch,omitempty"`
}

// DefaultBackupRetentionDays applies when spec.pitr.windowDays (and the
// deprecated spec.backup.retentionDays) is omitted.
const DefaultBackupRetentionDays int32 = 2

// BackupRetentionDays returns the requested point-in-time recovery window in
// days, or the default. If both spec.pitr.windowDays and the deprecated
// spec.backup.retentionDays are set, spec.pitr.windowDays takes precedence.
func (s PostgresSpec) BackupRetentionDays() int32 {
	if s.PITR != nil && s.PITR.WindowDays != nil {
		return *s.PITR.WindowDays
	}
	if s.Backup != nil && s.Backup.RetentionDays != nil {
		return *s.Backup.RetentionDays
	}
	return DefaultBackupRetentionDays
}

// PostgresStatus defines the observed state of Postgres.
type PostgresStatus struct {
	api.BaseStatus `json:",inline"`

	// ClusterName identifies the directly managed CloudNativePG Cluster. It is
	// unset until pgrator has observed or created the cluster.
	// +optional
	ClusterName string `json:"clusterName,omitempty"`

	// Ownership checkpoints physical resource UIDs before changing ownership.
	// It prevents a retry from treating a lost database as a new database.
	// +optional
	Ownership *PostgresOwnership `json:"ownership,omitempty"`

	// Configuration contains settings observed on the directly managed cluster.
	// Configured values are not necessarily active in running instances yet.
	// +optional
	Configuration *PostgresConfiguration `json:"configuration,omitempty"`

	// ActiveBranch is deprecated: branches were retired. Pgrator always reports
	// `main`; the field is kept for existing readers.
	// +optional
	ActiveBranch string `json:"activeBranch,omitempty"`
}

// PostgresOwnership is the durable, per-database ownership transition journal.
type PostgresOwnership struct {
	// Phase is Provisioning, Adopting or Managed. This is not database readiness.
	// +kubebuilder:validation:Enum=Provisioning;Adopting;Managed
	Phase string `json:"phase"`
	// LegacyBranchUID records the PostgresBranch this database was adopted from,
	// before branches were retired.
	// +optional
	LegacyBranchUID string `json:"legacyBranchUID,omitempty"`
	// ClusterUID pins the original CNPG Cluster, independent of its controller.
	// +optional
	ClusterUID string `json:"clusterUID,omitempty"`
	// Resources records the directly owned resources observed during adoption.
	// +optional
	Resources []PostgresOwnedResource `json:"resources,omitempty"`
}

// PostgresOwnedResource identifies a resource without storing its functional spec.
type PostgresOwnedResource struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

// PostgresConfiguration describes explicitly configured PostgreSQL settings.
type PostgresConfiguration struct {
	// Parameters contains unmodified PostgreSQL parameter names and values,
	// including units and extension prefixes. Defaults are not included.
	Parameters map[string]string `json:"parameters"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:categories={nais}
// +kubebuilder:printcolumn:name="Major Version",type="string",JSONPath=".spec.majorVersion"
// +kubebuilder:printcolumn:name="Disk Size",type="string",JSONPath=".spec.resources.diskSize"
// +kubebuilder:printcolumn:name="CPU",type="string",JSONPath=".spec.resources.cpu"
// +kubebuilder:printcolumn:name="Memory",type="string",JSONPath=".spec.resources.memory"
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
