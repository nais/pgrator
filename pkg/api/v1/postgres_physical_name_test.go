package v1

import (
	"strings"
	"testing"
)

func TestPostgresPhysicalNamePreservesMainIdentity(t *testing.T) {
	for _, name := range []string{"orders", "a-b", "orders.api", strings.Repeat("a", 63), strings.Repeat("a", 64), strings.Repeat("a", 253)} {
		t.Run(name, func(t *testing.T) {
			if got, want := PostgresPhysicalName(name), PostgresBranchObjectName(name, "main"); got != want {
				t.Errorf("physical name = %q, want existing main identity %q", got, want)
			}
		})
	}
}
