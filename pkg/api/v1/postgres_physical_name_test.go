package v1

import (
	"strings"
	"testing"
)

// Physical names are durable: existing clusters, roles and WAL archives are
// named after them, so these values must never change.
func TestPostgresPhysicalNameIsFrozen(t *testing.T) {
	for name, want := range map[string]string{
		"orders":                "orders-main-65648812",
		"mydb":                  "mydb-main-6bc4781b",
		"orders.api":            "orders-api-main-50aa6e18",
		"a-b":                   "a-b-main-36cc0407",
		strings.Repeat("a", 63): strings.Repeat("a", 30) + "-c7c080d5",
	} {
		got := PostgresPhysicalName(name)
		if got != want {
			t.Errorf("PostgresPhysicalName(%q) = %q, want %q", name, got, want)
		}
		if len(got) > 39 {
			t.Errorf("PostgresPhysicalName(%q) = %q has length %d, want <= 39", name, got, len(got))
		}
	}
}
