package object

import (
	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NaisObject interface {
	client.Object
	GetStatus() *data_nais_io_v1.PostgresStatus
	GetCorrelationId() string
}

var _ NaisObject = &data_nais_io_v1.Postgres{}
