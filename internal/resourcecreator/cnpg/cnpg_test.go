package cnpg

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
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

func TestCreatePoolerLabelsPodsWithPostgresName(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	pooler, err := CreatePooler(scheme, &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := pooler.Spec.Template.ObjectMeta.Labels[nameLabel]; got != "orders" {
		t.Errorf("pooler pod Postgres label = %q, want orders", got)
	}
	if got := pooler.Spec.Template.ObjectMeta.Labels["apiserver-access"]; got != "enabled" {
		t.Errorf("pooler pod API server access = %q, want enabled", got)
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
	}, nil)
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	if cluster.Spec.Bootstrap.Recovery == nil || cluster.Spec.Bootstrap.InitDB != nil {
		t.Fatal("Cluster bootstrap did not use recovery")
	}
	if PersonalAccessRole("orders-restore") == PersonalAccessRole("orders-primary") {
		t.Fatal("recovery instance reused source authentication group")
	}
	wantHBA := "hostssl all +" + PersonalAccessRole("orders-restore") + " all scram-sha-256"
	if !slices.Contains(cluster.Spec.PostgresConfiguration.PgHBA, wantHBA) {
		t.Errorf("recovered cluster pg_hba = %v, want %q", cluster.Spec.PostgresConfiguration.PgHBA, wantHBA)
	}
	if slices.Contains(cluster.Spec.PostgresConfiguration.PgHBA, "hostssl all +"+PersonalAccessRole("orders-primary")+" all scram-sha-256") {
		t.Fatal("recovered cluster accepts source authentication group")
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

func TestRecoveryAuthenticationGroupChangesWhenNameIsReused(t *testing.T) {
	pg := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{UID: "first-incarnation"}, Spec: v1.PostgresSpec{Recovery: &v1.PostgresRecovery{}}}
	first := PersonalAccessRoleForPostgres("reused-name", pg)
	pg.UID = "second-incarnation"
	second := PersonalAccessRoleForPostgres("reused-name", pg)
	if first == second || first == PersonalAccessRole("reused-name") || second == PersonalAccessRole("reused-name") {
		t.Fatal("a recovered database could reuse an ancestral authentication group")
	}
	pg.Spec.Recovery = nil
	if got := PersonalAccessRoleForPostgres("reused-name", pg); got != PersonalAccessRole("reused-name") {
		t.Fatal("changed the authentication identity of an existing ordinary database")
	}
}

func TestCreateClusterRecoveryBootstrapPreservesFractionalSeconds(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Postgres scheme: %v", err)
	}
	targetTime := metav1.NewTime(time.Date(2026, time.September, 9, 13, 10, 0, 123456789, time.UTC))

	cluster, err := CreateCluster(scheme, &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-restore", Namespace: "team"},
		Spec: v1.PostgresSpec{
			MajorVersion: "18",
		},
	}, &config.Config{}, WALArchive{BucketName: "orders-restore-archive"}, &RecoverySource{
		BucketName: "orders-primary-archive",
		ServerName: "pg-orders-primary",
		TargetTime: targetTime,
	}, nil)
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}

	got := cluster.Spec.Bootstrap.Recovery.RecoveryTarget.TargetTime
	want := targetTime.Format(time.RFC3339Nano)
	if got != want {
		t.Errorf("recovery target time = %q, want %q", got, want)
	}
}

