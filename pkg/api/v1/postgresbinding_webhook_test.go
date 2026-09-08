package v1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func adminBinding(name, postgres string) *PostgresBinding {
	return &PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "myteam"}, Spec: PostgresBindingSpec{
		Postgres: postgres, Consumer: PostgresBindingConsumer{Workload: &PostgresBindingWorkload{Name: name, Type: PostgresBindingWorkloadTypeApplication}},
		Credentials: []PostgresBindingCredential{PostgresBindingCredentialAdmin},
	}}
}

func newPostgresBindingValidator(t *testing.T, objects ...*PostgresBinding) *PostgresBindingValidator {
	t.Helper()
	scheme := runtime.NewScheme()
	requireNoError(t, AddToScheme(scheme))
	clientObjects := make([]client.Object, len(objects))
	for i := range objects {
		clientObjects[i] = objects[i]
	}
	return &PostgresBindingValidator{reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(clientObjects...).Build()}
}

func TestPostgresBindingValidator(t *testing.T) {
	validator := newPostgresBindingValidator(t, adminBinding("first", "mydb"))
	_, err := validator.ValidateCreate(context.Background(), adminBinding("second", "mydb"))
	requireErrorContains(t, err, `Postgres "mydb" already has admin binding "first"`)

	old := adminBinding("first", "mydb")
	updated := old.DeepCopy()
	updated.Spec.Credentials = []PostgresBindingCredential{PostgresBindingCredentialRead}
	_, err = validator.ValidateUpdate(context.Background(), old, updated)
	requireNoError(t, err)
	updated.Spec.Postgres = "other"
	_, err = validator.ValidateUpdate(context.Background(), old, updated)
	requireErrorEqual(t, err, "postgres and consumer are immutable")
}
