package binding

import (
	"sort"
	"strings"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
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
		Postgres: "mydb", SecretName: "myapp-mydb-connection", Consumer: v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "myapp"}},
		Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialAdmin, v1.PostgresBindingCredentialReadWrite},
	}}
	secret, err := CreateConfigSecret(newScheme(t), binding, "mydb-instance", []byte("ca"), map[v1.PostgresBindingCredential]CredentialMaterial{
		v1.PostgresBindingCredentialAdmin:     {Certificate: []byte("admin cert"), PrivateKey: []byte("admin key")},
		v1.PostgresBindingCredentialReadWrite: {Certificate: []byte("readwrite cert"), PrivateKey: []byte("readwrite key")},
	})
	if err != nil {
		t.Fatalf("CreateConfigSecret: %v", err)
	}
	if got, want := secret.Name, binding.Spec.SecretName; got != want {
		t.Errorf("Secret name = %q, want %q", got, want)
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

const longBindingName = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestConfigSecretLabelShortenedForLongBindingName(t *testing.T) {
	b := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: longBindingName, Namespace: "myteam"}}
	secret, err := CreateConfigSecret(newScheme(t), b, "mydb-instance", nil, nil)
	if err != nil {
		t.Fatalf("CreateConfigSecret: %v", err)
	}
	label := secret.Labels["postgres.nais.io/binding"]
	if len(label) > 63 {
		t.Errorf("label length = %d, want <= 63", len(label))
	}
	if errs := validation.IsValidLabelValue(label); len(errs) > 0 {
		t.Errorf("label %q invalid: %v", label, errs)
	}
	again, err := CreateConfigSecret(newScheme(t), b, "mydb-instance", nil, nil)
	if err != nil {
		t.Fatalf("CreateConfigSecret again: %v", err)
	}
	if got, want := again.Labels["postgres.nais.io/binding"], label; got != want {
		t.Errorf("label not deterministic: got %q, want %q", got, want)
	}
}

func TestEgressNetworkPolicyNameShortenedForLongBindingName(t *testing.T) {
	want := shortenedName(longBindingName, "-egress", 253)
	b := &v1.PostgresBinding{
		ObjectMeta: metav1.ObjectMeta{Name: longBindingName, Namespace: "myteam"},
		Spec:       v1.PostgresBindingSpec{Consumer: v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "myapp"}}},
	}
	netpol1, err := CreateEgressNetworkPolicy(newScheme(t), b, "mydb-instance")
	if err != nil {
		t.Fatalf("CreateEgressNetworkPolicy: %v", err)
	}
	name := netpol1.Name
	if len(name) > 253 {
		t.Errorf("network policy name length = %d, want <= 253", len(name))
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		t.Errorf("network policy name %q invalid: %v", name, errs)
	}
	if got := name; got != want {
		t.Errorf("network policy name = %q, want %q", got, want)
	}
	if !strings.HasSuffix(name, "-egress") {
		t.Errorf("network policy name %q does not have expected hash suffix", name)
	}
	netpol2, err := CreateEgressNetworkPolicy(newScheme(t), b, "mydb-instance")
	if err != nil {
		t.Fatalf("CreateEgressNetworkPolicy again: %v", err)
	}
	if netpol2.Name != name {
		t.Errorf("network policy name not deterministic: got %q, want %q", netpol2.Name, name)
	}
}

func TestDatabaseRoleNameWithinLimitForLongBindingName(t *testing.T) {
	b := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: longBindingName}}
	role := DatabaseRoleName(b, "mydb-instance", v1.PostgresBindingCredentialRead)
	if len(role) > 241 {
		t.Errorf("DatabaseRoleName length = %d, want <= 241", len(role))
	}
}
