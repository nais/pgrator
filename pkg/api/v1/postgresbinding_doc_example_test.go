package v1_test

import (
	"slices"
	"testing"

	"github.com/nais/pgrator/pkg/api/internal/testutil"
	v1 "github.com/nais/pgrator/pkg/api/v1"
)

var ignoredPostgresBindingFields = []string{
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
	`.ObjectMeta.Labels`,
	`.ObjectMeta.ManagedFields`,
	`.ObjectMeta.OwnerReferences`,
	`.ObjectMeta.ResourceVersion`,
	`.ObjectMeta.SelfLink`,
	`.ObjectMeta.UID`,
	`.Status`,
}

// Test that the example PostgresBinding contains examples for all fields encountered.
// Examples MUST contain a non-zero value to be valid, so no empty strings, false booleans, or zero ints.
func TestExamplePostgresBindingForDocumentation(t *testing.T) {
	binding := v1.ExamplePostgresBindingForDocumentation()
	keys := testutil.ZeroFields(binding)

	for _, key := range keys {
		if !slices.Contains(ignoredPostgresBindingFields, key) {
			t.Errorf("`%s` does not exist with a non-zero value in nais_io_v1.ExamplePostgresBindingForDocumentation", key)
		}
	}
}
