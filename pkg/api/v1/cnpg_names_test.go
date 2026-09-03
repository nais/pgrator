package v1_test

import (
	"strings"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
)

func TestCNPGClusterName(t *testing.T) {
	tests := []struct {
		name     string
		postgres string
		want     string
	}{
		{
			name:     "prefixes postgres name",
			postgres: "mydb",
			want:     "pg-mydb",
		},
		{
			name:     "shortens long name to CloudNativePG limit",
			postgres: strings.Repeat("database", 8),
			want:     "pg-databasedatabasedatabasedatabasedataba-bdf7c82f",
		},
		{
			name:     "converts DNS subdomain to DNS label",
			postgres: "reports.prod",
			want:     "pg-reports-prod-c1497b81",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := v1.CNPGClusterName(tt.postgres); got != tt.want {
				t.Errorf("CNPGClusterName(%q) = %q, want %q", tt.postgres, got, tt.want)
			}
		})
	}
}
