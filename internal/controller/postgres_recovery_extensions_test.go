package controller

import (
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestRecoveryRejectsSourceWithPendingExtensionRemoval(t *testing.T) {
	r, c, source, target := directRecoveryFixture(t)
	// The public Postgres spec no longer requests TimescaleDB, but dependent
	// SQL objects have blocked DROP EXTENSION. The source correctly keeps it.
	database := &cnpgv1.Database{}
	requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: source.Namespace, Name: source.Status.ClusterName + "-app"}, database))
	database.Spec.Extensions = []cnpgv1.ExtensionSpec{{DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: "timescaledb", Ensure: cnpgv1.EnsureAbsent}}}
	database.Status.Applied = new(false)
	database.Status.Extensions = []cnpgv1.DatabaseObjectStatus{{Name: "timescaledb", Applied: false, Message: "dependent objects still exist"}}
	requireNoError(t, c.Update(t.Context(), database))
	cluster := &cnpgv1.Cluster{}
	requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: source.Namespace, Name: source.Status.ClusterName}, cluster))
	cluster.Spec.PostgresConfiguration.Extensions = []cnpgv1.ExtensionConfiguration{{Name: "timescaledb-oss"}}
	cluster.Spec.PostgresConfiguration.AdditionalLibraries = []string{"timescaledb"}
	requireNoError(t, c.Update(t.Context(), cluster))
	if _, _, err := r.Prepare(t.Context(), c, target); err == nil || !strings.Contains(err.Error(), "extension") {
		t.Fatalf("recovery must reject a source whose archived data still needs removed extensions: %v", err)
	}
	// Once SQL reconciliation has removed the extension and the retained image
	// and preload library are gone, the same request may proceed.
	database.Spec.Extensions = nil
	database.Status.Applied = new(true)
	requireNoError(t, c.Update(t.Context(), database))
	cluster.Spec.PostgresConfiguration.Extensions = nil
	cluster.Spec.PostgresConfiguration.AdditionalLibraries = nil
	requireNoError(t, c.Update(t.Context(), cluster))
	_, _, err := r.Prepare(t.Context(), c, target)
	requireNoError(t, err)
}

func TestInProgressRecoveryIgnoresLaterSourceExtensionRemoval(t *testing.T) {
	r, c, source, target := directRecoveryFixture(t)
	requireNoError(t, c.Create(t.Context(), recoveryClusterForPostgres(t, r, c, target)))
	database := &cnpgv1.Database{}
	requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: source.Namespace, Name: source.Status.ClusterName + "-app"}, database))
	database.Spec.Extensions = []cnpgv1.ExtensionSpec{{DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: "timescaledb", Ensure: cnpgv1.EnsureAbsent}}}
	database.Status.Applied = new(false)
	requireNoError(t, c.Update(t.Context(), database))
	// The recovery already has its immutable bootstrap configuration. A later
	// source extension change must not alter or interrupt that target.
	_, _, err := r.Prepare(t.Context(), c, target)
	requireNoError(t, err)
	if len(target.Spec.Extensions) != 0 {
		t.Fatal("source changes modified the recovery's configured extensions")
	}
}
