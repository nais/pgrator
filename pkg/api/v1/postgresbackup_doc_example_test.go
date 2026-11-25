package v1_test

import (
	"slices"
	"testing"

	"github.com/nais/pgrator/pkg/api/internal/testutil"
	v1 "github.com/nais/pgrator/pkg/api/v1"
)

var ignoredPostgresBackupFields = []string{
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

func TestExamplePostgresBackupForDocumentation(t *testing.T) {
	backup := v1.ExamplePostgresBackupForDocumentation()
	keys := testutil.ZeroFields(backup)

	for _, key := range keys {
		if !slices.Contains(ignoredPostgresBackupFields, key) {
			t.Errorf("`%s` does not exist with a non-zero value in nais_io_v1.ExamplePostgresBackupForDocumentation", key)
		}
	}
}
