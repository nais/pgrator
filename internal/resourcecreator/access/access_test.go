package access

import (
	"slices"
	"strings"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
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
		{name: "email local part", username: "frode.sundby@nav.no", instance: "orders-restore", want: "frode-sundby-orders-restore-email-39901eb0e00a4f9c"},
		{name: "normalizes local part", username: "Frode_Sundby@nav.no", instance: "orders", want: "frode-sundby-orders-email-10d081ca63812026"},
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
		Username: "frode.sundby@nav.no", PostgresBranch: "orders-restore",
	}}

	role, err := CreateDatabaseRole(testScheme(t), access, false)
	if err != nil {
		t.Fatalf("CreateDatabaseRole() error = %v", err)
	}
	if role.Name != "frode-sundby-orders-restore-email-39901eb0e00a4f9c" {
		t.Errorf("role metadata name = %q", role.Name)
	}
	if role.Spec.Name != access.Spec.Username {
		t.Errorf("PostgreSQL role name = %q, want %q", role.Spec.Name, access.Spec.Username)
	}
	otherBranch := access.DeepCopy()
	otherBranch.Spec.PostgresBranch = "orders-main"
	otherRole, err := CreateDatabaseRole(testScheme(t), otherBranch, false)
	if err != nil {
		t.Fatalf("CreateDatabaseRole(other branch) error = %v", err)
	}
	if otherRole.Name == role.Name || otherRole.Spec.Name != role.Spec.Name {
		t.Errorf("other branch must use a separate Kubernetes resource but the same PostgreSQL identity: %q / %q", otherRole.Name, otherRole.Spec.Name)
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
	if strings.Contains(role.Spec.Comment, access.Spec.Username) {
		t.Error("role comment must not expose the user's full email address")
	}
}

func TestCredentialSecretUsesPostgreSQLIdentity(t *testing.T) {
	access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{
		Username: "frode.sundby@nav.no", PostgresBranch: "orders-main",
	}}
	secret, err := CreateCredentialSecret(testScheme(t), access, "password")
	if err != nil {
		t.Fatalf("CreateCredentialSecret() error = %v", err)
	}
	if got := secret.StringData["username"]; got != access.Spec.Username {
		t.Errorf("credential username = %q, want %q", got, access.Spec.Username)
	}
}

func TestPersonalAccessMemberships(t *testing.T) {
	tests := []struct {
		name  string
		level v1.PostgresAccessLevel
		roles []string
	}{
		{name: "read", level: v1.PostgresAccessLevelRead, roles: []string{rccnpg.ReadRole}},
		{name: "readwrite", level: v1.PostgresAccessLevelReadWrite, roles: []string{rccnpg.ReadWriteRole}},
		{name: "readwritecreate", level: v1.PostgresAccessLevelReadWriteCreate, roles: []string{rccnpg.ReadWriteCreateRole}},
		{name: "maintain", level: v1.PostgresAccessLevelMaintain, roles: []string{"pg_monitor", "pg_maintain"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{
				Username: "frode@nav.no", PostgresBranch: "orders-main", AccessLevel: tt.level,
				ExpiresAt: metav1.NewTime(time.Now().Add(time.Hour)),
			}}
			role, err := CreateDatabaseRole(testScheme(t), access, true)
			if err != nil {
				t.Fatalf("CreateDatabaseRole() error = %v", err)
			}
			want := append(slices.Clone(tt.roles), rccnpg.PersonalAccessRole(access.Spec.PostgresBranch))
			if !slices.Equal(role.Spec.InRoles, want) {
				t.Errorf("memberships = %v, want %v", role.Spec.InRoles, want)
			}
			if !role.Spec.Login || role.Spec.PasswordSecret == nil || role.Spec.PasswordSecret.Name != CredentialSecretName(access) || role.Spec.ValidUntil == nil || !role.Spec.ValidUntil.Equal(&access.Spec.ExpiresAt) {
				t.Error("active personal access must have login, its password Secret and expiry")
			}
			if role.Spec.Superuser || role.Spec.CreateDB || role.Spec.CreateRole || role.Spec.Replication || role.Spec.BypassRLS {
				t.Error("personal access must not enable privileged role attributes")
			}
			inactive, err := CreateDatabaseRole(testScheme(t), access, false)
			if err != nil {
				t.Fatalf("CreateDatabaseRole(inactive) error = %v", err)
			}
			if inactive.Spec.Name != role.Spec.Name || inactive.Name != role.Name {
				t.Error("deactivation must preserve the personal identity")
			}
			if inactive.Spec.Login || !inactive.Spec.DisablePassword || inactive.Spec.PasswordSecret != nil || len(inactive.Spec.InRoles) != 0 {
				t.Error("inactive personal access must disable login and password without retaining memberships")
			}
		})
	}
}

func TestPersonalAccessDowngradeFromMaintain(t *testing.T) {
	access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "maintain-access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{
		Username: "frode@nav.no", PostgresBranch: "orders-main", AccessLevel: v1.PostgresAccessLevelMaintain,
	}}
	maintain, err := CreateDatabaseRole(testScheme(t), access, true)
	if err != nil {
		t.Fatal(err)
	}
	// Access requests are immutable: a later request reuses the durable identity.
	access.Name = "read-access"
	access.Spec.AccessLevel = v1.PostgresAccessLevelRead
	read, err := CreateDatabaseRole(testScheme(t), access, true)
	if err != nil {
		t.Fatal(err)
	}
	if read.Name != maintain.Name || read.Spec.Name != maintain.Spec.Name {
		t.Error("a later access must reuse the durable personal identity")
	}
	want := []string{rccnpg.ReadRole, rccnpg.PersonalAccessRole(access.Spec.PostgresBranch)}
	if !slices.Equal(read.Spec.InRoles, want) {
		t.Errorf("downgraded memberships = %v, want %v", read.Spec.InRoles, want)
	}
}

func TestPersonalAccessRejectsUnsupportedLevels(t *testing.T) {
	for _, level := range []v1.PostgresAccessLevel{"", "admin", "dba", "unknown"} {
		t.Run(string(level), func(t *testing.T) {
			access := &v1.PostgresAccess{Spec: v1.PostgresAccessSpec{
				Username: "frode@nav.no", PostgresBranch: "orders-main", AccessLevel: level,
			}}
			if _, err := CreateDatabaseRole(testScheme(t), access, true); err == nil {
				t.Fatal("unsupported access level must not grant privileges")
			}
		})
	}
}

func TestPostgresRoleRejectsUsernameTruncation(t *testing.T) {
	access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{
		Username: strings.Repeat("a", roleNameLimit) + "@nav.no", PostgresBranch: "orders-main",
	}}
	if _, err := CreateCredentialSecret(testScheme(t), access, "password"); err == nil {
		t.Fatal("credential must not use a truncated PostgreSQL username")
	}
	if _, err := CreateDatabaseRole(testScheme(t), access, true); err == nil {
		t.Fatal("DatabaseRole must not use a truncated PostgreSQL username")
	}
}
