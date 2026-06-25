package storage_cnrm_cloud_google_com_v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(
		&StorageBucket{},
		&StorageBucketList{},
		&StorageBucketAccessControl{},
		&StorageBucketAccessControlList{},
	)
}

// StorageBucketStatus defines the config connector machine state of StorageBucket
type StorageBucketStatus struct {
	/* Conditions represent the latest available observations of the
	   object's current state. */
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	/* ObservedGeneration is the generation of the resource that was most recently observed by the Config Connector controller. If this is equal to metadata.generation, then that means that the current reported status reflects the most recent desired state of the resource. */
	ObservedGeneration *int64 `json:"observedGeneration,omitempty"`

	/* ObservedState is the state of the resource as most recently observed in GCP. */
	ObservedState *StorageBucketObservedState `json:"observedState,omitempty"`

	/* The URI of the created resource. */
	SelfLink *string `json:"selfLink,omitempty"`

	/* The base URL of the bucket, in the format gs://<bucket-name>. */
	Url *string `json:"url,omitempty"`
}

// StorageBucketObservedState is the state of the StorageBucket resource as most recently observed in GCP.
type StorageBucketObservedState struct {
	/* The bucket's soft delete policy, which defines the period of time that soft-deleted objects will be retained, and cannot be permanently deleted. If it is not provided, by default Google Cloud Storage sets this to default soft delete policy. */
	SoftDeletePolicy *StorageBucketSoftDeletePolicyObservedState `json:"softDeletePolicy,omitempty"`
}

type StorageBucketSoftDeletePolicyObservedState struct {
	/* Server-determined value that indicates the time from which the policy, or one with a greater retention, was effective. This value is in RFC 3339 format. */
	EffectiveTime *string `json:"effectiveTime,omitempty"`

	/* The duration in seconds that soft-deleted objects in the bucket will be retained and cannot be permanently deleted. Default value is 604800. */
	RetentionDurationSeconds *int64 `json:"retentionDurationSeconds,omitempty"`
}

// +kubebuilder:object:root=true
type StorageBucket struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              StorageBucketSpec   `json:"spec"`
	Status            StorageBucketStatus `json:"status,omitempty"`
}

type PublicAccessPrevention string

const (
	PublicAccessPreventionEnforced  PublicAccessPrevention = "enforced"
	PublicAccessPreventionInherited PublicAccessPrevention = "inherited"
)

type StorageBucketSpec struct {
	ResourceID               string                       `json:"resourceID,omitempty"`
	Location                 string                       `json:"location"`
	UniformBucketLevelAccess bool                         `json:"uniformBucketLevelAccess,omitempty"`
	RetentionPolicy          *RetentionPolicy             `json:"retentionPolicy,omitempty"`
	LifecycleRules           []StorageBucketLifecycleRule `json:"lifecycleRule,omitempty"`
	// +kubebuilder:validation:Enum=inherited;enforced
	PublicAccessPrevention PublicAccessPrevention `json:"publicAccessPrevention,omitempty"`
	SoftDeletePolicy       *SoftDeletePolicy      `json:"softDeletePolicy,omitempty"`
}

type SoftDeletePolicy struct {
	RetentionDurationSeconds uint `json:"retentionDurationSeconds"`
}

// +kubebuilder:object:root=true
type StorageBucketList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StorageBucket `json:"items"`
}

// +kubebuilder:object:root=true
type StorageBucketAccessControl struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              StorageBucketAccessControlSpec `json:"spec"`
}

type StorageBucketAccessControlSpec struct {
	BucketRef BucketRef `json:"bucketRef"`
	Entity    string    `json:"entity"`
	Role      string    `json:"role"`
}

type BucketRef struct {
	Name string `json:"name"`
}

// +kubebuilder:object:root=true
type StorageBucketAccessControlList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []StorageBucketAccessControl `json:"items"`
}

type RetentionPolicy struct {
	RetentionPeriod int `json:"retentionPeriod,omitempty"`
}

