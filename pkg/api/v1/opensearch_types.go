package v1

import (
	"fmt"
	"strings"

	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=SingleNode;HighAvailability
type OpenSearchTier string

const (
	OpenSearchTierSingleNode       OpenSearchTier = "SingleNode"
	OpenSearchTierHighAvailability OpenSearchTier = "HighAvailability"
)

// +kubebuilder:validation:Enum="2GB";"4GB";"8GB";"16GB";"32GB";"64GB"
type OpenSearchMemory string

const (
	OpenSearchMemory2GB  OpenSearchMemory = "2GB"
	OpenSearchMemory4GB  OpenSearchMemory = "4GB"
	OpenSearchMemory8GB  OpenSearchMemory = "8GB"
	OpenSearchMemory16GB OpenSearchMemory = "16GB"
	OpenSearchMemory32GB OpenSearchMemory = "32GB"
	OpenSearchMemory64GB OpenSearchMemory = "64GB"
)

// +kubebuilder:validation:Enum="1";"2";"2.19";"3.3"
type OpenSearchVersion string

const (
	OpenSearchVersionV1    OpenSearchVersion = "1"
	OpenSearchVersionV2    OpenSearchVersion = "2"
	OpenSearchVersionV2_19 OpenSearchVersion = "2.19"
	OpenSearchVersionV3_3  OpenSearchVersion = "3.3"
)

type upgradePath []OpenSearchVersion

func (u upgradePath) String() string {
	versions := make([]string, len(u))
	for i, v := range u {
		versions[i] = string(v)
	}
	return strings.Join(versions, ", ")
}

var upgradePaths = map[OpenSearchVersion]upgradePath{
	OpenSearchVersionV1:    {OpenSearchVersionV2, OpenSearchVersionV2_19},
	OpenSearchVersionV2:    {OpenSearchVersionV2_19},
	OpenSearchVersionV2_19: {OpenSearchVersionV3_3},
	OpenSearchVersionV3_3:  {},
}

// ValidateUpgradePath validates that upgrading from oldVersion to this version is allowed
func (v OpenSearchVersion) ValidateUpgradePath(oldVersion OpenSearchVersion) error {
	if v == oldVersion {
		return nil
	}

	path, ok := upgradePaths[oldVersion]
	if !ok {
		return fmt.Errorf("unknown OpenSearch version: %q", oldVersion)
	}

	if len(path) == 0 {
		return fmt.Errorf("cannot change OpenSearch version from %s to %s: no further upgrades available", oldVersion, v)
	}

	for _, allowed := range path {
		if allowed == v {
			return nil
		}
	}

	return fmt.Errorf("cannot change OpenSearch version from %s to %s: new version must be one of [%s]", oldVersion, v, path)
}

// ToAivenString returns the version string for Aiven API
func (v OpenSearchVersion) ToAivenString() (string, error) {
	switch v {
	case OpenSearchVersionV1:
		return "1", nil
	case OpenSearchVersionV2:
		return "2", nil
	case OpenSearchVersionV2_19:
		return "2.19", nil
	case OpenSearchVersionV3_3:
		return "3.3", nil
	default:
		return "", fmt.Errorf("unexpected OpenSearch version: %q", v)
	}
}

// openSearchStorageConfig defines storage constraints for an OpenSearch plan
type openSearchStorageConfig struct {
	Min        int
	Max        int
	Increments int
}

// openSearchPlanConfig defines the Aiven plan configuration for a tier/memory combination
type openSearchPlanConfig struct {
	AivenPlan string
	Storage   openSearchStorageConfig
}

var openSearchPlans = map[OpenSearchTier]map[OpenSearchMemory]openSearchPlanConfig{
	OpenSearchTierSingleNode: {
		OpenSearchMemory2GB:  {AivenPlan: "hobbyist", Storage: openSearchStorageConfig{Min: 16, Max: 16, Increments: 0}},
		OpenSearchMemory4GB:  {AivenPlan: "startup-4", Storage: openSearchStorageConfig{Min: 80, Max: 400, Increments: 10}},
		OpenSearchMemory8GB:  {AivenPlan: "startup-8", Storage: openSearchStorageConfig{Min: 175, Max: 875, Increments: 10}},
		OpenSearchMemory16GB: {AivenPlan: "startup-16", Storage: openSearchStorageConfig{Min: 350, Max: 1750, Increments: 10}},
		OpenSearchMemory32GB: {AivenPlan: "startup-32", Storage: openSearchStorageConfig{Min: 700, Max: 3500, Increments: 10}},
		OpenSearchMemory64GB: {AivenPlan: "startup-64", Storage: openSearchStorageConfig{Min: 1400, Max: 5120, Increments: 10}},
	},
	OpenSearchTierHighAvailability: {
		OpenSearchMemory4GB:  {AivenPlan: "business-4", Storage: openSearchStorageConfig{Min: 240, Max: 1200, Increments: 30}},
		OpenSearchMemory8GB:  {AivenPlan: "business-8", Storage: openSearchStorageConfig{Min: 525, Max: 2625, Increments: 30}},
		OpenSearchMemory16GB: {AivenPlan: "business-16", Storage: openSearchStorageConfig{Min: 1050, Max: 5250, Increments: 30}},
		OpenSearchMemory32GB: {AivenPlan: "business-32", Storage: openSearchStorageConfig{Min: 2100, Max: 10500, Increments: 30}},
		OpenSearchMemory64GB: {AivenPlan: "business-64", Storage: openSearchStorageConfig{Min: 4200, Max: 15360, Increments: 30}},
	},
}

// OpenSearchSpec defines the desired state of OpenSearch
type OpenSearchSpec struct {
	// Tier defines the tier of the OpenSearch instance
	// +kubebuilder:validation:Required
	Tier OpenSearchTier `json:"tier"`

	// Memory defines the available memory for the OpenSearch instance
	// +kubebuilder:validation:Required
	Memory OpenSearchMemory `json:"memory"`

	// Version defines the OpenSearch version
	// +kubebuilder:validation:Required
	Version OpenSearchVersion `json:"version"`

	// StorageGB defines the storage capacity in gigabytes
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=16
	StorageGB int `json:"storageGB"`

	// TODO: reconsider the naming of this setting
	// ShardIndexingPressure controls the shard indexing back pressure settings.
	// +optional
	ShardIndexingPressure *OpenSearchShardIndexingPressure `json:"shardIndexingPressure,omitempty"`

	// Indices controls index settings for the OpenSearch instance.
	// +optional
	Indices *OpenSearchIndices `json:"indices,omitempty"`

	// Http controls HTTP settings for the OpenSearch instance.
	// +optional
	Http *OpenSearchHttp `json:"http,omitempty"`
}

type OpenSearchShardIndexingPressure struct {
	// Enable or disable shard indexing backpressure. Default is false.
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// Run shard indexing backpressure in shadow mode or enforced mode.
	// In shadow mode (value set as false), shard indexing backpressure tracks all granular-level metrics, but it
	// doesn’t actually reject any indexing requests. In enforced mode (value set as true), shard indexing backpressure
	// rejects any requests to the cluster that might cause a dip in its performance.
	// +optional
	Enforced bool `json:"enforced,omitempty"`
}

type OpenSearchIndices struct {
	// TODO: reconsider exposing this setting; check with users that have this set
	// Maximum number of clauses Lucene BooleanQuery can have. The default value (1024) is relatively high,
	// and increasing it may cause performance issues. Investigate other approaches first before increasing this value.
	// +kubebuilder:validation:Minimum=64
	// +kubebuilder:validation:Maximum=4096
	// +kubebuilder:default=1024
	// +optional
	QueryBoolMaxClauseCount *int `json:"queryBoolMaxClauseCount,omitempty"`
}

type OpenSearchHttp struct {
	// TODO: reconsider exposing this setting; check with users that have this set
	// Maximum content length for HTTP requests to the OpenSearch HTTP API, in bytes. Default to 100mb (104857600 bytes).
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2147483647
	// +optional
	MaxContentLength *int `json:"maxContentLength,omitempty"`
}

// OpenSearchStatus defines the observed state of OpenSearch.
type OpenSearchStatus struct {
	api.BaseStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=os,categories={nais}
// +kubebuilder:printcolumn:name="Tier",type="string",JSONPath=".spec.tier"
// +kubebuilder:printcolumn:name="Memory",type="string",JSONPath=".spec.memory"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".spec.version"
// +kubebuilder:printcolumn:name="Storage (GB)",type="integer",JSONPath=".spec.storageGB"
// +kubebuilder:printcolumn:name="Last reconcile",type="string",JSONPath=".status.reconcileTime"
// OpenSearch is the Schema for the opensearches API
type OpenSearch struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of OpenSearch
	// +required
	Spec OpenSearchSpec `json:"spec"`

	// status defines the observed state of OpenSearch
	// +optional
	Status *OpenSearchStatus `json:"status,omitempty,omitzero"`
}

func (o *OpenSearch) GetCorrelationId() string {
	return o.Annotations[api.DeploymentCorrelationIDAnnotation]
}

func (o *OpenSearch) GetStatus() api.Status {
	if o.Status == nil {
		o.Status = &OpenSearchStatus{}
	}
	return o.Status
}

func (o *OpenSearch) AivenPlan() (string, error) {
	config, err := o.aivenPlanConfig()
	if err != nil {
		return "", err
	}
	return config.AivenPlan, nil
}

// aivenPlanConfig returns the plan configuration for this OpenSearch instance
func (o *OpenSearch) aivenPlanConfig() (*openSearchPlanConfig, error) {
	memories, ok := openSearchPlans[o.Spec.Tier]
	if !ok {
		return nil, fmt.Errorf("no plan found for tier %s", o.Spec.Tier)
	}
	config, ok := memories[o.Spec.Memory]
	if !ok {
		return nil, fmt.Errorf("no plan found for tier %s and memory %s", o.Spec.Tier, o.Spec.Memory)
	}
	return &config, nil
}

func (o *OpenSearch) CanBeDeleted() (string, bool) {
	allowDeletion, ok := o.GetAnnotations()[api.AllowDeletionAnnotation]
	reason := fmt.Sprintf("Deletion requires the annotation %q set to \"true\"", api.AllowDeletionAnnotation)
	return reason, ok && allowDeletion == "true"
}

// +kubebuilder:object:root=true

// OpenSearchList contains a list of OpenSearch
type OpenSearchList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpenSearch `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OpenSearch{}, &OpenSearchList{})
}
