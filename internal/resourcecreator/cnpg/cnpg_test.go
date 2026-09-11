package cnpg

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nais/pgrator/internal/config"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	"k8s.io/apimachinery/pkg/api/resource"
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

func TestCreateClusterRecoveryBootstrap(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Postgres scheme: %v", err)
	}
	targetTime := metav1.NewTime(time.Date(2026, time.September, 9, 13, 10, 0, 0, time.UTC))

	cluster, err := CreateCluster(scheme, &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-restore", Namespace: "team"},
		Spec: v1.PostgresSpec{
			MajorVersion: "18",
		},
	}, &config.Config{}, WALArchive{BucketName: "orders-restore-archive"}, &RecoverySource{
		BucketName: "orders-primary-archive",
		ServerName: "pg-orders-primary",
		TargetTime: targetTime,
	})
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	if cluster.Spec.Bootstrap.Recovery == nil || cluster.Spec.Bootstrap.InitDB != nil {
		t.Fatal("Cluster bootstrap did not use recovery")
	}
	if got, want := cluster.Spec.Bootstrap.Recovery.RecoveryTarget.TargetTime, "2026-09-09T13:10:00Z"; got != want {
		t.Errorf("recovery target time = %q, want %q", got, want)
	}
	if got, want := cluster.Spec.ExternalClusters[0].PluginConfiguration.Parameters, map[string]string{
		"barmanObjectName": "orders-primary-archive",
		"serverName":       "pg-orders-primary",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("recovery plugin parameters = %#v, want %#v", got, want)
	}
}

func TestAuditConfigurationExemptsApplicationRoles(t *testing.T) {
	parameters := makePostgresParameters(resource.MustParse("512Mi"))
	if got, want := parameters["pgaudit.log"], "read,write,ddl,role"; got != want {
		t.Errorf("pgaudit.log = %q, want %q", got, want)
	}

	assertSQLContains(t, postInitSQL(), "ALTER ROLE postgres SET pgaudit.log = 'none'")
	assertSQLContains(t, postInitApplicationSQL(), "ALTER ROLE app SET pgaudit.log = 'none'")
}

func assertSQLContains(t *testing.T, statements []string, want string) {
	t.Helper()
	for _, statement := range statements {
		if statement == want {
			return
		}
	}
	t.Errorf("SQL statements = %#v, want %q", statements, want)
}
