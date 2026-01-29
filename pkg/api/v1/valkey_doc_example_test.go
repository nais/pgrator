package v1_test

import (
	"slices"
	"testing"

	"github.com/nais/pgrator/internal/testutil"
	v1 "github.com/nais/pgrator/pkg/api/v1"
)

var ignoredValkeyFields = []string{
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

// Test that the example Valkey contains examples for all fields encountered.
// Examples MUST contain a non-zero value to be valid, so no empty strings, false booleans, or zero ints.
func TestExampleValkeyForDocumentation(t *testing.T) {
	valkey := v1.ExampleValkeyForDocumentation()
	keys := testutil.ZeroFields(valkey)

	for _, key := range keys {
		if !slices.Contains(ignoredValkeyFields, key) {
			t.Errorf("`%s` does not exist with a non-zero value in nais_io_v1.ExampleValkeyForDocumentation", key)
		}
	}
}
