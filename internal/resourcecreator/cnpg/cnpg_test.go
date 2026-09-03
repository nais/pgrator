package cnpg

import (
	"strings"
	"testing"
)

func TestClusterNameFor(t *testing.T) {
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
			name:     "shortens long postgres name",
			postgres: strings.Repeat("database", 8),
			want:     "pg-databasedatabasedatabasedatabasedataba-bdf7c82f",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClusterNameFor(tt.postgres); got != tt.want {
				t.Errorf("ClusterNameFor(%q) = %q, want %q", tt.postgres, got, tt.want)
			}
		})
	}
}

func TestPoolerNameFor(t *testing.T) {
	if got, want := PoolerNameFor("mydb"), "pg-mydb-pooler"; got != want {
		t.Errorf("PoolerNameFor() = %q, want %q", got, want)
	}
}
