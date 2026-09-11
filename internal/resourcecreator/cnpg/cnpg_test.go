package cnpg

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

func TestClusterNameFor(t *testing.T) {
	tests := []struct {
		name     string
		postgres string
		want     string
	}{
		{
			name:     "prefixes postgres name",
			postgres: "mydb",
			want:     "pg-mydb",
		},
		{
			name:     "shortens long postgres name",
			postgres: strings.Repeat("database", 8),
			want:     "pg-databasedatabasedatabasedatabasedataba-bdf7c82f",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClusterNameFor(tt.postgres); got != tt.want {
				t.Errorf("ClusterNameFor(%q) = %q, want %q", tt.postgres, got, tt.want)
			}
		})
	}
}

func TestPoolerNameFor(t *testing.T) {
	if got, want := PoolerNameFor("mydb"), "pg-mydb-pooler"; got != want {
		t.Errorf("PoolerNameFor() = %q, want %q", got, want)
	}
}

func TestAuditConfigurationExemptsApplicationLoginRoles(t *testing.T) {
	parameters := makePostgresParameters(resource.MustParse("512Mi"))
	if got, want := parameters["pgaudit.log"], "read,write,ddl,role"; got != want {
		t.Errorf("pgaudit.log = %q, want %q", got, want)
	}

	assertSQLContains(t, postInitSQL(), "ALTER ROLE postgres SET pgaudit.log = 'none'")
	assertSQLContains(t, postInitApplicationSQL(), "ALTER ROLE app SET pgaudit.log = 'none'")
}

func assertSQLContains(t *testing.T, statements []string, want string) {
	t.Helper()
	for _, statement := range statements {
		if statement == want {
			return
		}
	}
	t.Errorf("SQL statements = %#v, want %q", statements, want)
}
