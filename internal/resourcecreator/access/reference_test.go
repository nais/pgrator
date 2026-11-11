package access

import (
	"reflect"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestDirectReferencePreservesAccessResourceIdentity(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	for _, level := range []v1.PostgresAccessLevel{v1.PostgresAccessLevelRead, v1.PostgresAccessLevelReadWrite, v1.PostgresAccessLevelAdmin, v1.PostgresAccessLevelMaintain} {
		t.Run(string(level), func(t *testing.T) {
			legacy := &v1.PostgresAccess{ObjectMeta: metav1.ObjectMeta{Name: "existing-access", Namespace: "team", UID: "access-uid"}, Spec: v1.PostgresAccessSpec{
				PostgresBranch: v1.PostgresPhysicalName("orders"), Username: "user@example.com", AccessLevel: level,
			}}
			direct := legacy.DeepCopy()
			direct.Spec.Postgres, direct.Spec.PostgresBranch = "orders", ""
			oldRole, err := CreateDatabaseRole(scheme, legacy, true)
			if err != nil {
				t.Fatal(err)
			}
			newRole, err := CreateDatabaseRole(scheme, direct, true)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(oldRole, newRole) {
				t.Error("direct reference changed the role identity, cluster or memberships")
			}
			oldRelay, err := CreateRelayAccess(scheme, legacy, "digest")
			if err != nil {
				t.Fatal(err)
			}
			newRelay, err := CreateRelayAccess(scheme, direct, "digest")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(oldRelay, newRelay) {
				t.Error("direct reference changed immutable relay mapping")
			}
			oldPolicy, err := CreateRelayNetworkPolicy(scheme, legacy)
			if err != nil {
				t.Fatal(err)
			}
			newPolicy, err := CreateRelayNetworkPolicy(scheme, direct)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(oldPolicy, newPolicy) {
				t.Error("direct reference changed the permitted network target")
			}
		})
	}
}
