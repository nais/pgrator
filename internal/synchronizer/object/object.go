package object

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NaisObject interface {
	client.Object
	GetStatus() Status
	GetCorrelationId() string
}

type Status interface {
	GetCorrelationID() string
	SetCorrelationID(string)
	GetObservedGeneration() int64
	SetObservedGeneration(int64)
	GetReconcilePhase() string
	SetReconcilePhase(string)
	GetReconcileTime() *metav1.Time
	SetReconcileTime(*metav1.Time)
	GetRolloutCompleteTime() *metav1.Time
	SetRolloutCompleteTime(*metav1.Time)
	GetRolloutStatus() string
	SetRolloutStatus(string)
	GetConditions() []metav1.Condition
	SetCondition(metav1.Condition)
}

// BaseStatus defines the observed state of a controlled resource.
// +kubebuilder:object:generate=true
type BaseStatus struct {
	CorrelationID       string       `json:"correlationID,omitempty"`
	ObservedGeneration  int64        `json:"observedGeneration,omitempty"`
	ReconcilePhase      string       `json:"reconcilePhase,omitempty"`
	ReconcileTime       *metav1.Time `json:"reconcileTime,omitempty"`
	RolloutCompleteTime *metav1.Time `json:"rolloutCompleteTime,omitempty"`
	RolloutStatus       string       `json:"rolloutStatus,omitempty"`

	// conditions represent the current state of the controlled resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (s *BaseStatus) GetCorrelationID() string {
	return s.CorrelationID
}

func (s *BaseStatus) SetCorrelationID(v string) {
	s.CorrelationID = v
}

func (s *BaseStatus) GetObservedGeneration() int64 {
	return s.ObservedGeneration
}

func (s *BaseStatus) SetObservedGeneration(v int64) {
	s.ObservedGeneration = v
}

func (s *BaseStatus) GetReconcilePhase() string {
	return s.ReconcilePhase
}

func (s *BaseStatus) SetReconcilePhase(v string) {
	s.ReconcilePhase = v
}

func (s *BaseStatus) GetReconcileTime() *metav1.Time {
	return s.ReconcileTime
}

func (s *BaseStatus) SetReconcileTime(v *metav1.Time) {
	s.ReconcileTime = v
}

func (s *BaseStatus) GetRolloutCompleteTime() *metav1.Time {
	return s.RolloutCompleteTime
}

func (s *BaseStatus) SetRolloutCompleteTime(v *metav1.Time) {
	s.RolloutCompleteTime = v
}

func (s *BaseStatus) GetRolloutStatus() string {
	return s.RolloutStatus
}

func (s *BaseStatus) SetRolloutStatus(v string) {
	s.RolloutStatus = v
}

func (s *BaseStatus) GetConditions() []metav1.Condition {
	return s.Conditions
}

func (s *BaseStatus) SetCondition(condition metav1.Condition) {
	meta.SetStatusCondition(&s.Conditions, condition)
}
