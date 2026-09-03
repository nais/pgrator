package v1

import (
	"strings"
	"testing"
)

func TestPostgresBindingRoleName(t *testing.T) {
	tests := []struct {
		name     string
		role     PostgresBindingRole
		workload string
		want     string
	}{
		{name: "read", role: PostgresBindingRoleRead, workload: "reporter", want: "reporter-read"},
		{name: "readwrite", role: PostgresBindingRoleReadWrite, workload: "reporter", want: "reporter-readwrite"},
		{name: "admin", role: PostgresBindingRoleAdmin, workload: "reporter", want: "app"},
		{name: "long read workload", role: PostgresBindingRoleRead, workload: strings.Repeat("a", 63), want: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-6c913093f3d95ca4-read"},
		{name: "long readwrite workload", role: PostgresBindingRoleReadWrite, workload: strings.Repeat("a", 63), want: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-3c51106b347a274d-readwrite"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binding := &PostgresBinding{Spec: PostgresBindingSpec{
				Consumer: PostgresBindingConsumer{
					Workload: &PostgresBindingWorkload{Name: tt.workload},
				},
				Role: tt.role,
			}}

			got := binding.RoleName()
			if got != tt.want {
				t.Errorf("RoleName() = %q, want %q", got, tt.want)
			}
			if len(got) > 63 {
				t.Errorf("len(RoleName()) = %d, want at most 63", len(got))
			}
		})
	}
}

func TestPostgresBindingDerivedNames(t *testing.T) {
	t.Run("strips the CNPG suffix from the DatabaseRole name", func(t *testing.T) {
		binding := &PostgresBinding{Spec: PostgresBindingSpec{
			SecretName: "mydb-myapp-readwrite-client-cert",
		}}

		if got, want := binding.DatabaseRoleName(), "mydb-myapp-readwrite"; got != want {
			t.Errorf("DatabaseRoleName() = %q, want %q", got, want)
		}
		if got, want := binding.ClientCertSecretName(), "mydb-myapp-readwrite-client-cert"; got != want {
			t.Errorf("ClientCertSecretName() = %q, want %q", got, want)
		}
	})

	t.Run("keeps every derived name within the Kubernetes DNS-subdomain limit", func(t *testing.T) {
		binding := &PostgresBinding{Spec: PostgresBindingSpec{
			SecretName: strings.Repeat("a", 241) + "-client-cert",
		}}

		if got, want := len(binding.DatabaseRoleName()), 241; got != want {
			t.Errorf("len(DatabaseRoleName()) = %d, want %d", got, want)
		}
		if got, want := len(binding.ClientCertSecretName()), 253; got != want {
			t.Errorf("len(ClientCertSecretName()) = %d, want %d", got, want)
		}
	})
}
