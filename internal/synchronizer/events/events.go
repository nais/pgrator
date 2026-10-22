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
	if e.recorder != nil {
		// TODO: Consider using related argument to link to relevant objects (sub resources)
		// TODO: We should have separate reason and action in the events to make it easier for us to parse them later
		e.recorder.Eventf(obj, nil, eventType, reason, reason, messageFmt, args...)
	}
}

func (e *eventRecorder) RecordErrorEvent(obj api.NaisObject, phase string, err error) {
	if e.recorder != nil {
		e.recorder.Eventf(obj, nil, core_v1.EventTypeWarning, fmt.Sprintf("%sFailed", phase), phase, "%s phase failed for %s/%s: %v", phase, obj.GetNamespace(), obj.GetName(), err.Error())
	}
}
