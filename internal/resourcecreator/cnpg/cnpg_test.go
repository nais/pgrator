package cnpg

import (
	"strings"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

func TestCreateScheduledBackupStartsInitialBackup(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Postgres scheme: %v", err)
	}

	backup, err := CreateScheduledBackup(scheme, &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{Name: "mydb", Namespace: "myteam"},
	})
	if err != nil {
		t.Fatalf("CreateScheduledBackup() error = %v", err)
	}

	if backup.Spec.Immediate == nil || !*backup.Spec.Immediate {
		t.Errorf("ScheduledBackup.Spec.Immediate = %v, want true", backup.Spec.Immediate)
	}
}
