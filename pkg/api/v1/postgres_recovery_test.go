package v1_test

import (
	"encoding/json"
	"testing"
	"time"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestPostgresRecoveryPrecisionAtWireBoundary(t *testing.T) {
	for _, nanos := range []int{0, 1, 750_000_000} {
		t.Run(time.Duration(nanos).String(), func(t *testing.T) {
			recovery := &v1.PostgresRecovery{TargetTime: metav1.NewTime(time.Date(2026, 11, 16, 13, 10, 0, nanos, time.UTC))}
			data, err := json.Marshal(recovery)
			if nanos != 0 {
				if err == nil {
					t.Errorf("fractional target silently serialized: %s", data)
				}
				if _, err := runtime.DefaultUnstructuredConverter.ToUnstructured(recovery); err == nil {
					t.Error("unstructured conversion silently accepted fractional target")
				}
				object := &v1.Postgres{Spec: v1.PostgresSpec{Recovery: recovery}}
				if _, err := json.Marshal(object); err == nil {
					t.Error("Postgres JSON silently accepted fractional recovery")
				}
				if _, err := runtime.DefaultUnstructuredConverter.ToUnstructured(object); err == nil {
					t.Error("Postgres conversion silently accepted fractional recovery")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var decoded v1.PostgresRecovery
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if !decoded.TargetTime.Equal(&recovery.TargetTime) {
				t.Fatalf("target changed in JSON round trip: %s", data)
			}
		})
	}
}

func TestPostgresRecoveryRejectsFractionalWireInput(t *testing.T) {
	var recovery v1.PostgresRecovery
	if err := json.Unmarshal([]byte(`{"targetTime":"2026-11-16T13:10:00.750Z"}`), &recovery); err == nil {
		t.Fatal("accepted unsupported fractional precision from JSON")
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(map[string]any{"targetTime": "2026-11-16T13:10:00.750Z"}, &recovery); err == nil {
		t.Fatal("accepted unsupported fractional precision from unstructured input")
	}
}
