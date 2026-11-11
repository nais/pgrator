package v1

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestDirectPostgresStatusRoundTripAndDeepCopy(t *testing.T) {
	pg := &Postgres{Status: &PostgresStatus{
		ClusterName:   "pg-existing-main-12345678",
		Configuration: &PostgresConfiguration{Parameters: map[string]string{"work_mem": "32MB"}},
	}}
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(pg)
	if err != nil {
		t.Fatal(err)
	}
	var stored Postgres
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.ClusterName != pg.Status.ClusterName || stored.Status.Configuration.Parameters["work_mem"] != "32MB" {
		t.Fatalf("status did not round-trip: %+v", stored.Status)
	}
	copy := stored.DeepCopy()
	copy.Status.Configuration.Parameters["work_mem"] = "64MB"
	if stored.Status.Configuration.Parameters["work_mem"] != "32MB" {
		t.Error("DeepCopy shared the observed configuration map")
	}
}
