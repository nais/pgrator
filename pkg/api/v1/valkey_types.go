package v1

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nais/pgrator/pkg/api"
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

// +kubebuilder:validation:Enum="8.1";"9.0";"9.1"
type ValkeyVersion string

const (
	ValkeyVersionV8_1 ValkeyVersion = "8.1"
	ValkeyVersionV9_0 ValkeyVersion = "9.0"
	ValkeyVersionV9_1 ValkeyVersion = "9.1"
)

type valkeyVersionInfo struct {
	version ValkeyVersion
	// Declared rather than derived from ordering, because an upgrade may require an intermediate
	// step and "newer" would then admit a jump Aiven refuses.
	upgradesTo []ValkeyVersion
	creatable  bool
	warning    string
	refusal    string // required when creatable is false
}

// Ascending; position is what newerThan compares.
var valkeyVersions = []valkeyVersionInfo{
	{version: ValkeyVersionV8_1, creatable: true, upgradesTo: []ValkeyVersion{ValkeyVersionV9_1}},
	{
		version:    ValkeyVersionV9_0,
		upgradesTo: []ValkeyVersion{ValkeyVersionV9_1},
		warning:    "Valkey 9.0 reached end-of-life at Aiven on 2026-08-31 and will be force-upgraded",
		refusal:    "Valkey 9.0 is no longer available for new instances",
	},
	{version: ValkeyVersionV9_1, creatable: true},
}

func (v ValkeyVersion) index() int {
	return slices.IndexFunc(valkeyVersions, func(info valkeyVersionInfo) bool { return info.version == v })
}

func (v ValkeyVersion) ValidateUpgradePath(oldVersion ValkeyVersion) error {
	if v == oldVersion {
		return nil
	}

	index := oldVersion.index()
	if index < 0 {
		return fmt.Errorf("unknown Valkey version: %q", oldVersion)
	}

	destinations := valkeyVersions[index].upgradesTo
	if len(destinations) == 0 {
		return fmt.Errorf("cannot change Valkey version from %s to %s: no further upgrades available", oldVersion, v)
	}
	if slices.Contains(destinations, v) {
		return nil
	}

	names := make([]string, len(destinations))
	for i, destination := range destinations {
		names[i] = string(destination)
	}
	return fmt.Errorf("cannot change Valkey version from %s to %s: new version must be one of [%s]", oldVersion, v, strings.Join(names, ", "))
}

func (v ValkeyVersion) ValidateNewInstance() error {
	if v == "" {
		return errors.New("spec.version is required")
	}

	index := v.index()
	if index < 0 {
		return fmt.Errorf("unknown Valkey version: %q", v)
	}
	if !valkeyVersions[index].creatable {
		return errors.New(valkeyVersions[index].refusal)
	}
	return nil
}

func (v ValkeyVersion) DeprecationWarning() (string, bool) {
	if index := v.index(); index >= 0 && valkeyVersions[index].warning != "" {
		return valkeyVersions[index].warning, true
	}
	return "", false
}

// Membership is not permission; filter through ValidateNewInstance or ValidateUpgradePath.
// Not ValkeyVersionFromAiven: it drops the patch component, so it accepts values admission rejects.
func KnownValkeyVersions() []ValkeyVersion {
	versions := make([]ValkeyVersion, 0, len(valkeyVersions))
	for _, info := range valkeyVersions {
		versions = append(versions, info.version)
	}
	return versions
}

// Aiven reports `major.minor` with an optional patch suffix; the enum is `major.minor`.
func ValkeyVersionFromAiven(reported string) (ValkeyVersion, bool) {
	major, rest, _ := strings.Cut(reported, ".")
	minor, _, _ := strings.Cut(rest, ".")

	version := ValkeyVersion(major + "." + minor)
	if version.index() < 0 {
		return "", false
	}
	return version, true
}

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

var valkeyAivenPlans = map[ValkeyTier]map[ValkeyMemory]string{
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

	// Version defines the Valkey version.
	// Required when creating an instance. Instances predating this field adopt whichever version
	// Aiven reports as running, and it cannot be unset again afterwards.
	// Aiven upgrading an instance on its own is adopted here rather than reverted.
	// +optional
	Version ValkeyVersion `json:"version,omitempty"`

	// MaxMemoryPolicy defines the maximum memory policy for the Valkey instance
	// +optional
	MaxMemoryPolicy ValkeyMaxMemoryPolicy `json:"maxMemoryPolicy,omitempty"`

	// Configure keyspace notifications for the Valkey instance. See https://valkey.io/topics/notifications/ for details.
	// +optional
	// +kubebuilder:validation:Pattern=`^[KEg$lshztdxemnA]*$`
	NotifyKeyspaceEvents string `json:"notifyKeyspaceEvents,omitempty"`

	// Persistence controls persistence and backup settings.
	// +optional
	Persistence *ValkeyPersistence `json:"persistence,omitempty"`

	// Databases defines the number of logical databases for the Valkey instance. The default is 16.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=128
	// +kubebuilder:default=16
	Databases *int `json:"databases,omitempty"`
}

