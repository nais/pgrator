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
		Postgres: postgres, SecretName: name + "-connection", Consumer: PostgresBindingConsumer{Workload: &PostgresBindingWorkload{Name: name, Type: PostgresBindingWorkloadTypeApplication}},
		Credentials: []PostgresBindingCredential{PostgresBindingCredentialAdmin},
	}}
}

func readBinding(name, postgres string) *PostgresBinding {
	return &PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "myteam"}, Spec: PostgresBindingSpec{
		Postgres: postgres, SecretName: name + "-connection", Consumer: PostgresBindingConsumer{Workload: &PostgresBindingWorkload{Name: name, Type: PostgresBindingWorkloadTypeApplication}},
		Credentials: []PostgresBindingCredential{PostgresBindingCredentialRead},
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
	ctx := context.Background()
	validator := newPostgresBindingValidator(t, adminBinding("first", "mydb"))

	t.Run("ValidateCreate", func(t *testing.T) {
		t.Run("first admin binding for a Postgres allowed", func(t *testing.T) {
			_, err := validator.ValidateCreate(ctx, adminBinding("second", "otherdb"))
			requireNoError(t, err)
		})

		t.Run("second admin binding for same Postgres rejected", func(t *testing.T) {
			_, err := validator.ValidateCreate(ctx, adminBinding("second", "mydb"))
			requireErrorContains(t, err, `Postgres "mydb" already has admin binding "first"`)
		})

		t.Run("admin binding for a different Postgres allowed", func(t *testing.T) {
			_, err := validator.ValidateCreate(ctx, adminBinding("third", "otherdb"))
			requireNoError(t, err)
		})

		t.Run("binding without admin credential allowed even when admin binding exists", func(t *testing.T) {
			_, err := validator.ValidateCreate(ctx, readBinding("reader", "mydb"))
			requireNoError(t, err)
		})
	})

	t.Run("ValidateUpdate", func(t *testing.T) {
		t.Run("credentials change allowed", func(t *testing.T) {
			old := adminBinding("first", "mydb")
			updated := old.DeepCopy()
			updated.Spec.Credentials = []PostgresBindingCredential{PostgresBindingCredentialRead}
			_, err := validator.ValidateUpdate(ctx, old, updated)
			requireNoError(t, err)
		})

		t.Run("one-time secretName set allowed when old was empty", func(t *testing.T) {
			old := adminBinding("first", "mydb")
			old.Spec.SecretName = ""
			updated := old.DeepCopy()
			updated.Spec.SecretName = "first-connection"
			_, err := validator.ValidateUpdate(ctx, old, updated)
			requireNoError(t, err)
		})

		t.Run("immutable fields", func(t *testing.T) {
			old := adminBinding("first", "mydb")
			old.Spec.SecretName = "first-connection"

			tests := []struct {
				name    string
				mutate  func(*PostgresBinding)
				wantErr string
			}{
				{
					name: "workload name change rejected",
					mutate: func(b *PostgresBinding) {
						b.Spec.Consumer.Workload.Name = "changed"
					},
					wantErr: "postgres, secretName, and consumer are immutable",
				},
				{
					name: "workload type change rejected",
					mutate: func(b *PostgresBinding) {
						b.Spec.Consumer.Workload.Type = PostgresBindingWorkloadTypeJob
					},
					wantErr: "postgres, secretName, and consumer are immutable",
				},
				{
					name: "postgres change rejected",
					mutate: func(b *PostgresBinding) {
						b.Spec.Postgres = "other"
					},
					wantErr: "postgres, secretName, and consumer are immutable",
				},
				{
					name: "secretName change rejected when old secretName set",
					mutate: func(b *PostgresBinding) {
						b.Spec.SecretName = "changed-connection"
					},
					wantErr: "postgres, secretName, and consumer are immutable",
				},
				{
					name: "metadata-only update allowed",
					mutate: func(b *PostgresBinding) {
						b.Labels = map[string]string{"foo": "bar"}
					},
					wantErr: "",
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					updated := old.DeepCopy()
					tt.mutate(updated)
					_, err := validator.ValidateUpdate(ctx, old, updated)
					if tt.wantErr == "" {
						requireNoError(t, err)
					} else {
						requireErrorEqual(t, err, tt.wantErr)
					}
				})
			}
		})
	})
}
