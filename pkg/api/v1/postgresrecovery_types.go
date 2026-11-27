package v1

import (
	"encoding/json"
	"fmt"

	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PostgresRecoveryAnnotation marks a Postgres resource that must be initialized
// by a companion PostgresRecovery with the same name rather than fresh initdb.
const PostgresRecoveryAnnotation = "postgres.nais.io/recovery"

// PostgresPointInTimeRecovery pins the live database WAL archive and point in
// time used once to initialize a new Postgres.
type PostgresPointInTimeRecovery struct {
	// SourcePostgres is the namespace-local source Postgres name.
	// +kubebuilder:validation:MinLength=1
	SourcePostgres string `json:"sourcePostgres"`

	// SourceUID pins the UID of the source Postgres so a deleted and recreated
	// database with the same name cannot become the recovery source.
	// +kubebuilder:validation:MinLength=1
	SourceUID string `json:"sourceUID"`

	// BucketName identifies the source Barman ObjectStore and bucket.
	// +kubebuilder:validation:MinLength=1
	BucketName string `json:"bucketName"`

	// ServerName identifies the source archive within the ObjectStore.
	// +kubebuilder:validation:MinLength=1
	ServerName string `json:"serverName"`

	// TargetTime is the explicit point in time to recover to, with whole-second
	// precision. Fractional seconds are rejected rather than silently truncated.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=date-time
	// +kubebuilder:validation:Pattern="^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}([.]0+)?(Z|[+-][0-9]{2}:[0-9]{2})$"
	TargetTime metav1.Time `json:"targetTime"`
}

func (r PostgresPointInTimeRecovery) MarshalJSON() ([]byte, error) {
	if r.TargetTime.Nanosecond() != 0 {
		return nil, fmt.Errorf("recovery targetTime must have whole-second precision")
	}
	type plain PostgresPointInTimeRecovery
	return json.Marshal(plain(r))
}

func (r *PostgresPointInTimeRecovery) UnmarshalJSON(data []byte) error {
	type plain PostgresPointInTimeRecovery
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.TargetTime.Nanosecond() != 0 {
		return fmt.Errorf("recovery targetTime must have whole-second precision")
	}
	*r = PostgresPointInTimeRecovery(decoded)
	return nil
}

// PostgresBackupRecovery pins the standalone PostgresBackup and isolated DR
// bucket prefix used once to initialize a new Postgres.
type PostgresBackupRecovery struct {
	// SourcePostgres records the original database name the backup was taken from.
	// +optional
	SourcePostgres string `json:"sourcePostgres,omitempty"`

	// BackupName is the namespace-local PostgresBackup resource name.
	// +kubebuilder:validation:MinLength=1
	BackupName string `json:"backupName"`

	// BackupUID pins the UID of the PostgresBackup resource.
	// +kubebuilder:validation:MinLength=1
	BackupUID string `json:"backupUID"`

	// BucketName is the team's disaster-recovery bucket containing the copied backup.
	// +kubebuilder:validation:MinLength=1
	BucketName string `json:"bucketName"`

	// Prefix is the isolated object prefix within BucketName for this backup.
	// +kubebuilder:validation:MinLength=1
	Prefix string `json:"prefix"`

	// ServerName is the Barman server archive name within Prefix.
	// +kubebuilder:validation:MinLength=1
	ServerName string `json:"serverName"`

	// BackupID is the Barman backup identifier to restore.
	// +kubebuilder:validation:MinLength=1
	BackupID string `json:"backupID"`
}

// PostgresRecoverySpec defines the immutable recovery source for a new Postgres.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="postgresrecovery spec is immutable"
// +kubebuilder:validation:XValidation:rule="(has(self.pointInTime) ? 1 : 0) + (has(self.backup) ? 1 : 0) == 1",message="exactly one of pointInTime or backup must be set"
type PostgresRecoverySpec struct {
	// Postgres is the namespace-local target Postgres resource name. It must
	// match metadata.name of this PostgresRecovery.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Postgres string `json:"postgres"`

	// PointInTime initializes the target Postgres from a live database's WAL archive.
	// +optional
	PointInTime *PostgresPointInTimeRecovery `json:"pointInTime,omitempty"`

	// Backup initializes the target Postgres from a completed standalone PostgresBackup.
	// +optional
	Backup *PostgresBackupRecovery `json:"backup,omitempty"`
}

// PostgresRecoveryStatus defines the observed state of PostgresRecovery.
type PostgresRecoveryStatus struct {
	api.BaseStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories={nais}
// +kubebuilder:printcolumn:name="Postgres",type="string",JSONPath=".spec.postgres"
// +kubebuilder:printcolumn:name="Source Postgres",type="string",JSONPath=".spec.pointInTime.sourcePostgres"
// +kubebuilder:printcolumn:name="Backup",type="string",JSONPath=".spec.backup.backupName"
// +kubebuilder:printcolumn:name="Target Time",type="string",JSONPath=".spec.pointInTime.targetTime"

// PostgresRecovery is an ephemeral bootstrap contract created alongside a new
// Postgres when restoring from point-in-time recovery or a standalone
// PostgresBackup. Pgrator deletes it automatically once the new database has
// completed its first own backup and detached from the recovery source.
type PostgresRecovery struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PostgresRecoverySpec    `json:"spec"`
	Status *PostgresRecoveryStatus `json:"status,omitempty"`
}

func (p *PostgresRecovery) GetCorrelationId() string {
	return p.Annotations[api.DeploymentCorrelationIDAnnotation]
}

func (p *PostgresRecovery) GetStatus() api.Status {
	if p.Status == nil {
		p.Status = &PostgresRecoveryStatus{}
	}
	return p.Status
}

// +kubebuilder:object:root=true

// PostgresRecoveryList contains a list of PostgresRecovery.
type PostgresRecoveryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PostgresRecovery `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PostgresRecovery{}, &PostgresRecoveryList{})
}
