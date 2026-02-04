package resourcecreator

import (
	"fmt"
	"maps"
	"strconv"

	"github.com/nais/pgrator/internal/config"
	aiven_v1alpha1 "github.com/nais/pgrator/internal/thirdparty/aiven/v1alpha1"
	"github.com/nais/pgrator/pkg/annotation"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// AivenOpenSearchServiceName returns the namespaced Aiven service name for an OpenSearch instance.
// Format: opensearch-{teamSlug}-{instanceName}
func AivenOpenSearchServiceName(opensearch *v1.OpenSearch) string {
	return "opensearch-" + opensearch.GetNamespace() + "-" + opensearch.GetName()
}

// CreateOpenSearchObjectMeta creates a standard ObjectMeta for OpenSearch-owned resources
func CreateOpenSearchObjectMeta(opensearch *v1.OpenSearch) metav1.ObjectMeta {
	labels := map[string]string{}
	maps.Copy(labels, opensearch.GetLabels())

	labels["opensearch.nais.io/name"] = opensearch.GetName()

	var annotations map[string]string
	if opensearch.GetCorrelationId() != "" {
		annotations = map[string]string{
			annotation.DeploymentCorrelationIDAnnotation: opensearch.GetCorrelationId(),
		}
	}

	return metav1.ObjectMeta{
		Name:        AivenOpenSearchServiceName(opensearch),
		Namespace:   opensearch.GetNamespace(),
		Labels:      labels,
		Annotations: annotations,
	}
}

// MinimalAivenOpenSearch creates a minimal Aiven OpenSearch object for use in delete operations
func MinimalAivenOpenSearch(opensearch *v1.OpenSearch) *aiven_v1alpha1.OpenSearch {
	objectMeta := CreateOpenSearchObjectMeta(opensearch)

	return &aiven_v1alpha1.OpenSearch{
		TypeMeta: metav1.TypeMeta{
			Kind:       "OpenSearch",
			APIVersion: "aiven.io/v1alpha1",
		},
		ObjectMeta: objectMeta,
	}
}

// CreateAivenOpenSearchSpec creates an Aiven OpenSearch resource from a nais.io OpenSearch spec
func CreateAivenOpenSearchSpec(
	scheme *runtime.Scheme,
	opensearch *v1.OpenSearch,
	aiven config.Aiven,
	tenant config.Tenant,
) (*aiven_v1alpha1.OpenSearch, error) {
	aivenOpenSearch := MinimalAivenOpenSearch(opensearch)

	plan, err := opensearch.AivenPlan()
	if err != nil {
		return nil, err
	}

	version, err := opensearch.Spec.Version.ToAivenString()
	if err != nil {
		return nil, err
	}

	aivenOpenSearch.Spec = aiven_v1alpha1.OpenSearchSpec{
		Project:      aiven.Project,
		Plan:         plan,
		ProjectVPCID: aiven.ProjectVPCID,
		DiskSpace:    strconv.Itoa(opensearch.Spec.StorageGB) + "GiB",
		// Disable termination protection because Nais API will just set it to false before deleting
		TerminationProtection: ptr.To(false),
		Tags: map[string]string{
			"team":   opensearch.GetNamespace(),
			"app":    opensearch.GetName(),
			"tenant": tenant.Name,
		},
		UserConfig: &aiven_v1alpha1.OpenSearchUserConfig{
			OpenSearchVersion: &version,
		},
	}

	err = controllerutil.SetControllerReference(opensearch, aivenOpenSearch, scheme)
	if err != nil {
		return nil, fmt.Errorf("setting controller reference: %w", err)
	}

	return aivenOpenSearch, nil
}

// MinimalOpenSearchServiceIntegration creates a minimal ServiceIntegration object for use in delete operations
func MinimalOpenSearchServiceIntegration(opensearch *v1.OpenSearch) *aiven_v1alpha1.ServiceIntegration {
	objectMeta := CreateOpenSearchObjectMeta(opensearch)

	return &aiven_v1alpha1.ServiceIntegration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceIntegration",
			APIVersion: "aiven.io/v1alpha1",
		},
		ObjectMeta: objectMeta,
	}
}

// CreateOpenSearchServiceIntegrationSpec creates a ServiceIntegration for metrics/logs integration
func CreateOpenSearchServiceIntegrationSpec(scheme *runtime.Scheme, opensearch *v1.OpenSearch, cfg config.Aiven) (*aiven_v1alpha1.ServiceIntegration, error) {
	integration := MinimalOpenSearchServiceIntegration(opensearch)

	integration.Spec = aiven_v1alpha1.ServiceIntegrationSpec{
		Project:               cfg.Project,
		IntegrationType:       "prometheus",
		SourceServiceName:     AivenOpenSearchServiceName(opensearch),
		DestinationEndpointID: cfg.MetricsDestinationEndpointID,
	}

	err := controllerutil.SetControllerReference(opensearch, integration, scheme)
	if err != nil {
		return nil, fmt.Errorf("setting controller reference: %w", err)
	}

	return integration, nil
}
