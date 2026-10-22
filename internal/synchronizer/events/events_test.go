package events_test

import (
	"errors"
	"testing"

	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kevents "k8s.io/client-go/tools/events"
)

func TestEventMessages(t *testing.T) {
	for _, correlationID := range []string{"", "deployment-123"} {
		name := "without correlation ID"
		if correlationID != "" {
			name = "with correlation ID"
		}
		t.Run(name, func(t *testing.T) {
			obj := &v1.PostgresBranch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mydb-main",
					Namespace: "myteam",
					Annotations: map[string]string{
						api.DeploymentCorrelationIDAnnotation: correlationID,
					},
				},
			}
			recorder := kevents.NewFakeRecorder(2)
			r := events.NewRecorder(recorder)

			r.RecordEvent(obj, corev1.EventTypeNormal, "Updated", "Updated %s %s/%s", "DatabaseRole", "myteam", "personal-access")
			r.RecordErrorEvent(obj, "PerformActions", errors.New("update denied"))

			for _, want := range []string{
				"Normal Updated Updated DatabaseRole myteam/personal-access",
				"Warning PerformActionsFailed PerformActions phase failed for myteam/mydb-main: update denied",
			} {
				select {
				case got := <-recorder.Events:
					if got != want {
						t.Errorf("event = %q, want %q", got, want)
					}
				default:
					t.Errorf("missing event %q", want)
				}
			}
		})
	}
}
