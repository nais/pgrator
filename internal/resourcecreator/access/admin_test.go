package access

import (
	"slices"
	"testing"

	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPersonalAdminDowngrade(t *testing.T) {
	for _, level := range []v1.PostgresAccessLevel{v1.PostgresAccessLevelRead, v1.PostgresAccessLevelReadWrite, v1.PostgresAccessLevelReadWriteCreate, v1.PostgresAccessLevelMaintain} {
		t.Run(string(level), func(t *testing.T) {
			access := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "admin-access", Namespace: "team"}, Spec: v1.PostgresAccessSpec{
				Username: "user@example.com", PostgresBranch: "orders-main", AccessLevel: v1.PostgresAccessLevelAdmin,
			}}
			admin, err := CreateDatabaseRole(testScheme(t), access, true)
			if err != nil {
				t.Fatal(err)
			}
			// A later immutable request reuses the SQL identity, not its old privileges.
			access.Name = "later-access"
			access.Spec.AccessLevel = level
			role, err := CreateDatabaseRole(testScheme(t), access, true)
			if err != nil {
				t.Fatal(err)
			}
			if role.Name != admin.Name || role.Spec.Name != admin.Spec.Name {
				t.Fatal("downgrading must retain the personal SQL identity")
			}
			if slices.Contains(role.Spec.InRoles, rccnpg.OwnerRole) {
				t.Fatal("a lower access level must not retain database owner membership")
			}
		})
	}
}
