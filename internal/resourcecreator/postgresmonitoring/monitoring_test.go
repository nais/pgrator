package postgresmonitoring

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// testDatabases: an initialized HA database, one still provisioning and a PITR.
func testDatabases() []v1.Postgres {
	created := metav1.NewTime(time.Unix(1, 0))
	orders := v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", CreationTimestamp: created},
		Spec:       v1.PostgresSpec{HighAvailability: true},
		Status:     &v1.PostgresStatus{Ownership: &v1.PostgresOwnership{ClusterUID: "cluster-uid"}},
	}
	orders.Status.SetCondition(metav1.Condition{Type: InitializedCondition, Status: metav1.ConditionTrue, Message: "cluster-uid", LastTransitionTime: metav1.NewTime(time.Unix(2, 0))})
	return []v1.Postgres{
		orders,
		{ObjectMeta: metav1.ObjectMeta{Name: "fresh", Namespace: "team", CreationTimestamp: created}},
		{ObjectMeta: metav1.ObjectMeta{Name: "restoring", Namespace: "team", CreationTimestamp: created}, Spec: v1.PostgresSpec{Recovery: &v1.PostgresRecovery{}}},
	}
}

func records(t *testing.T, databases ...v1.Postgres) map[string]string {
	t.Helper()
	rule, err := Rule("team", Options{BackupsEnabled: true}, databases...)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rule.Spec.Groups[0].Rules {
		got[r.Labels["postgres"]+"/"+r.Record] = r.Expr.String()
	}
	return got
}

func TestPostgresAlertInventory(t *testing.T) {
	databases := testDatabases()
	deleting := databases[1].DeepCopy()
	deleting.Name = "deleting"
	deleting.DeletionTimestamp = new(metav1.NewTime(time.Unix(3, 0)))
	got := records(t, append(databases, *deleting)...)
	for key, want := range map[string]string{
		"orders/nais_postgres_desired_instances":             "vector(3)",
		"fresh/nais_postgres_desired_instances":              "vector(2)",
		"orders/nais_postgres_initialized_timestamp_seconds": "vector(2)",
		"fresh/nais_postgres_initialized_timestamp_seconds":  "vector(0)",
		"restoring/nais_postgres_recovery_requested":         "vector(1)",
		"orders/nais_postgres_backup_enabled":                "vector(1)",
		"orders/nais_postgres_backup_retention_days":         "vector(2)",
	} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
	if _, ok := got["deleting/nais_postgres_desired_instances"]; ok {
		t.Error("a deleting database must not be in the inventory")
	}

	// A replaced CNPG cluster must not inherit the old cluster's milestone.
	databases[0].Status.Ownership.ClusterUID = "replacement"
	if got := records(t, databases[0]); got["orders/nais_postgres_initialized_timestamp_seconds"] != "vector(0)" {
		t.Errorf("replaced cluster inherited initialized milestone: %s", got["orders/nais_postgres_initialized_timestamp_seconds"])
	}
}

// Validates and evaluates the rendered rule with promtool. The ordinary test
// suite skips it without promtool; `mise run check:postgres-alerts` requires it.
func TestPostgresAlertRules(t *testing.T) {
	tool, err := exec.LookPath("promtool")
	if err != nil {
		if os.Getenv("PGRATOR_REQUIRE_PROMTOOL") == "1" {
			t.Fatal("promtool is required")
		}
		t.Skip("promtool not found; run mise run check:postgres-alerts")
	}
	rule, err := Rule("team", Options{BackupsEnabled: true}, testDatabases()...)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := yaml.Marshal(rule.Spec)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rules := filepath.Join(dir, "rules.yaml")
	if err := os.WriteFile(rules, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("testdata/alerts.test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	tests := filepath.Join(dir, "alerts.test.yaml")
	if err := os.WriteFile(tests, []byte(strings.ReplaceAll(string(fixture), "__RULE_FILE__", strconv.Quote(rules))), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"check", "rules", rules}, {"test", "rules", tests}} {
		if output, err := exec.CommandContext(t.Context(), tool, args...).CombinedOutput(); err != nil {
			t.Fatalf("promtool %v: %v\n%s", args, err, output)
		}
	}
}
