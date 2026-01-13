package resourcecreator

import (
	"maps"

	"github.com/nais/pgrator/pkg/api/annotation"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateObjectMeta(postgres *data_nais_io_v1.Postgres) metav1.ObjectMeta {
	labels := map[string]string{}

	maps.Copy(labels, postgres.GetLabels())

	labels["postgres.data.nais.io/name"] = postgres.GetName()

	return metav1.ObjectMeta{
		Name:      postgres.GetName(),
		Namespace: postgres.GetNamespace(),
		Labels:    labels,
		Annotations: map[string]string{
			annotation.DeploymentCorrelationIDAnnotation: postgres.GetCorrelationId(),
		},
	}
}