type StorageBucketLifecycleRule struct {
	/* The Lifecycle Rule's action configuration. A single block of this type is supported. */
	// +required
	Action *StorageBucketLifecycleRuleAction `json:"action"`

	/* The Lifecycle Rule's condition configuration. */
	// +required
	Condition *StorageBucketLifecycleRuleCondition `json:"condition"`
}

type StorageBucketLifecycleRuleActionStorageClass string
type StorageBucketLifecycleRuleActionType string

const (
	StorageBucketLifecycleRuleActionStorageClassMultiRegional StorageBucketLifecycleRuleActionStorageClass = "MULTI_REGIONAL"
	StorageBucketLifecycleRuleActionStorageClassRegional      StorageBucketLifecycleRuleActionStorageClass = "REGIONAL"
	StorageBucketLifecycleRuleActionStorageClassNearline      StorageBucketLifecycleRuleActionStorageClass = "NEARLINE"
	StorageBucketLifecycleRuleActionStorageClassColdline      StorageBucketLifecycleRuleActionStorageClass = "COLDLINE"
	StorageBucketLifecycleRuleActionStorageClassArchive       StorageBucketLifecycleRuleActionStorageClass = "ARCHIVE"

	StorageBucketLifecycleRuleActionTypeDelete                         StorageBucketLifecycleRuleActionType = "Delete"
	StorageBucketLifecycleRuleActionTypeSetStorageClass                StorageBucketLifecycleRuleActionType = "SetStorageClass"
	StorageBucketLifecycleRuleActionTypeAbortIncompleteMultipartUpload StorageBucketLifecycleRuleActionType = "AbortIncompleteMultipartUpload"
)

type StorageBucketLifecycleRuleAction struct {
	/* The target Storage Class of objects affected by this Lifecycle Rule. Supported values include: MULTI_REGIONAL, REGIONAL, NEARLINE, COLDLINE, ARCHIVE. */
	StorageClass *StorageBucketLifecycleRuleActionStorageClass `json:"storageClass,omitempty"`

	/* The type of the action of this Lifecycle Rule. Supported values include: Delete, SetStorageClass and AbortIncompleteMultipartUpload. */
	// +required
	Type StorageBucketLifecycleRuleActionType `json:"type"`
}

type StorageBucketLifecycleRuleCondition struct {
	/* Minimum age of an object in days to satisfy this condition. */
	Age *int `json:"age,omitempty"`

	/* Creation date of an object in RFC 3339 (e.g. 2017-06-13) to satisfy this condition. */
	CreatedBefore *string `json:"createdBefore,omitempty"`

	/* Creation date of an object in RFC 3339 (e.g. 2017-06-13) to satisfy this condition. */
	CustomTimeBefore *string `json:"customTimeBefore,omitempty"`

	/* Number of days elapsed since the user-specified timestamp set on an object. */
	DaysSinceCustomTime *int `json:"daysSinceCustomTime,omitempty"`

	/* Number of days elapsed since the noncurrent timestamp of an object. This
	condition is relevant only for versioned objects. */
	DaysSinceNoncurrentTime *int `json:"daysSinceNoncurrentTime,omitempty"`

	/* One or more matching name prefixes to satisfy this condition. */
	MatchesPrefix []string `json:"matchesPrefix,omitempty"`

	/* Storage Class of objects to satisfy this condition. Supported values include: MULTI_REGIONAL, REGIONAL, NEARLINE, COLDLINE, ARCHIVE, STANDARD, DURABLE_REDUCED_AVAILABILITY. */
	MatchesStorageClass []string `json:"matchesStorageClass,omitempty"`

	/* One or more matching name suffixes to satisfy this condition. */
	MatchesSuffix []string `json:"matchesSuffix,omitempty"`

	/* Creation date of an object in RFC 3339 (e.g. 2017-06-13) to satisfy this condition. */
	NoncurrentTimeBefore *string `json:"noncurrentTimeBefore,omitempty"`

	/* Relevant only for versioned objects. The number of newer versions of an object to satisfy this condition. */
	NumNewerVersions *int `json:"numNewerVersions,omitempty"`

	/* Match to live and/or archived objects. Unversioned buckets have only live objects. Supported values include: "LIVE", "ARCHIVED", "ANY". */
	WithState *string `json:"withState,omitempty"`
}
