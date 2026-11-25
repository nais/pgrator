package v1_test

import (
	"encoding/json"
	"strings"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
)

// Deprecated branch fields stay on the wire so existing manifests and readers
// keep working.
func TestDeprecatedBranchFieldsKeepWireFormat(t *testing.T) {
	for _, tc := range []struct {
		name    string
		object  any
		wantKey string
	}{
		{"Postgres activeBranch", &v1.Postgres{Spec: v1.PostgresSpec{ActiveBranch: "main"}, Status: &v1.PostgresStatus{ActiveBranch: "main"}}, `"activeBranch":"main"`},
		{"Postgres backup.retentionDays", &v1.Postgres{Spec: v1.PostgresSpec{Backup: &v1.PostgresBackupPolicy{RetentionDays: new(int32(14))}}}, `"retentionDays":14`},
		{"PostgresBinding branch", &v1.PostgresBinding{Spec: v1.PostgresBindingSpec{Branch: "main"}}, `"branch":"main"`},
		{"PostgresAccess postgresBranch", &v1.PostgresAccess{Spec: v1.PostgresAccessSpec{PostgresBranch: "orders-main-65648812"}}, `"postgresBranch":"orders-main-65648812"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.object)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), tc.wantKey) {
				t.Errorf("wire format = %s, want %s", data, tc.wantKey)
			}
		})
	}
}
