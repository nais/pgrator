package config

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/sethvargo/go-envconfig"
	"k8s.io/apimachinery/pkg/util/validation"
)

const uidLength = 36 // Length of a Kubernetes object UID

type Config struct {
	MetricsCertPath string `env:"METRICS_CERT_PATH" yaml:"metricsCertPath"`

	GoogleProjectID string `env:"GOOGLE_PROJECT_ID" yaml:"googleProjectID"`

	PostgresStorageClass string `env:"POSTGRES_STORAGE_CLASS" yaml:"postgresStorageClass"`
	PostgresImage        string `env:"POSTGRES_IMAGE" yaml:"postgresImage"`

	DryRun      bool `env:"DRY_RUN" yaml:"dryRun"`
	Development bool `env:"DEVELOPMENT" yaml:"development"`

	LeaderElectionEnabled   bool   `env:"LEADER_ELECTION_ENABLED" yaml:"leaderElectionEnabled"`
	PrometheusRulesDisabled bool   `env:"PROMETHEUS_RULES_DISABLED" yaml:"prometheusRulesDisabled"`
	ResyncIAMPermissions    bool   `env:"RESYNC_IAM_PERMISSIONS" yaml:"resyncIAMPermissions"`
	WalGsBucket             string `env:"WAL_GS_BUCKET" yaml:"walGsBucket"`

	Aiven  Aiven  `yaml:"aiven"`
	Tenant Tenant `yaml:"tenant"`
	CNPG   CNPG   `yaml:"cnpg"`
	Google Google `yaml:"google"`
}

type Google struct {
	// TODO: GoogleProjectID should be in here as well
	Location string `env:"GOOGLE_LOCATION" yaml:"location"`
}

type Aiven struct {
	Project                      string `env:"AIVEN_PROJECT, required" yaml:"project"`
	ProjectVPCID                 string `env:"AIVEN_PROJECT_VPC_ID, required" yaml:"projectVPCID"`
	MetricsDestinationEndpointID string `env:"AIVEN_METRICS_DESTINATION_ENDPOINT_ID, required" yaml:"metricsDestinationEndpointID"`
}

type Tenant struct {
	Name string `env:"TENANT_NAME, required" yaml:"name"`
}

type CNPG struct {
	// ImageCatalogName is the name of the ClusterImageCatalog for CNPG version selection.
	ImageCatalogName string `env:"CNPG_IMAGE_CATALOG_NAME, default=postgresql" yaml:"imageCatalogName"`
	// StorageClass is the storage class for CNPG persistent volumes.
	StorageClass string `env:"CNPG_STORAGE_CLASS" yaml:"storageClass"`
	// WalBucketPrefix is the prefix used for GCS buckets for barman-cloud WAL storage.
	WalBucketPrefix string `env:"CNPG_WAL_BUCKET_PREFIX" yaml:"walBucketPrefix"`
	// WalBucketNamespace is the namespace for creating wal storage buckets
	WalBucketNamespace string `env:"CNPG_WAL_BUCKET_NAMESPACE" yaml:"walBucketNamespace"`
	// WalBucketRole is the namespace for creating wal storage buckets
	WalBucketRole string `env:"CNPG_WAL_BUCKET_ROLE" yaml:"walBucketRole"`
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

	if err = validateWalBucketPrefixLength(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validateWalBucketPrefixLength makes sure that the configured prefix is not too long
// The prefix is combined with the UID from the postgres object to create the name of the WAL bucket.
// The WAL bucket name has a max length of 63 characters (DNS1035LabelMaxLength).
// See controller.PostgresReconciler::makeStorageBucketName
func validateWalBucketPrefixLength(cfg *Config) error {
	allowedPrefixLength := validation.DNS1035LabelMaxLength - uidLength
	prefixLength := len(cfg.CNPG.WalBucketPrefix)
	if prefixLength > allowedPrefixLength {
		return fmt.Errorf("WAL bucket prefix too long (%d), must be shorter than %d", prefixLength, allowedPrefixLength)
	}
	return nil
}

func (f *Config) Log(logger logr.Logger) {
	val := reflect.ValueOf(*f)
	typeOfStruct := val.Type()

	for i := 0; i < val.NumField(); i++ {
		logger.Info(fmt.Sprintf("%s: %v", typeOfStruct.Field(i).Name, val.Field(i).Interface()))
	}
}
