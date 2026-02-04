package events

import (
	"fmt"

	"github.com/nais/pgrator/pkg/api"
	core_v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/events"
)

type Recorder interface {
	RecordEvent(obj api.NaisObject, eventType string, reason string, messageFmt string, args ...any)
	RecordErrorEvent(obj api.NaisObject, phase string, err error)
}

func NewRecorder(recorder events.EventRecorder) Recorder {
	return &eventRecorder{
		recorder: recorder,
	}
}

type eventRecorder struct {
	recorder events.EventRecorder
}

func (e *eventRecorder) RecordEvent(obj api.NaisObject, eventType string, reason string, messageFmt string, args ...any) {
	correlationId := obj.GetCorrelationId()
	if correlationId == "" {
		correlationId = "no correlation id"
	}
	if e.recorder != nil {
		msg := fmt.Sprintf(messageFmt, args...)
		// TODO: Consider using related argument to link to relevant objects (sub resources)
		e.recorder.Eventf(obj, nil, eventType, reason, "[%s] %s", correlationId, msg)
	}
}

func (e *eventRecorder) RecordErrorEvent(obj api.NaisObject, phase string, err error) {
	if e.recorder != nil {
		correlationId := obj.GetCorrelationId()
		if correlationId == "" {
			correlationId = "no correlation id"
		}
		e.recorder.Eventf(obj, nil, core_v1.EventTypeWarning, fmt.Sprintf("%sFailed", phase), "[%s] %s phase failed for %s/%s: %v", correlationId, phase, obj.GetNamespace(), obj.GetName(), err.Error())
	}
}
