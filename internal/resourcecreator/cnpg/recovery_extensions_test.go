package cnpg

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
)

func TestValidateRecoverySourceExtensions(t *testing.T) {
	present := func(name string) cnpgv1.ExtensionSpec {
		return cnpgv1.ExtensionSpec{DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: name, Ensure: cnpgv1.EnsurePresent}}
	}
	absent := func(name string) cnpgv1.ExtensionSpec {
		return cnpgv1.ExtensionSpec{DatabaseObjectSpec: cnpgv1.DatabaseObjectSpec{Name: name, Ensure: cnpgv1.EnsureAbsent}}
	}
	for _, tt := range []struct {
		name              string
		requested         []string
		database          []cnpgv1.ExtensionSpec
		images, libraries []string
		wantError         bool
	}{
		{name: "settled without extensions"},
		{name: "settled image and SQL extensions", requested: []string{"timescaledb", "pg_trgm"}, database: []cnpgv1.ExtensionSpec{present("timescaledb"), present("pg_trgm")}, images: []string{"timescaledb-oss"}, libraries: []string{"timescaledb"}},
		{name: "legacy SQL name normalized", requested: []string{"timescaledb-oss"}, database: []cnpgv1.ExtensionSpec{present("timescaledb")}, images: []string{"timescaledb-oss"}, libraries: []string{"timescaledb"}},
		{name: "database has not observed removal", database: []cnpgv1.ExtensionSpec{present("timescaledb")}, images: []string{"timescaledb-oss"}, libraries: []string{"timescaledb"}, wantError: true},
		{name: "blocked SQL drop", database: []cnpgv1.ExtensionSpec{absent("timescaledb")}, images: []string{"timescaledb-oss"}, libraries: []string{"timescaledb"}, wantError: true},
		{name: "old image still retained", images: []string{"timescaledb-oss"}, wantError: true},
		{name: "old preload still retained", libraries: []string{"timescaledb"}, wantError: true},
		{name: "missing required image", requested: []string{"timescaledb"}, database: []cnpgv1.ExtensionSpec{present("timescaledb")}, libraries: []string{"timescaledb"}, wantError: true},
		{name: "database has not observed addition", requested: []string{"timescaledb"}, images: []string{"timescaledb-oss"}, libraries: []string{"timescaledb"}, wantError: true},
		{name: "ordering is not significant", requested: []string{"postgis", "timescaledb"}, database: []cnpgv1.ExtensionSpec{present("timescaledb"), present("postgis")}, images: []string{"timescaledb-oss", "postgis"}, libraries: []string{"timescaledb"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requested := make([]v1.PostgresExtension, 0, len(tt.requested))
			for _, name := range tt.requested {
				requested = append(requested, v1.PostgresExtension{Name: name})
			}
			database := &cnpgv1.Database{Spec: cnpgv1.DatabaseSpec{Extensions: tt.database}}
			cluster := &cnpgv1.Cluster{}
			for _, name := range tt.images {
				cluster.Spec.PostgresConfiguration.Extensions = append(cluster.Spec.PostgresConfiguration.Extensions, cnpgv1.ExtensionConfiguration{Name: name})
			}
			cluster.Spec.PostgresConfiguration.AdditionalLibraries = tt.libraries
			if err := ValidateRecoverySourceExtensions(requested, database, cluster); (err != nil) != tt.wantError {
				t.Fatalf("validation = %v, want error %t", err, tt.wantError)
			}
		})
	}
}
