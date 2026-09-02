package opensearch

import (
	"fmt"
	"maps"
	"reflect"
	"strconv"

	"github.com/nais/pgrator/internal/config"
	aiven_v1alpha1 "github.com/nais/pgrator/internal/thirdparty/aiven/v1alpha1"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// ServiceName returns the namespaced Aiven service name for an OpenSearch instance.
// Format: opensearch-{teamSlug}-{instanceName}
func ServiceName(opensearch *v1.OpenSearch) string {
	return "opensearch-" + opensearch.GetNamespace() + "-" + opensearch.GetName()
}

// ObjectMeta creates a standard ObjectMeta for OpenSearch-owned resources
func ObjectMeta(opensearch *v1.OpenSearch) metav1.ObjectMeta {
	labels := map[string]string{}
	maps.Copy(labels, opensearch.GetLabels())

	labels["opensearch.nais.io/name"] = opensearch.GetName()

	var annotations map[string]string
	if opensearch.GetCorrelationId() != "" {
		annotations = map[string]string{
			api.DeploymentCorrelationIDAnnotation: opensearch.GetCorrelationId(),
		}
	}

	return metav1.ObjectMeta{
		Name:        ServiceName(opensearch),
		Namespace:   opensearch.GetNamespace(),
		Labels:      labels,
		Annotations: annotations,
	}
}

// Minimal creates a minimal Aiven OpenSearch object for use in delete operations
func Minimal(opensearch *v1.OpenSearch) *aiven_v1alpha1.OpenSearch {
	objectMeta := ObjectMeta(opensearch)

	return &aiven_v1alpha1.OpenSearch{
		TypeMeta: metav1.TypeMeta{
			Kind:       "OpenSearch",
			APIVersion: "aiven.io/v1alpha1",
		},
		ObjectMeta: objectMeta,
	}
}

// CreateSpec creates an Aiven OpenSearch resource from a nais.io OpenSearch spec
func CreateSpec(
	scheme *runtime.Scheme,
	opensearch *v1.OpenSearch,
	aiven config.Aiven,
	tenant config.Tenant,
) (*aiven_v1alpha1.OpenSearch, error) {
	aivenOpenSearch := Minimal(opensearch)

	plan, err := opensearch.AivenPlan()
	if err != nil {
		return nil, err
	}

	version, err := opensearch.Spec.Version.ToAivenString()
	if err != nil {
		return nil, err
	}

	userConfig := &aiven_v1alpha1.OpenSearchUserConfig{
		OpenSearchVersion: &version,
	}

	if osSettings := aivenOpenSearchSettings(opensearch); osSettings != nil {
		userConfig.OpenSearch = osSettings
	}

	aivenOpenSearch.Spec = aiven_v1alpha1.OpenSearchSpec{
		Project:               aiven.Project,
		Plan:                  plan,
		ProjectVPCID:          aiven.ProjectVPCID,
		DiskSpace:             strconv.Itoa(opensearch.Spec.StorageGB) + "GiB",
		TerminationProtection: new(true),
		Tags: map[string]string{
			"team":   opensearch.GetNamespace(),
			"app":    opensearch.GetName(),
			"tenant": tenant.Name,
		},
		UserConfig: userConfig,
	}

	err = controllerutil.SetControllerReference(opensearch, aivenOpenSearch, scheme)
	if err != nil {
		return nil, fmt.Errorf("setting controller reference: %w", err)
	}

	return aivenOpenSearch, nil
}

func aivenOpenSearchSettings(opensearch *v1.OpenSearch) *aiven_v1alpha1.OpenSearchSettings {
	settings := aiven_v1alpha1.OpenSearchSettings{}

	if opensearch.Spec.ShardIndexingPressure != nil {
		settings.ShardIndexingPressure = &aiven_v1alpha1.OpenSearchShardIndexingPressure{
			Enabled:  &opensearch.Spec.ShardIndexingPressure.Enabled,
			Enforced: &opensearch.Spec.ShardIndexingPressure.Enforced,
		}
	}

	if opensearch.Spec.Indices != nil {
		settings.IndicesQueryBoolMaxClauseCount = opensearch.Spec.Indices.QueryBoolMaxClauseCount
	}

	if opensearch.Spec.Http != nil && opensearch.Spec.Http.MaxContentLength != nil {
		bytes := int(opensearch.Spec.Http.MaxContentLength.Value())
		settings.HttpMaxContentLength = &bytes
	}

	if reflect.DeepEqual(settings, aiven_v1alpha1.OpenSearchSettings{}) {
		return nil
	}
	return &settings
}

// MinimalServiceIntegration creates a minimal ServiceIntegration object for use in delete operations
func MinimalServiceIntegration(opensearch *v1.OpenSearch) *aiven_v1alpha1.ServiceIntegration {
	objectMeta := ObjectMeta(opensearch)

	return &aiven_v1alpha1.ServiceIntegration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceIntegration",
			APIVersion: "aiven.io/v1alpha1",
		},
		ObjectMeta: objectMeta,
	}
}

// CreateServiceIntegrationSpec creates a ServiceIntegration for metrics/logs integration
func CreateServiceIntegrationSpec(scheme *runtime.Scheme, opensearch *v1.OpenSearch, cfg config.Aiven) (*aiven_v1alpha1.ServiceIntegration, error) {
	integration := MinimalServiceIntegration(opensearch)

	integration.Spec = aiven_v1alpha1.ServiceIntegrationSpec{
		Project:               cfg.Project,
		IntegrationType:       "prometheus",
		SourceServiceName:     ServiceName(opensearch),
		DestinationEndpointID: cfg.MetricsDestinationEndpointID,
	}

	err := controllerutil.SetControllerReference(opensearch, integration, scheme)
	if err != nil {
		return nil, fmt.Errorf("setting controller reference: %w", err)
	}

	return integration, nil
}
