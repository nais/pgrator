package access

import (
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDatabaseRoleResourceName(t *testing.T) {
	tests := []struct {
		name     string
		username string
		instance string
		want     string
	}{
		{name: "email local part", username: "frode.sundby@nav.no", instance: "orders-restore", want: "frode-sundby-orders-restore-39901eb0e00a4f9c"},
		{name: "normalizes local part", username: "Frode_Sundby@nav.no", instance: "orders", want: "frode-sundby-orders-10d081ca63812026"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DatabaseRoleResourceName(tt.username, tt.instance); got != tt.want {
				t.Errorf("DatabaseRoleResourceName() = %q, want %q", got, tt.want)
			}
		})
	}

	first := DatabaseRoleResourceName("frode.sundby@nav.no", "orders")
	second := DatabaseRoleResourceName("frode.sundby@example.com", "orders")
	if first == second {
		t.Error("different full email addresses must not share a resource name")
	}

	long := DatabaseRoleResourceName(strings.Repeat("a", 100)+"@nav.no", strings.Repeat("b", 100))
	if len(long) > roleNameLimit {
		t.Errorf("DatabaseRoleResourceName() length = %d, want <= %d", len(long), roleNameLimit)
	}
}

func TestCreateDatabaseRole(t *testing.T) {
	access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{
		Username: "frode.sundby@nav.no", PostgresInstance: "orders-restore",
	}}

	role, err := CreateDatabaseRole(testScheme(t), access, false)
	if err != nil {
		t.Fatalf("CreateDatabaseRole() error = %v", err)
	}
	wantResourceName := "frode-sundby-orders-restore-39901eb0e00a4f9c"
	if role.Name != wantResourceName {
		t.Errorf("role metadata name = %q, want %q", role.Name, wantResourceName)
	}
	if role.Spec.Name != access.Spec.Username {
		t.Errorf("role spec name = %q, want %q", role.Spec.Name, access.Spec.Username)
	}
	if role.Spec.ClusterRef.Name != "pg-orders-restore" {
		t.Errorf("cluster = %q, want pg-orders-restore", role.Spec.ClusterRef.Name)
	}
	if role.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimRetain {
		t.Errorf("reclaim policy = %q, want retain", role.Spec.ReclaimPolicy)
	}
	if role.Spec.Login || role.Spec.Superuser || role.Spec.CreateDB || role.Spec.CreateRole || role.Spec.Replication || role.Spec.BypassRLS {
		t.Error("durable identity has unexpected active or privileged role attributes")
	}
	if role.Spec.PasswordSecret != nil || len(role.Spec.InRoles) != 0 {
		t.Error("inactive identity must not have access-specific credentials or memberships")
	}
	owner := metav1.GetControllerOf(role)
	if owner == nil || owner.Kind != "PostgresAccess" || owner.Name != access.Name {
		t.Error("DatabaseRole must be owned by PostgresAccess")
	}
}

func TestCreateDatabaseRoleUsesRawUsername(t *testing.T) {
	access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{
		Username: "personal-access-e2e@nav.no", PostgresInstance: "orders-restore",
	}}

	role, err := CreateDatabaseRole(testScheme(t), access, true)
	if err != nil {
		t.Fatalf("CreateDatabaseRole() error = %v", err)
	}
	if role.Spec.Name != access.Spec.Username {
		t.Errorf("role spec name = %q, want %q", role.Spec.Name, access.Spec.Username)
	}
	if role.Name == access.Spec.Username {
		t.Errorf("role metadata name must not be the raw username")
	}
	if role.Name != DatabaseRoleResourceName(access.Spec.Username, access.Spec.PostgresInstance) {
		t.Errorf("role metadata name = %q, want deterministic resource name", role.Name)
	}
}

func TestCreateCredentialSecretUsesRawUsername(t *testing.T) {
	access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{
		Username: "personal-access-e2e@nav.no", PostgresInstance: "orders-restore",
	}}

	caPEM := "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----\n"
	secret, err := CreateCredentialSecret(testScheme(t), access, "hunter2", caPEM)
	if err != nil {
		t.Fatalf("CreateCredentialSecret() error = %v", err)
	}
	if got := secret.StringData[corev1.BasicAuthUsernameKey]; got != access.Spec.Username {
		t.Errorf("credential username = %q, want %q", got, access.Spec.Username)
	}
	if got := secret.StringData["ca.crt"]; got != caPEM {
		t.Errorf("credential ca.crt = %q, want %q", got, caPEM)
	}
}
