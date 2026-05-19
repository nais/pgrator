package resourcecreator

import (
	"fmt"

	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	monitoring_v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func MinimalCNPGPodMonitor(postgres *data_nais_io_v1.Postgres, pgClusterName, namespace string) *monitoring_v1.PodMonitor {
	objectMeta := CreateObjectMeta(postgres)
	objectMeta.Name = fmt.Sprintf("pg-%s", pgClusterName)
	objectMeta.Namespace = namespace

	return &monitoring_v1.PodMonitor{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PodMonitor",
			APIVersion: "monitoring.coreos.com/v1",
		},
		ObjectMeta: objectMeta,
	}
}

func CreateCNPGPodMonitor(postgres *data_nais_io_v1.Postgres, pgClusterName, namespace string) *monitoring_v1.PodMonitor {
	podMonitor := MinimalCNPGPodMonitor(postgres, pgClusterName, namespace)

	// Label with prometheus=tenant so the tenant Prometheus instance scrapes these
	// metrics, making them available to application developers (not only the platform team).
	if podMonitor.Labels == nil {
		podMonitor.Labels = make(map[string]string)
	}
	podMonitor.Labels["prometheus"] = "tenant"

	podMonitor.Spec = monitoring_v1.PodMonitorSpec{
		Selector: metav1.LabelSelector{
			MatchLabels: map[string]string{
				"cnpg.io/cluster": pgClusterName,
			},
		},
		PodMetricsEndpoints: []monitoring_v1.PodMetricsEndpoint{
			{
				Port: ptr.To("metrics"),
			},
		},
	}

	return podMonitor
}
