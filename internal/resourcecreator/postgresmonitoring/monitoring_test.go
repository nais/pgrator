package postgresmonitoring

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

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
	rule, err := Rule("team")
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
