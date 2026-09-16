package access

import (
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDatabaseRoleName(t *testing.T) {
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
			if got := DatabaseRoleName(tt.username, tt.instance); got != tt.want {
				t.Errorf("DatabaseRoleName() = %q, want %q", got, tt.want)
			}
		})
	}

	first := DatabaseRoleName("frode.sundby@nav.no", "orders")
	second := DatabaseRoleName("frode.sundby@example.com", "orders")
	if first == second {
		t.Error("different full email addresses must not share a role name")
	}

	long := DatabaseRoleName(strings.Repeat("a", 100)+"@nav.no", strings.Repeat("b", 100))
	if len(long) > roleNameLimit {
		t.Errorf("DatabaseRoleName() length = %d, want <= %d", len(long), roleNameLimit)
	}
}

func TestCreateDatabaseRole(t *testing.T) {
	access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{
		Username: "frode.sundby@nav.no", PostgresInstance: "orders-restore",
	}}

	role := CreateDatabaseRole(access, false)
	if role.Name != "frode-sundby-orders-restore-39901eb0e00a4f9c" {
		t.Errorf("role metadata name = %q", role.Name)
	}
	if role.Spec.Name != role.Name {
		t.Errorf("role spec name = %q, want %q", role.Spec.Name, role.Name)
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
		t.Error("durable identity must not have access-specific credentials or memberships")
	}
	if strings.Contains(role.Spec.Comment, access.Spec.Username) {
		t.Error("role comment must not expose the user's full email address")
	}
}
