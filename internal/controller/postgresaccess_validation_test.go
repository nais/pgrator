package controller

import (
	"context"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const postgresAccessValidationNamespace = "default"

func TestPostgresAccessDBAAdmission(t *testing.T) {
	access, _, _, _ := accessFixture()
	access.Name = "dba-access-validation"
	access.Namespace = postgresAccessValidationNamespace
	access.UID = ""
	access.Spec.AccessLevel = v1.PostgresAccessLevelDBA
	requireNoError(t, k8sClient.Create(t.Context(), access))
	t.Cleanup(func() { requireNoError(t, client.IgnoreNotFound(k8sClient.Delete(context.Background(), access))) })

	access.Spec.AccessLevel = v1.PostgresAccessLevelRead
	if err := k8sClient.Update(t.Context(), access); !apierrors.IsInvalid(err) {
		t.Fatalf("changing DBA access level must be rejected as immutable, got %v", err)
	}
}

func TestPostgresBindingRejectsDBACredential(t *testing.T) {
	for _, workloadType := range []v1.PostgresBindingWorkloadType{v1.PostgresBindingWorkloadTypeApplication, v1.PostgresBindingWorkloadTypeJob} {
		t.Run(string(workloadType), func(t *testing.T) {
			binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "dba-binding-validation-" + string(workloadType), Namespace: postgresAccessValidationNamespace}, Spec: v1.PostgresBindingSpec{
				Postgres:    "orders",
				Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "consumer", Type: workloadType}},
				Credentials: []v1.PostgresBindingCredential{"dba"},
			}}
			err := k8sClient.Create(t.Context(), binding)
			if err == nil {
				t.Cleanup(func() { requireNoError(t, client.IgnoreNotFound(k8sClient.Delete(context.Background(), binding))) })
			}
			if !apierrors.IsInvalid(err) {
				t.Fatalf("DBA workload credential must be rejected, got %v", err)
			}
		})
	}
}
