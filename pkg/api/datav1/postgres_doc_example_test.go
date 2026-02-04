package datav1_test

import (
	"slices"
	"testing"

	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	"github.com/nais/pgrator/pkg/api/internal/testutil"
)

var ignoredPostgresFields = []string{
	`.ObjectMeta.Annotations`,
	`.ObjectMeta.ClusterName`,
	`.ObjectMeta.CreationTimestamp`,
	`.ObjectMeta.CreationTimestamp.Time`,
	`.ObjectMeta.CreationTimestamp.Time.ext`,
	`.ObjectMeta.CreationTimestamp.Time.loc`,
	`.ObjectMeta.CreationTimestamp.Time.wall`,
	`.ObjectMeta.DeletionGracePeriodSeconds`,
	`.ObjectMeta.DeletionTimestamp`,
	`.ObjectMeta.Finalizers`,
	`.ObjectMeta.GenerateName`,
	`.ObjectMeta.Generation`,
	`.ObjectMeta.ManagedFields`,
	`.ObjectMeta.OwnerReferences`,
	`.ObjectMeta.ResourceVersion`,
	`.ObjectMeta.SelfLink`,
	`.ObjectMeta.UID`,
	`.Status`,
}

// Test that the example Postgres contains examples for all fields encountered.
// Examples MUST contain a non-zero value to be valid, so no empty strings, false booleans, or zero ints.
func TestExamplePostgresForDocumentation(t *testing.T) {
	postgres := data_nais_io_v1.ExamplePostgresForDocumentation()
	keys := testutil.ZeroFields(postgres)

	for _, key := range keys {
		if !slices.Contains(ignoredPostgresFields, key) {
			t.Errorf("`%s` does not exist with a non-zero value in data_nais_io_v1.ExamplePostgresForDocumentation", key)
		}
	}
}
