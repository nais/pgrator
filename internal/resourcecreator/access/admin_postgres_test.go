//go:build integration

package access

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/nais/pgrator/internal/config"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Run against a fresh, disposable PostgreSQL instance over a private Unix socket:
// PGRATOR_TEST_POSTGRES_HOST=/path/to/socket go test -tags=integration ./internal/resourcecreator/access -run TestPersonalAdminSQL
// The test creates database app and cluster-wide roles; never point it at a shared server.
func TestPersonalAdminSQL(t *testing.T) {
	host := os.Getenv("PGRATOR_TEST_POSTGRES_HOST")
	if host == "" {
		t.Skip("requires a disposable PostgreSQL instance via PGRATOR_TEST_POSTGRES_HOST")
	}
	if !strings.HasPrefix(host, "/") {
		t.Fatal("test database must use an explicit local Unix socket directory")
	}
	psql, err := exec.LookPath("psql")
	if err != nil {
		t.Fatal(err)
	}
	query := func(user, database, sql string) (string, error) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), psql, "-XAt", "--host", host, "--port", "5432", "--username", user, "--dbname", database,
			"--set", "ON_ERROR_STOP=1", "--set", "VERBOSITY=verbose", "--command", sql)
		output, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}
	assertions := func(t *testing.T) (func(string, string, string) string, func(string, string)) {
		mustQuery := func(user, database, sql string) string {
			t.Helper()
			output, err := query(user, database, sql)
			if err != nil {
				t.Fatalf("SQL as %s failed: %s: %v", user, output, err)
			}
			return output
		}
		denied := func(user, sql string) {
			t.Helper()
			output, err := query(user, "app", sql)
			if err == nil || !strings.Contains(output, "42501") {
				t.Fatalf("SQL as %s must fail with insufficient_privilege: output=%s error=%v", user, output, err)
			}
		}
		return mustQuery, denied
	}
	mustQuery, _ := assertions(t)
	quote := func(value string) string { return `"` + strings.ReplaceAll(value, `"`, `""`) + `"` }

	mustQuery("postgres", "postgres", "CREATE ROLE app LOGIN")
	mustQuery("postgres", "postgres", "CREATE DATABASE app OWNER app")
	cluster, err := rccnpg.CreateCluster(testScheme(t), &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-main", Namespace: "team"}, Spec: v1.PostgresSpec{MajorVersion: "18"},
	}, &config.Config{}, rccnpg.WALArchive{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range cluster.Spec.Bootstrap.InitDB.PostInitSQL {
		mustQuery("postgres", "postgres", sql)
	}
	for _, sql := range cluster.Spec.Bootstrap.InitDB.PostInitApplicationSQL {
		mustQuery("postgres", "app", sql)
	}
	mustQuery("app", "app", "CREATE TABLE public.existing (id bigint)")
	mustQuery("postgres", "postgres", "CREATE ROLE "+quote(rccnpg.PersonalAccessRole("orders-main"))+" NOLOGIN")
	for _, level := range []v1.PostgresAccessLevel{v1.PostgresAccessLevelRead, v1.PostgresAccessLevelReadWrite, v1.PostgresAccessLevelReadWriteCreate, v1.PostgresAccessLevelAdmin} {
		t.Run(string(level), func(t *testing.T) {
			mustQuery, denied := assertions(t)
			username := string(level) + "@example.com"
			access := &v1.PostgresAccess{Spec: v1.PostgresAccessSpec{Username: username, PostgresBranch: "orders-main", AccessLevel: level}}
			role, err := CreateDatabaseRole(testScheme(t), access, true)
			if err != nil {
				t.Fatal(err)
			}
			mustQuery("postgres", "postgres", "CREATE ROLE "+quote(role.Spec.Name)+" LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS")
			for _, group := range role.Spec.InRoles {
				mustQuery("postgres", "postgres", "GRANT "+quote(group)+" TO "+quote(role.Spec.Name))
			}
			if got := mustQuery(username, "app", "SELECT session_user || '/' || current_user"); got != username+"/"+username {
				t.Fatalf("personal identity lost: %s", got)
			}
			if got := mustQuery(username, "app", "SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls FROM pg_roles WHERE rolname = current_user"); got != "f" {
				t.Fatalf("personal access must not grant privileged role attributes: %s", got)
			}
			mustQuery(username, "app", "SELECT * FROM public.existing")
			if level == v1.PostgresAccessLevelRead {
				denied(username, "INSERT INTO public.existing VALUES (1)")
			} else {
				mustQuery(username, "app", "INSERT INTO public.existing VALUES (1)")
			}
			if level != v1.PostgresAccessLevelAdmin {
				denied(username, "CREATE SCHEMA debezium")
				denied(username, "ALTER TABLE public.existing ADD COLUMN forbidden text")
				denied(username, "SET ROLE app")
				if level == v1.PostgresAccessLevelReadWriteCreate {
					mustQuery(username, "app", "CREATE TABLE public.legacy_personal (id bigint)")
				} else {
					denied(username, "CREATE TABLE public.forbidden (id bigint)")
				}
				return
			}
			mustQuery(username, "app", "CREATE SCHEMA debezium")
			mustQuery(username, "app", "ALTER TABLE public.existing ADD COLUMN permitted text")
			mustQuery(username, "app", "CREATE TABLE public.personal_owned (id bigint)")
			if got := mustQuery(username, "app", "SET ROLE app; SELECT session_user || '/' || current_user"); !strings.HasSuffix(got, username+"/app") {
				t.Fatalf("SET ROLE must retain authenticated identity: %s", got)
			}
			mustQuery(username, "app", "SET ROLE app; CREATE TABLE public.app_owned (id bigint)")
			mustQuery("read@example.com", "app", "SELECT * FROM public.app_owned")
			mustQuery(username, "app", "DROP TABLE public.app_owned")
			denied(username, "CREATE ROLE forbidden")
			oid := mustQuery("postgres", "app", "SELECT oid FROM pg_roles WHERE rolname = 'admin@example.com'")
			// CNPG applies the inactive configuration before its Retain CR is removed.
			inactive, err := CreateDatabaseRole(testScheme(t), access, false)
			if err != nil {
				t.Fatal(err)
			}
			if inactive.Spec.Login || !inactive.Spec.DisablePassword || len(inactive.Spec.InRoles) != 0 {
				t.Fatal("inactive role must revoke login, password and memberships")
			}
			mustQuery("postgres", "postgres", "ALTER ROLE "+quote(username)+" NOLOGIN PASSWORD NULL")
			for _, group := range role.Spec.InRoles {
				mustQuery("postgres", "postgres", "REVOKE "+quote(group)+" FROM "+quote(username))
			}
			if got := mustQuery("postgres", "app", "SELECT oid FROM pg_roles WHERE rolname = 'admin@example.com'"); got != oid {
				t.Fatal("revocation must preserve the role OID")
			}
			if got := mustQuery("postgres", "app", "SELECT tableowner FROM pg_tables WHERE schemaname = 'public' AND tablename = 'personal_owned'"); got != username {
				t.Fatal("revocation must preserve owned objects")
			}
			if got := mustQuery("postgres", "app", "SELECT pg_has_role('admin@example.com', 'app', 'MEMBER')"); got != "f" {
				t.Fatal("revoked personal identity must not retain owner membership")
			}
			// Authenticate as postgres, then exercise the retained identity without
			// relying on NOLOGIN to conceal residual database-owner privileges.
			output, err := query("postgres", "app", "SET SESSION AUTHORIZATION "+quote(username)+"; CREATE SCHEMA after_expiry")
			if err == nil || !strings.Contains(output, "42501") {
				t.Fatalf("revoked admin must not retain CREATE SCHEMA: %s, %v", output, err)
			}
		})
	}
}
