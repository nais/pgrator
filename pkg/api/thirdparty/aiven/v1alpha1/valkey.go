package aiven_v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(
		&Valkey{},
		&ValkeyList{},
		&ServiceIntegration{},
		&ServiceIntegrationList{},
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

// AuthSecretReference is a reference to a secret containing Aiven API credentials
type AuthSecretReference struct {
	// Name of the secret
	Name string `json:"name"`
	// Key within the secret
	Key string `json:"key"`
}

// ConnInfoSecretTarget specifies where to store connection information
type ConnInfoSecretTarget struct {
	// Name of the secret to create
	Name string `json:"name"`
	// Annotations to add to the secret
	Annotations map[string]string `json:"annotations,omitempty"`
	// Labels to add to the secret
	Labels map[string]string `json:"labels,omitempty"`
	// Prefix for the secret keys
	Prefix string `json:"prefix,omitempty"`
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

// IpFilter represents an IP filter entry
type IpFilter struct {
	// CIDR network address block
	Network string `json:"network"`
	// Description of the IP filter entry
	Description *string `json:"description,omitempty"`
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

// Migration configuration for data migration
type Migration struct {
	// Hostname or IP address of the source server
	Host string `json:"host"`
	// Port number of the source server
	Port int `json:"port"`
	// Password for authentication with the source server
	Password *string `json:"password,omitempty"`
	// Use SSL for the migration connection
	Ssl *bool `json:"ssl,omitempty"`
	// Username for authentication with the source server
	Username *string `json:"username,omitempty"`
	// Database name for bootstrapping the initial connection
	Dbname *string `json:"dbname,omitempty"`
	// Comma-separated list of databases to ignore during migration
	IgnoreDbs *string `json:"ignore_dbs,omitempty"`
	// The migration method
	Method *string `json:"method,omitempty"`
}

// ServiceStatus defines the observed state of an Aiven service
type ServiceStatus struct {
	// Conditions represent the latest available observations of a service state
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Service state (POWEROFF, REBUILDING, REBALANCING, RUNNING)
	State string `json:"state,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type ServiceIntegration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ServiceIntegrationSpec   `json:"spec,omitempty"`
	Status            ServiceIntegrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ServiceIntegrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceIntegration `json:"items"`
}

// ServiceIntegrationSpec defines the desired state of ServiceIntegration
type ServiceIntegrationSpec struct {
	// Identifies the project this resource belongs to
	Project string `json:"project"`

	// Aiven authentication secret reference
	AuthSecretRef *AuthSecretReference `json:"authSecretRef,omitempty"`

	// Type of the service integration
	IntegrationType string `json:"integrationType"`

	// Source endpoint for the integration (if any)
	SourceEndpointID string `json:"sourceEndpointID,omitempty"`

	// Source service for the integration (if any)
	SourceServiceName string `json:"sourceServiceName,omitempty"`

	// Source project for the integration (if any)
	SourceProjectName string `json:"sourceProjectName,omitempty"`

	// Destination endpoint for the integration (if any)
	DestinationEndpointID string `json:"destinationEndpointId,omitempty"`

	// Destination service for the integration (if any)
	DestinationServiceName string `json:"destinationServiceName,omitempty"`

	// Destination project for the integration (if any)
	DestinationProjectName string `json:"destinationProjectName,omitempty"`

	// Datadog specific user configuration options
	DatadogUserConfig *DatadogUserConfig `json:"datadog,omitempty"`

	// Metrics configuration values
	MetricsUserConfig *MetricsUserConfig `json:"metrics,omitempty"`

	// Logs configuration values
	LogsUserConfig *LogsUserConfig `json:"logs,omitempty"`
}

// DatadogUserConfig for Datadog integration
type DatadogUserConfig struct {
	// Custom tags provided by user
	DatadogTags []*DatadogTag `json:"datadog_tags,omitempty"`
	// Disable consumer group metrics
	ExcludeConsumerGroups *bool `json:"exclude_consumer_groups,omitempty"`
	// Disable topic metrics
	ExcludeTopics *bool `json:"exclude_topics,omitempty"`
	// Number of separate instances to fetch Kafka consumer statistics with
	KafkaConsumerCheckInstances *int `json:"kafka_consumer_check_instances,omitempty"`
	// Number of seconds to wait between Kafka consumer statistics fetch iterations
	KafkaConsumerStatsTimeout *int `json:"kafka_consumer_stats_timeout,omitempty"`
	// Maximum number of partition contexts to send
	MaxPartitionContexts *int `json:"max_partition_contexts,omitempty"`
}

// DatadogTag represents a Datadog tag
type DatadogTag struct {
	// Tag key
	Tag string `json:"tag"`
	// Tag value
	Comment *string `json:"comment,omitempty"`
}

// MetricsUserConfig for metrics integration
type MetricsUserConfig struct {
	// Name of the database where to store metrics
	Database *string `json:"database,omitempty"`
	// Number of days to keep metrics
	RetentionDays *int `json:"retention_days,omitempty"`
	// Name of a user that can write to the database
	RoUsername *string `json:"ro_username,omitempty"`
	// Username used for metrics
	Username *string `json:"username,omitempty"`
}

// LogsUserConfig for logs integration
type LogsUserConfig struct {
	// Elasticsearch index prefix
	ElasticsearchIndexPrefix *string `json:"elasticsearch_index_prefix,omitempty"`
	// Number of days to keep logs
	ElasticsearchIndexDaysMax *int `json:"elasticsearch_index_days_max,omitempty"`
}

// ServiceIntegrationStatus defines the observed state of ServiceIntegration
type ServiceIntegrationStatus struct {
	// Conditions represent the latest available observations of an ServiceIntegration state
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Service integration ID
	ID string `json:"id,omitempty"`
}
