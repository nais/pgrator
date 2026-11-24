package v1

import (
	"github.com/nais/pgrator/pkg/api"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresBackupPhase describes the high-level lifecycle phase of a PostgresBackup.
type PostgresBackupPhase string

const (
	PostgresBackupPhasePreparing PostgresBackupPhase = "Preparing"
	PostgresBackupPhaseRunning   PostgresBackupPhase = "Running"
	PostgresBackupPhaseCompleted PostgresBackupPhase = "Completed"
	PostgresBackupPhaseFailed    PostgresBackupPhase = "Failed"

	// DefaultPostgresBackupRetentionDays applies when spec.retentionDays is omitted.
	DefaultPostgresBackupRetentionDays int32 = 30
)

// PostgresBackupSpec defines one standalone disaster-recovery backup of a Postgres database.
type PostgresBackupSpec struct {
	// Postgres is the namespace-local Postgres to back up. It follows the
	// DNS-subdomain naming rules of Postgres.metadata.name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="postgres is immutable"
	Postgres string `json:"postgres"`

	// RetentionDays is how many days the standalone backup is kept in the team's
	// disaster-recovery bucket before pgrator deletes it. Allowed range is 1 to 730
	// days (up to 2 years). Defaults to 30 when omitted.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=730
	// +optional
	RetentionDays *int32 `json:"retentionDays,omitempty"`
}

// EffectiveRetentionDays returns the requested retention in days, or the default.
func (s PostgresBackupSpec) EffectiveRetentionDays() int32 {
	if s.RetentionDays == nil {
		return DefaultPostgresBackupRetentionDays
	}
	return *s.RetentionDays
}

// PostgresBackupLocation identifies the isolated object-storage prefix holding
// a completed standalone backup.
type PostgresBackupLocation struct {
	// BucketName is the team's disaster-recovery bucket containing the copied backup.
	// +kubebuilder:validation:MinLength=1
	BucketName string `json:"bucketName"`

	// Prefix is the isolated object prefix within BucketName for this backup.
	// +kubebuilder:validation:MinLength=1
	Prefix string `json:"prefix"`
}

// PostgresBackupStatus defines the observed state of PostgresBackup.
type PostgresBackupStatus struct {
	api.BaseStatus `json:",inline"`

	// Phase is the high-level lifecycle phase: Preparing, Running, Completed or Failed.
	// +optional
	// +kubebuilder:validation:Enum=Preparing;Running;Completed;Failed
	Phase PostgresBackupPhase `json:"phase,omitempty"`

	// SourceUID pins the UID of the source Postgres when the backup was taken.
	// +optional
	SourceUID string `json:"sourceUID,omitempty"`

	// MajorVersion records the PostgreSQL major version of the source database so
	// restore validation works after the source Postgres is deleted.
	// +optional
	MajorVersion string `json:"majorVersion,omitempty"`

	// Extensions records the requested extensions on the source database when the
	// backup was taken so restore validation works after the source Postgres is deleted.
	// +optional
	Extensions []PostgresExtension `json:"extensions,omitempty"`

	// DiskSize records the configured disk size of the source database when the
	// backup was taken so restore validation works after the source Postgres is deleted.
	// +optional
	DiskSize *resource.Quantity `json:"diskSize,omitempty"`

	// BackupID is the Barman backup identifier produced by CloudNativePG.
	// +optional
	BackupID string `json:"backupID,omitempty"`

	// ServerName is the Barman server archive name within the backup prefix.
	// +optional
	ServerName string `json:"serverName,omitempty"`

	// StartedAt is when the backup execution started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// CompletedAt is when the backup and DR copy completed.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// BeginWal is the starting WAL segment required to restore this backup.
	// +optional
	BeginWal string `json:"beginWal,omitempty"`

	// EndWal is the ending WAL segment required to restore this backup to consistency.
	// +optional
	EndWal string `json:"endWal,omitempty"`

	// SizeBytes is the total verified size in bytes of the copied backup in the DR bucket.
	// +optional
	SizeBytes *int64 `json:"sizeBytes,omitempty"`

	// Location identifies the DR bucket and prefix where the standalone backup is stored.
	// +optional
	Location *PostgresBackupLocation `json:"location,omitempty"`

	// ExpiresAt is the time after which pgrator deletes this backup and its DR bucket files.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// ErrorMessage describes why the backup failed when Phase is Failed.
	// +optional
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={nais}
// +kubebuilder:printcolumn:name="Postgres",type="string",JSONPath=".spec.postgres"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Retention (days)",type="integer",JSONPath=".spec.retentionDays"
// +kubebuilder:printcolumn:name="Completed",type="date",JSONPath=".status.completedAt"
// +kubebuilder:printcolumn:name="Expires",type="string",JSONPath=".status.expiresAt"
// +kubebuilder:printcolumn:name="Last reconcile",type="string",JSONPath=".status.reconcileTime"

// PostgresBackup is a standalone disaster-recovery backup of a Postgres database.
// It has no owner reference to the source Postgres and survives source deletion.
type PostgresBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PostgresBackupSpec    `json:"spec"`
	Status *PostgresBackupStatus `json:"status,omitempty"`
}

func (p *PostgresBackup) GetCorrelationId() string {
	return p.Annotations[api.DeploymentCorrelationIDAnnotation]
}

func (p *PostgresBackup) GetStatus() api.Status {
	if p.Status == nil {
		p.Status = &PostgresBackupStatus{}
	}
	return p.Status
}

// +kubebuilder:object:root=true

// PostgresBackupList contains a list of PostgresBackup.
type PostgresBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresBackup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresBackup{}, &PostgresBackupList{})
}
