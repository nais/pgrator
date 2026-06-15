package config

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	MetricsCertPath string `env:"METRICS_CERT_PATH" yaml:"metricsCertPath"`

	GoogleProjectID string `env:"GOOGLE_PROJECT_ID" yaml:"googleProjectID"`

	PostgresStorageClass string `env:"POSTGRES_STORAGE_CLASS" yaml:"postgresStorageClass"`
	PostgresImage        string `env:"POSTGRES_IMAGE" yaml:"postgresImage"`

	DryRun                  bool   `env:"DRY_RUN" yaml:"dryRun"`
	LeaderElectionEnabled   bool   `env:"LEADER_ELECTION_ENABLED" yaml:"leaderElectionEnabled"`
	PrometheusRulesDisabled bool   `env:"PROMETHEUS_RULES_DISABLED" yaml:"prometheusRulesDisabled"`
	ResyncIAMPermissions    bool   `env:"RESYNC_IAM_PERMISSIONS" yaml:"resyncIAMPermissions"`
	WalGsBucket             string `env:"WAL_GS_BUCKET" yaml:"walGsBucket"`

	Aiven  Aiven  `yaml:"aiven"`
	Tenant Tenant `yaml:"tenant"`
	CNPG   CNPG   `yaml:"cnpg"`
}

type Aiven struct {
	Project                      string `env:"AIVEN_PROJECT, required" yaml:"project"`
	ProjectVPCID                 string `env:"AIVEN_PROJECT_VPC_ID, required" yaml:"projectVPCID"`
	MetricsDestinationEndpointID string `env:"AIVEN_METRICS_DESTINATION_ENDPOINT_ID, required" yaml:"metricsDestinationEndpointID"`
}

type Tenant struct {
	Name string `env:"TENANT_NAME, required" yaml:"name"`
	// TODO: GoogleProjectID should be in here as well
}

type CNPG struct {
	// ImageCatalogName is the name of the ClusterImageCatalog for CNPG version selection.
	ImageCatalogName string `env:"CNPG_IMAGE_CATALOG_NAME, default=postgresql" yaml:"imageCatalogName"`
	// StorageClass is the storage class for CNPG persistent volumes.
	StorageClass string `env:"CNPG_STORAGE_CLASS" yaml:"storageClass"`
	// WalBucketPrefix is the prefix used for GCS buckets for barman-cloud WAL storage.
	WalBucketPrefix string `env:"CNPG_WAL_BUCKET_PREFIX" yaml:"walBucketPrefix"`
	// WalBucketNamespace is the namespace for creating wal storage buckets
	WalBucketNamespace string `env:"CNPG_WAL_BUCKET_NAMESPACE" yaml:"walBucketnamespace"`
}

func NewConfig(ctx context.Context, lookuper envconfig.Lookuper) (*Config, error) {
	cfg := &Config{}
	err := envconfig.ProcessWith(ctx, &envconfig.Config{
		Target:   cfg,
		Lookuper: lookuper,
	})
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

func (f *Config) Log(logger logr.Logger) {
	val := reflect.ValueOf(*f)
	typeOfStruct := val.Type()

	for i := 0; i < val.NumField(); i++ {
		logger.Info(fmt.Sprintf("%s: %v", typeOfStruct.Field(i).Name, val.Field(i).Interface()))
	}
}