type ValkeyPersistence struct {
	// Disabled indicates whether persistence (i.e. RDB dumps and backups) should be disabled for the Valkey instance.
	// If true, the Valkey instance will not perform RDB dumps and backups. All data will be lost if the instance is restarted for any reason.
	// Defaults to false.
	// +optional
	Disabled bool `json:"disabled,omitempty"`
}

// ValkeyStatus defines the observed state of Valkey.
type ValkeyStatus struct {
	api.BaseStatus `json:",inline"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=vk,categories={nais}
// +kubebuilder:printcolumn:name="Tier",type="string",JSONPath=".spec.tier"
// +kubebuilder:printcolumn:name="Memory",type="string",JSONPath=".spec.memory"
// +kubebuilder:printcolumn:name="Version",type="string",JSONPath=".spec.version"
// +kubebuilder:printcolumn:name="Last reconcile",type="string",JSONPath=".status.reconcileTime"
// Valkey is the Schema for the valkeys API
type Valkey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of Valkey
	// +required
	Spec ValkeySpec `json:"spec"`

	// status defines the observed state of Valkey
	// +optional
	Status *ValkeyStatus `json:"status,omitempty"`
}

func (v *Valkey) GetCorrelationId() string {
	return v.Annotations[api.DeploymentCorrelationIDAnnotation]
}

func (v *Valkey) GetStatus() api.Status {
	if v.Status == nil {
		v.Status = &ValkeyStatus{}
	}
	return v.Status
}

func (v *Valkey) AivenPlan() (string, error) {
	memories, ok := valkeyAivenPlans[v.Spec.Tier]
	if !ok {
		return "", fmt.Errorf("no Aiven plans for tier %s", v.Spec.Tier)
	}

	plan, ok := memories[v.Spec.Memory]
	if !ok {
		return "", fmt.Errorf("no Aiven plan for memory %s in tier %s", v.Spec.Memory, v.Spec.Tier)
	}

	return plan, nil
}

// Resolve returns the version to configure at Aiven, given what Aiven reports as running.
// Aiven upgrades services on its own schedule, so a running version newer than the receiver is
// adopted rather than overwritten; the caller stores the result as the new desired version.
func (v ValkeyVersion) Resolve(aivenReportedVersion string) (ValkeyVersion, error) {
	if aivenReportedVersion == "" {
		if v == "" {
			return "", fmt.Errorf("spec.version is unset and Aiven reports no running version; set spec.version explicitly")
		}
		// With nothing to order against, no other check runs, and an unknown version would reach
		// Aiven verbatim.
		if v.index() < 0 {
			return "", fmt.Errorf("unsupported Valkey version %q in spec.version", v)
		}
		return v, nil
	}

	// Aiven's version set is open-ended, so a version we cannot name is one we cannot order against
	// the spec. Configuring anything here risks instructing a downgrade.
	running, ok := ValkeyVersionFromAiven(aivenReportedVersion)
	if !ok {
		return "", fmt.Errorf("unsupported Valkey version %q reported by Aiven", aivenReportedVersion)
	}

	if v == "" {
		return running, nil
	}

	// A running version older than the spec is the ordinary pending-upgrade case: the spec is the
	// request, and sending it is how the upgrade gets made. The request still has to be a legal
	// step, which admission cannot check for a version being set for the first time — it has no
	// client with which to read what is running.
	if !running.newerThan(v) {
		if err := v.ValidateUpgradePath(running); err != nil {
			return "", fmt.Errorf("spec.version cannot be applied to an instance running %s: %w", running, err)
		}
		return v, nil
	}

	// Adopting a version the upgrade table forbids would produce a spec the webhook rejects, so
	// pgrator would configure Aiven and then fail to record what it had just done.
	if err := running.ValidateUpgradePath(v); err != nil {
		return "", fmt.Errorf("cannot adopt the running version %s: %w", running, err)
	}

	return running, nil
}

// An unknown version sorts below every known one, which routes it into ValidateUpgradePath and its
// explicit error rather than letting it win a comparison.
func (v ValkeyVersion) newerThan(other ValkeyVersion) bool {
	return v.index() > other.index()
}

func (v *Valkey) CanBeDeleted() (string, bool) {
	allowDeletion, ok := v.GetAnnotations()[api.AllowDeletionAnnotation]
	reason := fmt.Sprintf("Deletion requires the annotation %q set to \"true\"", api.AllowDeletionAnnotation)
	return reason, ok && allowDeletion == "true"
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