func TestRecoveryBootstrapPreservesInstantInUTC(t *testing.T) {
	for _, offset := range []int{-5 * 3600, 2 * 3600} {
		t.Run((time.Duration(offset) * time.Second).String(), func(t *testing.T) {
			target := metav1.NewTime(time.Date(2026, time.October, 15, 19, 53, 0, 123456789, time.UTC).In(time.FixedZone("local", offset)))
			recovery := &RecoverySource{TargetTime: target}
			got := bootstrap(recovery).Recovery.RecoveryTarget.TargetTime
			if want := "2026-10-15T19:53:00.123456789Z"; got != want {
				t.Fatalf("target = %q, want %q", got, want)
			}
			if recovery.TargetTime != target {
				t.Fatal("bootstrap changed the original target")
			}
		})
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

func TestReadWriteCreateRoleHasExplicitCreatePrivilege(t *testing.T) {
	assertSQLContains(t, postInitSQL(), "CREATE ROLE app_readwritecreate NOLOGIN")
	assertSQLContains(t, postInitApplicationSQL(), "GRANT CREATE ON SCHEMA public TO app_readwritecreate;")
}

func assertSQLContains(t *testing.T, statements []string, want string) {
	t.Helper()
	if slices.Contains(statements, want) {
		return
	}
	t.Errorf("SQL statements = %#v, want %q", statements, want)
}

func TestCreateDatabaseDeclaresApplicationDatabaseSafely(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Postgres scheme: %v", err)
	}
	postgres := &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{Name: "mydb-main-6bc4781b", Namespace: "myteam"},
		Spec:       v1.PostgresSpec{Extensions: []v1.PostgresExtension{{Name: "postgis"}, {Name: "pg_trgm"}}},
	}

	database, err := CreateDatabase(scheme, postgres, nil)
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}

	spec := database.Spec
	if spec.Name != "app" || spec.Owner != "app" || spec.ClusterRef.Name != ClusterName(postgres) {
		t.Errorf("Database identity = %q/%q on %q, want app/app on %q", spec.Name, spec.Owner, spec.ClusterRef.Name, ClusterName(postgres))
	}
	if spec.ReclaimPolicy != "retain" || spec.Ensure != "present" {
		t.Errorf("Database policy = reclaim %q ensure %q, want retain/present", spec.ReclaimPolicy, spec.Ensure)
	}
	if len(spec.Extensions) != 2 || spec.Extensions[0].Name != "postgis" || spec.Extensions[1].Name != "pg_trgm" || spec.Extensions[1].Ensure != "present" {
		t.Errorf("Database extensions = %+v, want postgis and pg_trgm present", spec.Extensions)
	}
}

func TestCreateDatabaseWithoutRequestedExtensionsDeclaresNone(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Postgres scheme: %v", err)
	}

	database, err := CreateDatabase(scheme, &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "mydb", Namespace: "myteam"}}, nil)
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	if len(database.Spec.Extensions) != 0 {
		t.Errorf("Database extensions = %+v, want none", database.Spec.Extensions)
	}
}

func TestExtensionsAreMountedPreloadedAndActivated(t *testing.T) {
	for _, tt := range []struct {
		name          string
		extensions    []string
		wantMounted   []string
		wantActivated []string
		wantPreload   []string
	}{
		{
			name:          "SQL names",
			extensions:    []string{"postgis", "pg_trgm", "timescaledb"},
			wantMounted:   []string{"postgis", "timescaledb-oss"},
			wantActivated: []string{"postgis", "pg_trgm", "timescaledb"},
			wantPreload:   []string{"timescaledb"},
		},
		{
			name:          "legacy timescaledb-oss name",
			extensions:    []string{"timescaledb-oss"},
			wantMounted:   []string{"timescaledb-oss"},
			wantActivated: []string{"timescaledb"},
			wantPreload:   []string{"timescaledb"},
		},
		{
			name:          "legacy and SQL name together",
			extensions:    []string{"timescaledb-oss", "timescaledb"},
			wantMounted:   []string{"timescaledb-oss"},
			wantActivated: []string{"timescaledb"},
			wantPreload:   []string{"timescaledb"},
		},
		{
			name:          "bundled extensions only",
			extensions:    []string{"pg_trgm", "citext"},
			wantMounted:   []string{},
			wantActivated: []string{"pg_trgm", "citext"},
			wantPreload:   []string{},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			extensions := make([]v1.PostgresExtension, 0, len(tt.extensions))
			for _, name := range tt.extensions {
				extensions = append(extensions, v1.PostgresExtension{Name: name})
			}
			cluster := clusterExtensions(extensions, nil)

			mountedExtensions := makeExtensions(cluster)
			mounted := make([]string, 0, len(mountedExtensions))
			for _, e := range mountedExtensions {
				mounted = append(mounted, e.Name)
			}
			if !slices.Equal(mounted, tt.wantMounted) {
				t.Errorf("mounted extensions = %v, want %v", mounted, tt.wantMounted)
			}
			databaseExtensions := makeDatabaseExtensions(extensions, nil)
			activated := make([]string, 0, len(databaseExtensions))
			for _, e := range databaseExtensions {
				activated = append(activated, e.Name)
			}
			if !slices.Equal(activated, tt.wantActivated) {
				t.Errorf("activated extensions = %v, want %v", activated, tt.wantActivated)
			}
			if preload := makePreloadLibraries(cluster); !slices.Equal(preload, tt.wantPreload) {
				t.Errorf("preload libraries = %v, want %v", preload, tt.wantPreload)
			}
		})
	}
}

