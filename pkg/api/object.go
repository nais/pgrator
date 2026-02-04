package api

import "sigs.k8s.io/controller-runtime/pkg/client"

type NaisObject interface {
	client.Object
	GetStatus() Status
	GetCorrelationId() string
}
