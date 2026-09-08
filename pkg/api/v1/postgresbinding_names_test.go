package v1

import (
	"strings"
	"testing"
)

func TestPostgresBindingRoleName(t *testing.T) {
	tests := []struct {
		name       string
		credential PostgresBindingCredential
		workload   string
		want       string
	}{
		{name: "read", credential: PostgresBindingCredentialRead, workload: "reporter", want: "reporter-read"},
		{name: "readwrite", credential: PostgresBindingCredentialReadWrite, workload: "reporter", want: "reporter-readwrite"},
		{name: "admin", credential: PostgresBindingCredentialAdmin, workload: "reporter", want: "app"},
		{name: "long read workload", credential: PostgresBindingCredentialRead, workload: strings.Repeat("a", 63), want: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-6c913093f3d95ca4-read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binding := &PostgresBinding{Spec: PostgresBindingSpec{Consumer: PostgresBindingConsumer{Workload: &PostgresBindingWorkload{Name: tt.workload}}}}
			if got := binding.RoleName(tt.credential); got != tt.want {
				t.Errorf("RoleName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPostgresBindingCredentials(t *testing.T) {
	binding := &PostgresBinding{Spec: PostgresBindingSpec{Credentials: []PostgresBindingCredential{PostgresBindingCredentialAdmin, PostgresBindingCredentialReadWrite}}}
	if !binding.HasCredential(PostgresBindingCredentialAdmin) || binding.HasCredential(PostgresBindingCredentialRead) {
		t.Error("HasCredential() did not reflect requested credentials")
	}
	if got := ConnectionEnvPrefix(PostgresBindingCredentialReadWrite); got != "READWRITE_" {
		t.Errorf("ConnectionEnvPrefix() = %q, want READWRITE_", got)
	}
}