func TestPersistedLegacyExtensionNameKeepsInstalledExtension(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Postgres scheme: %v", err)
	}
	postgres := &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{Name: "mydb-main-6bc4781b", Namespace: "myteam"},
		Spec: v1.PostgresSpec{MajorVersion: "18", Extensions: []v1.PostgresExtension{
			{Name: "postgis"}, {Name: "timescaledb-oss"},
		}},
	}
	present := func(name string) cnpgv1.ExtensionSpec {
		return cnpgv1.ExtensionSpec{DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: name, Ensure: cnpgv1.EnsurePresent}}
	}
	existing := &cnpgv1.Database{
		ObjectMeta: metav1.ObjectMeta{Generation: 1},
		Spec:       cnpgv1.DatabaseSpec{Extensions: []cnpgv1.ExtensionSpec{present("postgis"), present("timescaledb")}},
		Status: cnpgv1.DatabaseStatus{ObservedGeneration: 1, Applied: new(true), Extensions: []cnpgv1.DatabaseObjectStatus{
			{Name: "postgis", Applied: true}, {Name: "timescaledb", Applied: true},
		}},
	}

	database, err := CreateDatabase(scheme, postgres, existing)
	if err != nil {
		t.Fatalf("CreateDatabase() error = %v", err)
	}
	if want := []cnpgv1.ExtensionSpec{present("postgis"), present("timescaledb")}; !slices.Equal(database.Spec.Extensions, want) {
		t.Errorf("Database extensions = %+v, want %+v", database.Spec.Extensions, want)
	}

	cluster, err := CreateCluster(scheme, postgres, &config.Config{}, WALArchive{}, nil, database)
	if err != nil {
		t.Fatalf("CreateCluster() error = %v", err)
	}
	mounted := make([]string, 0, len(cluster.Spec.PostgresConfiguration.Extensions))
	for _, e := range cluster.Spec.PostgresConfiguration.Extensions {
		mounted = append(mounted, e.Name)
	}
	if want := []string{"postgis", "timescaledb-oss"}; !slices.Equal(mounted, want) {
		t.Errorf("mounted extensions = %v, want %v", mounted, want)
	}
	if preload, want := cluster.Spec.PostgresConfiguration.AdditionalLibraries, []string{"timescaledb"}; !slices.Equal(preload, want) {
		t.Errorf("preload libraries = %v, want %v", preload, want)
	}
}

