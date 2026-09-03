package v1

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func adminBinding(name, postgres string) *PostgresBinding {
	return &PostgresBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "myteam"},
		Spec: PostgresBindingSpec{
			Postgres: postgres,
			Consumer: PostgresBindingConsumer{
				Workload: &PostgresBindingWorkload{
					Name: name,
					Type: PostgresBindingWorkloadTypeApplication,
				},
			},
			SecretName: name + "-admin-client-cert",
			Role:       PostgresBindingRoleAdmin,
		},
	}
}

func newPostgresBindingValidator(t *testing.T, objects ...*PostgresBinding) *PostgresBindingValidator {
	t.Helper()

	scheme := runtime.NewScheme()
	requireNoError(t, AddToScheme(scheme))
	clientObjects := make([]client.Object, len(objects))
	for i, object := range objects {
		clientObjects[i] = object
	}
	return &PostgresBindingValidator{
		reader: fake.NewClientBuilder().WithScheme(scheme).WithObjects(clientObjects...).Build(),
	}
}

func TestPostgresBindingValidatorValidateCreate(t *testing.T) {
	tests := []struct {
		name     string
		existing []*PostgresBinding
		binding  *PostgresBinding
		wantErr  string
	}{
		{
			name:    "allows the first admin binding for a Postgres",
			binding: adminBinding("migrator", "mydb"),
		},
		{
			name:    "allows a maximum-length Kubernetes resource name",
			binding: adminBinding(strings.Repeat("a", 253), "mydb"),
		},
		{
			name:     "rejects another admin binding for the same Postgres",
			existing: []*PostgresBinding{adminBinding("migrator", "mydb")},
			binding:  adminBinding("other-migrator", "mydb"),
			wantErr:  `Postgres "mydb" already has admin binding "migrator"`,
		},
		{
			name:     "allows admin bindings for different Postgres resources",
			existing: []*PostgresBinding{adminBinding("migrator", "mydb")},
			binding:  adminBinding("other-migrator", "otherdb"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newPostgresBindingValidator(t, tt.existing...).ValidateCreate(context.Background(), tt.binding)
			if tt.wantErr != "" {
				requireErrorContains(t, err, tt.wantErr)
				return
			}
			requireNoError(t, err)
		})
	}
}

func TestPostgresBindingValidatorValidateUpdate(t *testing.T) {
	tests := []struct {
		name   string
		change func(*PostgresBinding)
	}{
		{name: "Postgres", change: func(binding *PostgresBinding) { binding.Spec.Postgres = "otherdb" }},
		{name: "workload name", change: func(binding *PostgresBinding) { binding.Spec.Consumer.Workload.Name = "otherapp" }},
		{name: "workload type", change: func(binding *PostgresBinding) { binding.Spec.Consumer.Workload.Type = PostgresBindingWorkloadTypeJob }},
		{name: "Secret name", change: func(binding *PostgresBinding) { binding.Spec.SecretName = "other-client-cert" }},
		{name: "role", change: func(binding *PostgresBinding) { binding.Spec.Role = PostgresBindingRoleReadWrite }},
	}

	for _, tt := range tests {
		t.Run("rejects "+tt.name+" changes", func(t *testing.T) {
			oldBinding := adminBinding("application", "mydb")
			newBinding := oldBinding.DeepCopy()
			tt.change(newBinding)

			_, err := newPostgresBindingValidator(t).ValidateUpdate(context.Background(), oldBinding, newBinding)
			requireErrorEqual(t, err, "spec is immutable")
		})
	}

	t.Run("allows metadata-only updates", func(t *testing.T) {
		oldBinding := adminBinding("application", "mydb")
		newBinding := oldBinding.DeepCopy()
		newBinding.Labels = map[string]string{"updated": "true"}

		_, err := newPostgresBindingValidator(t, oldBinding).ValidateUpdate(context.Background(), oldBinding, newBinding)
		requireNoError(t, err)
	})
}
