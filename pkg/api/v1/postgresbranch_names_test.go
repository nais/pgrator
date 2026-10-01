package v1_test

import (
	"strings"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
)

func TestPostgresBranchObjectName(t *testing.T) {
	for _, tt := range []struct{ postgres, branch string }{
		{"a-b", "c"}, {"a", "b-c"}, {strings.Repeat("p", 39), strings.Repeat("b", 63)}, {"orders", v1.DefaultBranchName},
	} {
		name := v1.PostgresBranchObjectName(tt.postgres, tt.branch)
		if name != v1.PostgresBranchObjectName(tt.postgres, tt.branch) {
			t.Errorf("name for (%q, %q) is not deterministic", tt.postgres, tt.branch)
		}
		if len(name) > 39 || len(name) == 0 {
			t.Errorf("name %q has length %d, want 1..39", name, len(name))
		}
	}
	if a, b := v1.PostgresBranchObjectName("a-b", "c"), v1.PostgresBranchObjectName("a", "b-c"); a == b {
		t.Errorf("ambiguous pairs collide: %q", a)
	}
}
