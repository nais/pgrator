package binding

import (
	"sort"
	"strings"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
)

func newBinding(name string) *v1.PostgresBinding {
	return &v1.PostgresBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "myteam", UID: types.UID(name)},
		Spec: v1.PostgresBindingSpec{
			Postgres:   "mydb",
			Workload:   v1.PostgresBindingWorkload{Name: "myapp"},
			SecretName: "mydb-myapp-read-client-cert",
			Role:       v1.PostgresBindingRoleRead,
		},
	}
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return scheme
}

func TestValidDeterministicLabelForMaxLengthBindingName(t *testing.T) {
	scheme := newScheme(t)
	binding := newBinding(strings.Repeat("a", 253))

	secret, err := CreateConfigSecret(scheme, binding)
	if err != nil {
		t.Fatalf("CreateConfigSecret: %v", err)
	}
	label := secret.Labels[nameLabel]
	wantLabel := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-32859a3ab65ac529"
	if label != wantLabel {
		t.Errorf("label = %q, want %q", label, wantLabel)
	}
	if errs := validation.IsValidLabelValue(label); len(errs) != 0 {
		t.Errorf("IsValidLabelValue(%q) = %v, want no errors", label, errs)
	}

	egress, err := CreateEgressNetworkPolicy(scheme, binding)
	if err != nil {
		t.Fatalf("CreateEgressNetworkPolicy: %v", err)
	}
	if len(egress.Name) != 253 {
		t.Errorf("egress.Name length = %d, want 253", len(egress.Name))
	}
	if !strings.HasSuffix(egress.Name, "-ace6074bdfd5c870-egress") {
		t.Errorf("egress.Name = %q, want suffix -ace6074bdfd5c870-egress", egress.Name)
	}
	if errs := validation.IsDNS1123Subdomain(egress.Name); len(errs) != 0 {
		t.Errorf("IsDNS1123Subdomain(%q) = %v, want no errors", egress.Name, errs)
	}
}

func TestConfigSecretUsesRoleSpecificEnvVarPrefixes(t *testing.T) {
	scheme := newScheme(t)

	tests := []struct {
		role   v1.PostgresBindingRole
		prefix string
	}{
		{role: v1.PostgresBindingRoleAdmin, prefix: ""},
		{role: v1.PostgresBindingRoleRead, prefix: "READ_"},
		{role: v1.PostgresBindingRoleReadWrite, prefix: "READWRITE_"},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			binding := &v1.PostgresBinding{
				ObjectMeta: metav1.ObjectMeta{Name: "mybinding", Namespace: "myteam"},
				Spec: v1.PostgresBindingSpec{
					Postgres: "mydb",
					Workload: v1.PostgresBindingWorkload{Name: "myapp"},
					Role:     tt.role,
				},
			}
			secret, err := CreateConfigSecret(scheme, binding)
			if err != nil {
				t.Fatalf("CreateConfigSecret: %v", err)
			}

			keys := make([]string, 0, len(secret.StringData))
			for key := range secret.StringData {
				keys = append(keys, key)
			}
			sort.Strings(keys)

			want := []string{
				tt.prefix + "PGDATABASE",
				tt.prefix + "PGHOST",
				tt.prefix + "PGPORT",
				tt.prefix + "PGSSLMODE",
				tt.prefix + "PGUSER",
			}
			if len(keys) != len(want) {
				t.Fatalf("keys = %v, want %v", keys, want)
			}
			for i := range keys {
				if keys[i] != want[i] {
					t.Errorf("keys = %v, want %v", keys, want)
					break
				}
			}
		})
	}
}
