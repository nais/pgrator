package events

import (
	"fmt"

	"github.com/nais/pgrator/internal/synchronizer/object"
	core_v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
)

type Recorder interface {
	RecordEvent(obj object.NaisObject, eventType string, reason string, messageFmt string, args ...any)
	RecordErrorEvent(obj object.NaisObject, phase string, err error)
}

func NewRecorder(recorder record.EventRecorder) Recorder {
	return &eventRecorder{
		recorder: recorder,
	}
}

type eventRecorder struct {
	recorder record.EventRecorder
}

func (e *eventRecorder) RecordEvent(obj object.NaisObject, eventType string, reason string, messageFmt string, args ...any) {
	if e.recorder != nil {
		msg := fmt.Sprintf(messageFmt, args...)
		e.recorder.Eventf(obj, eventType, reason, "[%s] %s", obj.GetCorrelationId(), msg)
	}
}

func (e *eventRecorder) RecordErrorEvent(obj object.NaisObject, phase string, err error) {
	if e.recorder != nil {
		e.recorder.Eventf(obj, core_v1.EventTypeWarning, fmt.Sprintf("%sFailed", phase), "[%s] %s phase failed for %s/%s: %v", obj.GetCorrelationId(), phase, obj.GetNamespace(), obj.GetName(), err.Error())
	}
}
