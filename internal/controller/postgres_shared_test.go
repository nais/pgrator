package controller

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/initscheme"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestClusterConditionGetter(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	for _, testcase := range []struct {
		name        string
		phase       string
		phaseReason string
		ready       *metav1.Condition
		wantStatus  metav1.ConditionStatus
		wantReason  string
		wantMessage string
	}{
		{
			name: "ready cluster", phase: "Cluster in healthy state",
			ready:      &metav1.Condition{Status: metav1.ConditionTrue, Reason: "ClusterIsReady"},
			wantStatus: metav1.ConditionTrue, wantReason: "ClusterIsReady",
			wantMessage: "Cluster is in phase: Cluster in healthy state",
		},
		{
			name: "invalid image catalog", phase: "Cluster has incomplete or invalid image catalog",
			phaseReason: `extension "dblink" found in the Cluster Spec but no ImageVolumeSource.Reference is defined`,
			ready:       &metav1.Condition{Status: metav1.ConditionFalse, Reason: "ClusterIsNotReady", Message: "Cluster Is Not Ready"},
			wantStatus:  metav1.ConditionFalse, wantReason: "ClusterIsNotReady",
			wantMessage: `Cluster is in phase: Cluster has incomplete or invalid image catalog: extension "dblink" found in the Cluster Spec but no ImageVolumeSource.Reference is defined`,
		},
		{
			name:       "unknown readiness",
			ready:      &metav1.Condition{Status: metav1.ConditionUnknown, Reason: "Checking", Message: "Checking cluster readiness"},
			wantStatus: metav1.ConditionUnknown, wantReason: "Checking", wantMessage: "Checking cluster readiness",
		},
		{
			name: "missing readiness with phase", phase: "Setting up primary",
			wantStatus: metav1.ConditionUnknown, wantReason: "Unknown", wantMessage: "Cluster is in phase: Setting up primary",
		},
		{
			name:       "missing status",
			wantStatus: metav1.ConditionUnknown, wantReason: "Unknown", wantMessage: "No readiness status available on source resource",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Generation: 3},
				Status:     cnpgv1.ClusterStatus{Phase: testcase.phase, PhaseReason: testcase.phaseReason},
			}
			if testcase.ready != nil {
				ready := *testcase.ready
				ready.Type = string(cnpgv1.ConditionClusterReady)
				cluster.Status.Conditions = []metav1.Condition{ready}
			}
			conditions := clusterConditionGetter(cluster, scheme)
			if len(conditions) != 1 {
				t.Fatalf("got %d conditions, want 1", len(conditions))
			}
			condition := conditions[0]
			if condition.Type != "cluster.postgresql.cnpg.io/ObservedState" || condition.ObservedGeneration != cluster.Generation {
				t.Errorf("unexpected condition metadata: %+v", condition)
			}
			if condition.Status != testcase.wantStatus || condition.Reason != testcase.wantReason || condition.Message != testcase.wantMessage {
				t.Errorf("got condition %+v, want status %q, reason %q, message %q", condition, testcase.wantStatus, testcase.wantReason, testcase.wantMessage)
			}
		})
	}
}
