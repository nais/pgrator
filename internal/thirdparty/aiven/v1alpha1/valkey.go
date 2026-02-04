package aiven_v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(
		&Valkey{},
		&ValkeyList{},
	)
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type Valkey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ValkeySpec    `json:"spec,omitempty"`
	Status            ServiceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ValkeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Valkey `json:"items"`
}

// ValkeySpec defines the desired state of an Aiven Valkey service
type ValkeySpec struct {
	// Identifies the project this resource belongs to
	Project string `json:"project"`

	// Aiven authentication secret reference
	AuthSecretRef *AuthSecretReference `json:"authSecretRef,omitempty"`

	// Subscription plan
	Plan string `json:"plan"`

	// Cloud the service runs in
	CloudName string `json:"cloudName,omitempty"`

	// Identifier of the VPC the service should be in, if any
	ProjectVPCID string `json:"projectVpcId,omitempty"`

	// Day of week when maintenance operations should be performed
	MaintenanceWindowDow string `json:"maintenanceWindowDow,omitempty"`

	// Time of day when maintenance operations should be performed (UTC time in HH:mm:ss format)
	MaintenanceWindowTime string `json:"maintenanceWindowTime,omitempty"`

	// Prevent service from being deleted
	TerminationProtection *bool `json:"terminationProtection,omitempty"`

	// Tags are key-value pairs that allow you to categorize services
	Tags map[string]string `json:"tags,omitempty"`

	// When true, the secret containing connection information will not be created
	ConnInfoSecretTargetDisabled *bool `json:"connInfoSecretTargetDisabled,omitempty"`

	// Secret configuration
	ConnInfoSecretTarget *ConnInfoSecretTarget `json:"connInfoSecretTarget,omitempty"`

	// Valkey specific user configuration options
	UserConfig *ValkeyUserConfig `json:"userConfig,omitempty"`
}

// ValkeyUserConfig contains Valkey specific configuration
type ValkeyUserConfig struct {
	// Valkey maxmemory-policy
	ValkeyMaxmemoryPolicy *string `json:"valkey_maxmemory_policy,omitempty"`

	// Set notify-keyspace-events option
	ValkeyNotifyKeyspaceEvents *string `json:"valkey_notify_keyspace_events,omitempty"`

	// When persistence is 'rdb', Valkey does RDB dumps. When 'off', no dumps are done.
	ValkeyPersistence *string `json:"valkey_persistence,omitempty"`

	// Set Valkey IO thread count
	ValkeyIoThreads *int `json:"valkey_io_threads,omitempty"`

	// Set number of Valkey databases
	ValkeyNumberOfDatabases *int `json:"valkey_number_of_databases,omitempty"`

	// Valkey idle connection timeout in seconds
	ValkeyTimeout *int `json:"valkey_timeout,omitempty"`

	// LFU maxmemory-policy counter decay time in minutes
	ValkeyLfuDecayTime *int `json:"valkey_lfu_decay_time,omitempty"`

	// Counter logarithm factor for volatile-lfu and allkeys-lfu maxmemory-policies
	ValkeyLfuLogFactor *int `json:"valkey_lfu_log_factor,omitempty"`

	// Require SSL to access Valkey
	ValkeySsl *bool `json:"valkey_ssl,omitempty"`

	// Set output buffer limit for pub/sub clients in MB
	ValkeyPubsubClientOutputBufferLimit *int `json:"valkey_pubsub_client_output_buffer_limit,omitempty"`

	// Determines default pub/sub channels' ACL for new users
	ValkeyAclChannelsDefault *string `json:"valkey_acl_channels_default,omitempty"`

	// The hour of day (in UTC) when backup for the service is started
	BackupHour *int `json:"backup_hour,omitempty"`

	// The minute of an hour when backup for the service is started
	BackupMinute *int `json:"backup_minute,omitempty"`

	// Additional Cloud Regions for Backup Replication
	AdditionalBackupRegions []string `json:"additional_backup_regions,omitempty"`

	// Allow incoming connections from CIDR address block
	IpFilter []*IpFilter `json:"ip_filter,omitempty"`

	// Store logs for the service so that they are available in the HTTP API and console
	ServiceLog *bool `json:"service_log,omitempty"`

	// Use static public IP addresses
	StaticIps *bool `json:"static_ips,omitempty"`

	// Register AAAA DNS records for the service, and allow IPv6 packets
	EnableIpv6 *bool `json:"enable_ipv6,omitempty"`

	// Name of another project to fork a service from
	ProjectToForkFrom *string `json:"project_to_fork_from,omitempty"`

	// Name of another service to fork from
	ServiceToForkFrom *string `json:"service_to_fork_from,omitempty"`

	// Name of the basebackup to restore in forked service
	RecoveryBasebackupName *string `json:"recovery_basebackup_name,omitempty"`

	// When enabled, Valkey will create frequent local RDB snapshots
	FrequentSnapshots *bool `json:"frequent_snapshots,omitempty"`

	// Valkey active-expire-effort setting
	ValkeyActiveExpireEffort *int `json:"valkey_active_expire_effort,omitempty"`

	// Allow access to selected service ports from private networks
	PrivateAccess *PrivateAccess `json:"private_access,omitempty"`

	// Allow access to selected service components through Privatelink
	PrivatelinkAccess *PrivatelinkAccess `json:"privatelink_access,omitempty"`

	// Allow access to selected service ports from the public Internet
	PublicAccess *PublicAccess `json:"public_access,omitempty"`

	// Migrate data from existing server
	Migration *Migration `json:"migration,omitempty"`
}

// PrivateAccess configuration
type PrivateAccess struct {
	// Enable Valkey private access
	Valkey *bool `json:"valkey,omitempty"`
	// Enable Prometheus private access
	Prometheus *bool `json:"prometheus,omitempty"`
}

// PrivatelinkAccess configuration
type PrivatelinkAccess struct {
	// Enable Valkey privatelink access
	Valkey *bool `json:"valkey,omitempty"`
	// Enable Prometheus privatelink access
	Prometheus *bool `json:"prometheus,omitempty"`
}

// PublicAccess configuration
type PublicAccess struct {
	// Enable Valkey public access
	Valkey *bool `json:"valkey,omitempty"`
	// Enable Prometheus public access
	Prometheus *bool `json:"prometheus,omitempty"`
}