func TestRemovedExtensionIsDroppedBeforeImageAndPreloadAreRemoved(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("adding Postgres scheme: %v", err)
	}
	postgres := &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{Name: "mydb-main-6bc4781b", Namespace: "myteam"},
		Spec:       v1.PostgresSpec{MajorVersion: "18", Extensions: []v1.PostgresExtension{{Name: "postgis"}}},
	}
	present := func(name string) cnpgv1.ExtensionSpec {
		return cnpgv1.ExtensionSpec{DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: name, Ensure: cnpgv1.EnsurePresent}}
	}
	absent := func(name string) cnpgv1.ExtensionSpec {
		return cnpgv1.ExtensionSpec{DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: name, Ensure: cnpgv1.EnsureAbsent}}
	}

	for _, tt := range []struct {
		name         string
		existing     *cnpgv1.Database
		wantDatabase []cnpgv1.ExtensionSpec
		wantMounted  []string
		wantPreload  []string
	}{
		{
			name: "removed entry is dropped while image and library are retained",
			existing: &cnpgv1.Database{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec:       cnpgv1.DatabaseSpec{Extensions: []cnpgv1.ExtensionSpec{present("postgis"), present("timescaledb")}},
				Status: cnpgv1.DatabaseStatus{ObservedGeneration: 1, Applied: new(true), Extensions: []cnpgv1.DatabaseObjectStatus{
					{Name: "postgis", Applied: true}, {Name: "timescaledb", Applied: true},
				}},
			},
			wantDatabase: []cnpgv1.ExtensionSpec{present("postgis"), absent("timescaledb")},
			wantMounted:  []string{"postgis", "timescaledb-oss"},
			wantPreload:  []string{"timescaledb"},
		},
		{
			name: "drop not yet applied keeps image and library",
			existing: &cnpgv1.Database{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       cnpgv1.DatabaseSpec{Extensions: []cnpgv1.ExtensionSpec{present("postgis"), absent("timescaledb")}},
				Status: cnpgv1.DatabaseStatus{ObservedGeneration: 1, Applied: new(true), Extensions: []cnpgv1.DatabaseObjectStatus{
					{Name: "postgis", Applied: true}, {Name: "timescaledb", Applied: true},
				}},
			},
			wantDatabase: []cnpgv1.ExtensionSpec{present("postgis"), absent("timescaledb")},
			wantMounted:  []string{"postgis", "timescaledb-oss"},
			wantPreload:  []string{"timescaledb"},
		},
		{
			name: "failed drop keeps image and library",
			existing: &cnpgv1.Database{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       cnpgv1.DatabaseSpec{Extensions: []cnpgv1.ExtensionSpec{present("postgis"), absent("timescaledb")}},
				Status: cnpgv1.DatabaseStatus{ObservedGeneration: 1, Applied: new(false), Extensions: []cnpgv1.DatabaseObjectStatus{
					{Name: "postgis", Applied: true}, {Name: "timescaledb", Applied: false, Message: "cannot drop extension"},
				}},
			},
			wantDatabase: []cnpgv1.ExtensionSpec{present("postgis"), absent("timescaledb")},
			wantMounted:  []string{"postgis", "timescaledb-oss"},
			wantPreload:  []string{"timescaledb"},
		},
		{
			name: "confirmed drop removes entry, image and library",
			existing: &cnpgv1.Database{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       cnpgv1.DatabaseSpec{Extensions: []cnpgv1.ExtensionSpec{present("postgis"), absent("timescaledb")}},
				Status: cnpgv1.DatabaseStatus{ObservedGeneration: 2, Applied: new(true), Extensions: []cnpgv1.DatabaseObjectStatus{
					{Name: "postgis", Applied: true}, {Name: "timescaledb", Applied: true},
				}},
			},
			wantDatabase: []cnpgv1.ExtensionSpec{present("postgis")},
			wantMounted:  []string{"postgis"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			database, err := CreateDatabase(scheme, postgres, tt.existing)
			if err != nil {
				t.Fatalf("CreateDatabase() error = %v", err)
			}
			if !slices.Equal(database.Spec.Extensions, tt.wantDatabase) {
				t.Errorf("Database extensions = %+v, want %+v", database.Spec.Extensions, tt.wantDatabase)
			}

			cluster, err := CreateCluster(scheme, postgres, &config.Config{}, WALArchive{}, nil, database)
			if err != nil {
				t.Fatalf("CreateCluster() error = %v", err)
			}
			mounted := make([]string, 0, len(cluster.Spec.PostgresConfiguration.Extensions))
			for _, e := range cluster.Spec.PostgresConfiguration.Extensions {
				mounted = append(mounted, e.Name)
			}
			if !slices.Equal(mounted, tt.wantMounted) {
				t.Errorf("mounted extensions = %v, want %v", mounted, tt.wantMounted)
			}
			if preload := cluster.Spec.PostgresConfiguration.AdditionalLibraries; !slices.Equal(preload, tt.wantPreload) {
				t.Errorf("preload libraries = %v, want %v", preload, tt.wantPreload)
			}
		})
	}
}
