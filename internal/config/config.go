package config

import (
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/sethvargo/go-envconfig"
	"golang.org/x/net/context"
)

type Config struct {
	MetricsCertPath string `env:"METRICS_CERT_PATH"`

	GoogleProjectID string `env:"GOOGLE_PROJECT_ID"`

	PostgresStorageClass string `env:"POSTGRES_STORAGE_CLASS"`
	PostgresImage        string `env:"POSTGRES_IMAGE"`

	DryRun                  bool `env:"DRY_RUN"`
	PrometheusRulesDisabled bool `env:"PROMETHEUS_RULES_DISABLED"`
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
