package v1

import (
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/object"
	"github.com/nais/pgrator/pkg/annotation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=SingleNode;HighAvailability
type ValkeyTier string

const (
	ValkeyTierSingleNode       ValkeyTier = "SingleNode"
	ValkeyTierHighAvailability ValkeyTier = "HighAvailability"
)

// +kubebuilder:validation:Enum="1GB";"4GB";"8GB";"14GB";"28GB";"56GB";"112GB";"200GB"
type ValkeyMemory string

const (
	ValkeyMemory1GB   ValkeyMemory = "1GB"
	ValkeyMemory4GB   ValkeyMemory = "4GB"
	ValkeyMemory8GB   ValkeyMemory = "8GB"
	ValkeyMemory14GB  ValkeyMemory = "14GB"
	ValkeyMemory28GB  ValkeyMemory = "28GB"
	ValkeyMemory56GB  ValkeyMemory = "56GB"
	ValkeyMemory112GB ValkeyMemory = "112GB"
	ValkeyMemory200GB ValkeyMemory = "200GB"
)

// +kubebuilder:validation:Enum=allkeys-lfu;allkeys-lru;allkeys-random;noeviction;volatile-lfu;volatile-lru;volatile-random;volatile-ttl
type ValkeyMaxMemoryPolicy string

const (
	// ValkeyMaxMemoryPolicyAllkeysLFU keeps frequently used keys; removes least frequently used (LFU) keys
	ValkeyMaxMemoryPolicyAllkeysLFU ValkeyMaxMemoryPolicy = "allkeys-lfu"
	// ValkeyMaxMemoryPolicyAllkeysLRU keeps most recently used keys; removes least recently used (LRU) keys
	ValkeyMaxMemoryPolicyAllkeysLRU ValkeyMaxMemoryPolicy = "allkeys-lru"
	// ValkeyMaxMemoryPolicyAllkeysRandom randomly removes keys to make space for the new data added
	ValkeyMaxMemoryPolicyAllkeysRandom ValkeyMaxMemoryPolicy = "allkeys-random"
	// ValkeyMaxMemoryPolicyNoEviction means new values aren't saved when memory limit is reached. When a database uses replication, this applies to the primary database
	ValkeyMaxMemoryPolicyNoEviction ValkeyMaxMemoryPolicy = "noeviction"
	// ValkeyMaxMemoryPolicyVolatileLFU removes least frequently used keys with a TTL set
	ValkeyMaxMemoryPolicyVolatileLFU ValkeyMaxMemoryPolicy = "volatile-lfu"
	// ValkeyMaxMemoryPolicyVolatileLRU removes least recently used keys with a time-to-live (TTL) set
	ValkeyMaxMemoryPolicyVolatileLRU ValkeyMaxMemoryPolicy = "volatile-lru"
	// ValkeyMaxMemoryPolicyVolatileRandom randomly removes keys with a TTL set
	ValkeyMaxMemoryPolicyVolatileRandom ValkeyMaxMemoryPolicy = "volatile-random"
	// ValkeyMaxMemoryPolicyVolatileTTL removes keys with a TTL set, the keys with the shortest remaining time-to-live value first
	ValkeyMaxMemoryPolicyVolatileTTL ValkeyMaxMemoryPolicy = "volatile-ttl"
)

var aivenPlans = map[ValkeyTier]map[ValkeyMemory]string{
	ValkeyTierSingleNode: {
		ValkeyMemory1GB:   "hobbyist",
		ValkeyMemory4GB:   "startup-4",
		ValkeyMemory8GB:   "startup-8",
		ValkeyMemory14GB:  "startup-14",
		ValkeyMemory28GB:  "startup-28",
		ValkeyMemory56GB:  "startup-56",
		ValkeyMemory112GB: "startup-112",
		ValkeyMemory200GB: "startup-200",
	},
	ValkeyTierHighAvailability: {
		ValkeyMemory1GB:   "business-1",
		ValkeyMemory4GB:   "business-4",
		ValkeyMemory8GB:   "business-8",
		ValkeyMemory14GB:  "business-14",
		ValkeyMemory28GB:  "business-28",
		ValkeyMemory56GB:  "business-56",
		ValkeyMemory112GB: "business-112",
		ValkeyMemory200GB: "business-200",
	},
}

// ValkeySpec defines the desired state of Valkey
type ValkeySpec struct {
	// Tier defines the tier of the Valkey instance
	// +kubebuilder:validation:Required
	Tier ValkeyTier `json:"tier"`

	// Memory defines the available memory for the Valkey instance
	// +kubebuilder:validation:Required
	Memory ValkeyMemory `json:"memory"`

	// MaxMemoryPolicy defines the maximum memory policy for the Valkey instance
	// +optional
	MaxMemoryPolicy ValkeyMaxMemoryPolicy `json:"maxMemoryPolicy,omitempty"`

	// Configure keyspace notifications for the Valkey instance. See https://valkey.io/topics/notifications/ for details.
	// +optional
	// +kubebuilder:validation:Pattern=`^[KEg$lshztdxemnA]*$`
	NotifyKeyspaceEvents string `json:"notifyKeyspaceEvents,omitempty"`
}

// ValkeyStatus defines the observed state of Valkey.
type ValkeyStatus struct {
	object.BaseStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Last reconcile",type="string",JSONPath=".status.reconcileTime"
// +kubebuilder:printcolumn:name="Last rollout",type="string",JSONPath=".status.rolloutCompleteTime"

// Valkey is the Schema for the valkeys API
type Valkey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of Valkey
	// +required
	Spec ValkeySpec `json:"spec"`

	// status defines the observed state of Valkey
	// +optional
	Status *ValkeyStatus `json:"status,omitempty,omitzero"`
}

func (v *Valkey) GetCorrelationId() string {
	return v.Annotations[annotation.DeploymentCorrelationIDAnnotation]
}

func (v *Valkey) GetStatus() object.Status {
	if v.Status == nil {
		v.Status = &ValkeyStatus{}
	}
	return v.Status
}

func (v *Valkey) AivenPlan() (string, error) {
	memories, ok := aivenPlans[v.Spec.Tier]
	if !ok {
		return "", fmt.Errorf("no Aiven plans for tier %s", v.Spec.Tier)
	}

	plan, ok := memories[v.Spec.Memory]
	if !ok {
		return "", fmt.Errorf("no Aiven plan for memory %s in tier %s", v.Spec.Memory, v.Spec.Tier)
	}

	return plan, nil
}

// +kubebuilder:object:root=true

// ValkeyList contains a list of Valkey
type ValkeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Valkey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Valkey{}, &ValkeyList{})
}
