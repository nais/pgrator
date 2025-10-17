package synchronizer

import (
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type NaisObject interface {
	client.Object
	GetStatus() *nais_io_v1.Status
	SetStatus(status *nais_io_v1.Status)
}
