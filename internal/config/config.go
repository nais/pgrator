package config

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	MetricsCertPath string `env:"METRICS_CERT_PATH"`

	GoogleProjectID string `env:"GOOGLE_PROJECT_ID"`

	PostgresStorageClass string `env:"POSTGRES_STORAGE_CLASS"`
	PostgresImage        string `env:"POSTGRES_IMAGE"`

	DryRun                  bool   `env:"DRY_RUN"`
	LeaderElectionEnabled   bool   `env:"LEADER_ELECTION_ENABLED"`
	PrometheusRulesDisabled bool   `env:"PROMETHEUS_RULES_DISABLED"`
	ResyncIAMPermissions    bool   `env:"RESYNC_IAM_PERMISSIONS"`
	WalGsBucket             string `env:"WAL_GS_BUCKET"`

	Aiven  Aiven
	Tenant Tenant
}

type Aiven struct {
	Project                      string `env:"AIVEN_PROJECT, required"`
	ProjectVPCID                 string `env:"AIVEN_PROJECT_VPC_ID, required"`
	MetricsDestinationEndpointID string `env:"AIVEN_METRICS_DESTINATION_ENDPOINT_ID, required"`
}

type Tenant struct {
	Name string `env:"TENANT_NAME, required"`
	// TODO: GoogleProjectID should be in here as well
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
