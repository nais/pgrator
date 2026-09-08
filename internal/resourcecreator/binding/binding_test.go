package binding

import (
	"sort"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return scheme
}

func TestConfigSecretContainsEveryRequestedCredential(t *testing.T) {
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "mybinding", Namespace: "myteam"}, Spec: v1.PostgresBindingSpec{
		Postgres: "mydb", Consumer: v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "myapp"}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialAdmin, v1.PostgresBindingCredentialReadWrite},
	}}
	secret, err := CreateConfigSecret(newScheme(t), binding, "mydb-instance", []byte("ca"), map[v1.PostgresBindingCredential]CredentialMaterial{
		v1.PostgresBindingCredentialAdmin:     {Certificate: []byte("admin cert"), PrivateKey: []byte("admin key")},
		v1.PostgresBindingCredentialReadWrite: {Certificate: []byte("readwrite cert"), PrivateKey: []byte("readwrite key")},
	})
	if err != nil {
		t.Fatalf("CreateConfigSecret: %v", err)
	}
	keys := make([]string, 0, len(secret.StringData))
	for key := range secret.StringData {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	want := []string{"PGDATABASE", "PGHOST", "PGPORT", "PGSSLMODE", "PGUSER", "READWRITE_PGDATABASE", "READWRITE_PGHOST", "READWRITE_PGPORT", "READWRITE_PGSSLMODE", "READWRITE_PGUSER"}
	for i := range want {
		if i >= len(keys) || keys[i] != want[i] {
			t.Fatalf("keys = %v, want %v", keys, want)
		}
	}
	for key, want := range map[string]string{"ca.crt": "ca", "admin.tls.crt": "admin cert", "admin.tls.key": "admin key", "readwrite.tls.crt": "readwrite cert", "readwrite.tls.key": "readwrite key"} {
		if got := string(secret.Data[key]); got != want {
			t.Errorf("Data[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestDatabaseRoleNameIncludesInstance(t *testing.T) {
	binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: "mydb-myapp"}}
	got := DatabaseRoleName(binding, "mydb-restore", v1.PostgresBindingCredentialRead)
	if got != "mydb-myapp-mydb-restore-read" {
		t.Errorf("DatabaseRoleName() = %q", got)
	}
}
