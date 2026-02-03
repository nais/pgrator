package aiven_v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(
		&OpenSearch{},
		&OpenSearchList{},
	)
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type OpenSearch struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              OpenSearchSpec `json:"spec,omitempty"`
	Status            ServiceStatus  `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type OpenSearchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpenSearch `json:"items"`
}

// OpenSearchSpec defines the desired state of an Aiven OpenSearch service
type OpenSearchSpec struct {
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

	// Disk space for data storage (in GiB)
	DiskSpace string `json:"disk_space,omitempty"`

	// OpenSearch specific user configuration options
	UserConfig *OpenSearchUserConfig `json:"userConfig,omitempty"`
}

// OpenSearchUserConfig contains OpenSearch specific configuration
type OpenSearchUserConfig struct {
	// OpenSearch major version
	OpenSearchVersion *string `json:"opensearch_version,omitempty"`

	// Maximum index count
	MaxIndexCount *int `json:"max_index_count,omitempty"`

	// Don't reset index.refresh_interval to the default value
	KeepIndexRefreshInterval *bool `json:"keep_index_refresh_interval,omitempty"`

	// Enable or disable OpenSearch Dashboards
	OpenSearchDashboards *OpenSearchDashboardsConfig `json:"opensearch_dashboards,omitempty"`

	// Index patterns configuration
	IndexPatterns []*IndexPattern `json:"index_patterns,omitempty"`

	// Index template configuration
	IndexTemplate *IndexTemplate `json:"index_template,omitempty"`

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

	// Allow access to selected service ports from private networks
	PrivateAccess *OpenSearchPrivateAccess `json:"private_access,omitempty"`

	// Allow access to selected service components through Privatelink
	PrivatelinkAccess *OpenSearchPrivatelinkAccess `json:"privatelink_access,omitempty"`

	// Allow access to selected service ports from the public Internet
	PublicAccess *OpenSearchPublicAccess `json:"public_access,omitempty"`

	// OpenSearch settings
	OpenSearch *OpenSearchSettings `json:"opensearch,omitempty"`
}

// OpenSearchDashboardsConfig contains OpenSearch Dashboards configuration
type OpenSearchDashboardsConfig struct {
	// Enable or disable OpenSearch Dashboards
	Enabled *bool `json:"enabled,omitempty"`

	// Max concurrent searches
	OpensearchRequestTimeout *int `json:"opensearch_request_timeout,omitempty"`
}

// IndexPattern configuration for automatic index creation
type IndexPattern struct {
	// Maximum number of indexes to keep
	MaxIndexCount *int `json:"max_index_count,omitempty"`

	// Pattern to match index names
	Pattern string `json:"pattern"`

	// Sorting pattern option
	SortingAlgorithm *string `json:"sorting_algorithm,omitempty"`
}

// IndexTemplate configuration
type IndexTemplate struct {
	// The maximum number of nested JSON objects across all fields
	MappingNestedObjectsLimit *int `json:"mapping_nested_objects_limit,omitempty"`

	// The number of replicas each primary shard has
	NumberOfReplicas *int `json:"number_of_replicas,omitempty"`

	// The number of primary shards that an index should have
	NumberOfShards *int `json:"number_of_shards,omitempty"`
}

// OpenSearchPrivateAccess configuration
type OpenSearchPrivateAccess struct {
	// Enable OpenSearch private access
	OpenSearch *bool `json:"opensearch,omitempty"`
	// Enable OpenSearch Dashboards private access
	OpenSearchDashboards *bool `json:"opensearch_dashboards,omitempty"`
	// Enable Prometheus private access
	Prometheus *bool `json:"prometheus,omitempty"`
}

// OpenSearchPrivatelinkAccess configuration
type OpenSearchPrivatelinkAccess struct {
	// Enable OpenSearch privatelink access
	OpenSearch *bool `json:"opensearch,omitempty"`
	// Enable OpenSearch Dashboards privatelink access
	OpenSearchDashboards *bool `json:"opensearch_dashboards,omitempty"`
	// Enable Prometheus privatelink access
	Prometheus *bool `json:"prometheus,omitempty"`
}

// OpenSearchPublicAccess configuration
type OpenSearchPublicAccess struct {
	// Enable OpenSearch public access
	OpenSearch *bool `json:"opensearch,omitempty"`
	// Enable OpenSearch Dashboards public access
	OpenSearchDashboards *bool `json:"opensearch_dashboards,omitempty"`
	// Enable Prometheus public access
	Prometheus *bool `json:"prometheus,omitempty"`
}

// OpenSearchSettings contains OpenSearch-specific settings
type OpenSearchSettings struct {
	// action.auto_create_index setting
	ActionAutoCreateIndexEnabled *bool `json:"action_auto_create_index_enabled,omitempty"`

	// action.destructive_requires_name setting
	ActionDestructiveRequiresName *bool `json:"action_destructive_requires_name,omitempty"`

	// Cluster max shards per node setting
	ClusterMaxShardsPerNode *int `json:"cluster_max_shards_per_node,omitempty"`

	// cluster.routing.allocation.node_concurrent_recoveries setting
	ClusterRoutingAllocationNodeConcurrentRecoveries *int `json:"cluster_routing_allocation_node_concurrent_recoveries,omitempty"`

	// http.max_content_length setting
	HttpMaxContentLength *int `json:"http_max_content_length,omitempty"`

	// http.max_header_size setting
	HttpMaxHeaderSize *int `json:"http_max_header_size,omitempty"`

	// http.max_initial_line_length setting
	HttpMaxInitialLineLength *int `json:"http_max_initial_line_length,omitempty"`

	// indices.fielddata.cache.size setting
	IndicesFielddataCacheSize *int `json:"indices_fielddata_cache_size,omitempty"`

	// indices.memory.index_buffer_size setting
	IndicesMemoryIndexBufferSize *int `json:"indices_memory_index_buffer_size,omitempty"`

	// indices.queries.cache.size setting
	IndicesQueriesCacheSize *int `json:"indices_queries_cache_size,omitempty"`

	// indices.query.bool.max_clause_count setting
	IndicesQueryBoolMaxClauseCount *int `json:"indices_query_bool_max_clause_count,omitempty"`

	// indices.recovery.max_bytes_per_sec setting
	IndicesRecoveryMaxBytesPerSec *int `json:"indices_recovery_max_bytes_per_sec,omitempty"`

	// indices.recovery.max_concurrent_file_chunks setting
	IndicesRecoveryMaxConcurrentFileChunks *int `json:"indices_recovery_max_concurrent_file_chunks,omitempty"`

	// search.max_buckets setting
	SearchMaxBuckets *int `json:"search_max_buckets,omitempty"`

	// thread_pool.analyze.queue_size setting
	ThreadPoolAnalyzeQueueSize *int `json:"thread_pool_analyze_queue_size,omitempty"`

	// thread_pool.analyze.size setting
	ThreadPoolAnalyzeSize *int `json:"thread_pool_analyze_size,omitempty"`

	// thread_pool.force_merge.size setting
	ThreadPoolForceMergeSize *int `json:"thread_pool_force_merge_size,omitempty"`

	// thread_pool.get.queue_size setting
	ThreadPoolGetQueueSize *int `json:"thread_pool_get_queue_size,omitempty"`

	// thread_pool.get.size setting
	ThreadPoolGetSize *int `json:"thread_pool_get_size,omitempty"`

	// thread_pool.search.queue_size setting
	ThreadPoolSearchQueueSize *int `json:"thread_pool_search_queue_size,omitempty"`

	// thread_pool.search.size setting
	ThreadPoolSearchSize *int `json:"thread_pool_search_size,omitempty"`

	// thread_pool.search.throttled.queue_size setting
	ThreadPoolSearchThrottledQueueSize *int `json:"thread_pool_search_throttled_queue_size,omitempty"`

	// thread_pool.search.throttled.size setting
	ThreadPoolSearchThrottledSize *int `json:"thread_pool_search_throttled_size,omitempty"`

	// thread_pool.write.queue_size setting
	ThreadPoolWriteQueueSize *int `json:"thread_pool_write_queue_size,omitempty"`

	// thread_pool.write.size setting
	ThreadPoolWriteSize *int `json:"thread_pool_write_size,omitempty"`
}
