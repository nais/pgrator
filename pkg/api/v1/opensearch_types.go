package v1

import (
	"fmt"
	"strings"

	"github.com/nais/pgrator/internal/synchronizer/object"
	"github.com/nais/pgrator/pkg/annotation"
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
type OpenSearchMajorVersion string

const (
	OpenSearchMajorVersionV1    OpenSearchMajorVersion = "1"
	OpenSearchMajorVersionV2    OpenSearchMajorVersion = "2"
	OpenSearchMajorVersionV2_19 OpenSearchMajorVersion = "2.19"
	OpenSearchMajorVersionV3_3  OpenSearchMajorVersion = "3.3"
)

type upgradePath []OpenSearchMajorVersion

func (u upgradePath) String() string {
	versions := make([]string, len(u))
	for i, v := range u {
		versions[i] = string(v)
	}
	return strings.Join(versions, ", ")
}

var upgradePaths = map[OpenSearchMajorVersion]upgradePath{
	OpenSearchMajorVersionV1:    {OpenSearchMajorVersionV2, OpenSearchMajorVersionV2_19},
	OpenSearchMajorVersionV2:    {OpenSearchMajorVersionV2_19},
	OpenSearchMajorVersionV2_19: {OpenSearchMajorVersionV3_3},
	OpenSearchMajorVersionV3_3:  {},
}

// ValidateUpgradePath validates that upgrading from oldVersion to this version is allowed
func (v OpenSearchMajorVersion) ValidateUpgradePath(oldVersion OpenSearchMajorVersion) error {
	if v == oldVersion {
		return nil
	}

	path, ok := upgradePaths[oldVersion]
	if !ok {
		return fmt.Errorf("unknown OpenSearch major version: %q", oldVersion)
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
func (v OpenSearchMajorVersion) ToAivenString() (string, error) {
	switch v {
	case OpenSearchMajorVersionV1:
		return "1", nil
	case OpenSearchMajorVersionV2:
		return "2", nil
	case OpenSearchMajorVersionV2_19:
		return "2.19", nil
	case OpenSearchMajorVersionV3_3:
		return "3.3", nil
	default:
		return "", fmt.Errorf("unexpected OpenSearch major version: %q", v)
	}
}

// MachineType represents the machine configuration for an OpenSearch instance
type MachineType struct {
	AivenPlan         string
	Tier              OpenSearchTier
	Memory            OpenSearchMemory
	StorageMin        int
	StorageMax        int
	StorageIncrements int
}

var openSearchMachineTypes = []MachineType{
	// SingleNode (hobbyist/startup) plans
	{AivenPlan: "hobbyist", Tier: OpenSearchTierSingleNode, Memory: OpenSearchMemory2GB, StorageMin: 16, StorageMax: 16, StorageIncrements: 10},
	{AivenPlan: "startup-4", Tier: OpenSearchTierSingleNode, Memory: OpenSearchMemory4GB, StorageMin: 80, StorageMax: 400, StorageIncrements: 10},
	{AivenPlan: "startup-8", Tier: OpenSearchTierSingleNode, Memory: OpenSearchMemory8GB, StorageMin: 175, StorageMax: 875, StorageIncrements: 10},
	{AivenPlan: "startup-16", Tier: OpenSearchTierSingleNode, Memory: OpenSearchMemory16GB, StorageMin: 350, StorageMax: 1750, StorageIncrements: 10},
	{AivenPlan: "startup-32", Tier: OpenSearchTierSingleNode, Memory: OpenSearchMemory32GB, StorageMin: 700, StorageMax: 3500, StorageIncrements: 10},
	{AivenPlan: "startup-64", Tier: OpenSearchTierSingleNode, Memory: OpenSearchMemory64GB, StorageMin: 1400, StorageMax: 5120, StorageIncrements: 10},
	// HighAvailability (business) plans
	{AivenPlan: "business-4", Tier: OpenSearchTierHighAvailability, Memory: OpenSearchMemory4GB, StorageMin: 240, StorageMax: 1200, StorageIncrements: 30},
	{AivenPlan: "business-8", Tier: OpenSearchTierHighAvailability, Memory: OpenSearchMemory8GB, StorageMin: 525, StorageMax: 2625, StorageIncrements: 30},
	{AivenPlan: "business-16", Tier: OpenSearchTierHighAvailability, Memory: OpenSearchMemory16GB, StorageMin: 1050, StorageMax: 5250, StorageIncrements: 30},
	{AivenPlan: "business-32", Tier: OpenSearchTierHighAvailability, Memory: OpenSearchMemory32GB, StorageMin: 2100, StorageMax: 10500, StorageIncrements: 30},
	{AivenPlan: "business-64", Tier: OpenSearchTierHighAvailability, Memory: OpenSearchMemory64GB, StorageMin: 4200, StorageMax: 15360, StorageIncrements: 30},
}

var aivenOpenSearchPlans map[OpenSearchTier]map[OpenSearchMemory]MachineType

func init() {
	aivenOpenSearchPlans = make(map[OpenSearchTier]map[OpenSearchMemory]MachineType)
	for _, m := range openSearchMachineTypes {
		if _, ok := aivenOpenSearchPlans[m.Tier]; !ok {
			aivenOpenSearchPlans[m.Tier] = make(map[OpenSearchMemory]MachineType)
		}
		if _, ok := aivenOpenSearchPlans[m.Tier][m.Memory]; ok {
			panic("duplicate tier and memory combination [" + string(m.Tier) + ", " + string(m.Memory) + "] in aivenOpenSearchPlans")
		}
		aivenOpenSearchPlans[m.Tier][m.Memory] = m
	}
}

// OpenSearchSpec defines the desired state of OpenSearch
type OpenSearchSpec struct {
	// Tier defines the tier of the OpenSearch instance
	// +kubebuilder:validation:Required
	Tier OpenSearchTier `json:"tier"`

	// Memory defines the available memory for the OpenSearch instance
	// +kubebuilder:validation:Required
	Memory OpenSearchMemory `json:"memory"`

	// MajorVersion defines the OpenSearch major version
	// +kubebuilder:validation:Required
	MajorVersion OpenSearchMajorVersion `json:"majorVersion"`

	// StorageGB defines the storage capacity in gigabytes
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=16
	StorageGB int `json:"storageGB"`
}

// OpenSearchStatus defines the observed state of OpenSearch.
type OpenSearchStatus struct {
	object.BaseStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=os,categories={nais}
// +kubebuilder:printcolumn:name="Tier",type="string",JSONPath=".spec.tier"
// +kubebuilder:printcolumn:name="Memory",type="string",JSONPath=".spec.memory"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".spec.majorVersion"
// +kubebuilder:printcolumn:name="Storage",type="integer",JSONPath=".spec.storageGB"
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
	return o.Annotations[annotation.DeploymentCorrelationIDAnnotation]
}

func (o *OpenSearch) GetStatus() object.Status {
	if o.Status == nil {
		o.Status = &OpenSearchStatus{}
	}
	return o.Status
}

func (o *OpenSearch) AivenPlan() (string, error) {
	memories, ok := aivenOpenSearchPlans[o.Spec.Tier]
	if !ok {
		return "", fmt.Errorf("no Aiven plans for tier %s", o.Spec.Tier)
	}

	plan, ok := memories[o.Spec.Memory]
	if !ok {
		return "", fmt.Errorf("no Aiven plan for memory %s in tier %s", o.Spec.Memory, o.Spec.Tier)
	}

	return plan.AivenPlan, nil
}

// GetMachineType returns the machine type configuration for this OpenSearch instance
func (o *OpenSearch) GetMachineType() (*MachineType, error) {
	for _, mt := range openSearchMachineTypes {
		if mt.Tier == o.Spec.Tier && mt.Memory == o.Spec.Memory {
			return &mt, nil
		}
	}
	return nil, fmt.Errorf("no machine type found for tier %s and memory %s", o.Spec.Tier, o.Spec.Memory)
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
