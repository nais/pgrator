package resourcecreator

import (
	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	nais_io_v1 "github.com/nais/liberator/pkg/apis/nais.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateObjectMeta(obj *data_nais_io_v1.Postgres) metav1.ObjectMeta {
	labels := map[string]string{}

	for k, v := range obj.GetLabels() {
		labels[k] = v
	}

	labels["postgres.data.nais.io/name"] = obj.GetName()

	return metav1.ObjectMeta{
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
		Labels:    labels,
		Annotations: map[string]string{
			nais_io_v1.DeploymentCorrelationIDAnnotation: obj.GetCorrelationId(),
		},
	}
}
