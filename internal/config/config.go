package config

import (
	"github.com/sethvargo/go-envconfig"
	"golang.org/x/net/context"
)

type Config struct {
	MetricsCertPath string `env:"METRICS_CERT_PATH"`
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
