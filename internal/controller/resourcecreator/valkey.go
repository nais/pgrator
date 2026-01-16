package resourcecreator

import (
	"maps"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/pkg/annotation"
	aiven_v1alpha1 "github.com/nais/pgrator/pkg/api/thirdparty/aiven/v1alpha1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// CreateValkeyObjectMeta creates a standard ObjectMeta for Valkey-owned resources
func CreateValkeyObjectMeta(valkey *v1.Valkey) metav1.ObjectMeta {
	labels := map[string]string{}
	maps.Copy(labels, valkey.GetLabels())

	labels["valkey.nais.io/name"] = valkey.GetName()

	var annotations map[string]string
	if valkey.GetCorrelationId() != "" {
		annotations = map[string]string{
			annotation.DeploymentCorrelationIDAnnotation: valkey.GetCorrelationId(),
		}
	}

	return metav1.ObjectMeta{
		Name:        valkey.GetName(),
		Namespace:   valkey.GetNamespace(),
		Labels:      labels,
		Annotations: annotations,
	}
}

// MinimalAivenValkey creates a minimal Aiven Valkey object for use in delete operations
func MinimalAivenValkey(valkey *v1.Valkey) *aiven_v1alpha1.Valkey {
	objectMeta := CreateValkeyObjectMeta(valkey)

	return &aiven_v1alpha1.Valkey{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Valkey",
			APIVersion: "aiven.io/v1alpha1",
		},
		ObjectMeta: objectMeta,
	}
}

// CreateAivenValkeySpec creates an Aiven Valkey resource from a nais.io Valkey spec
func CreateAivenValkeySpec(valkey *v1.Valkey, cfg *config.Config, aivenPlan string) *aiven_v1alpha1.Valkey {
	aivenValkey := MinimalAivenValkey(valkey)

	userConfig := &aiven_v1alpha1.ValkeyUserConfig{}

	// Set max memory policy if specified
	if valkey.Spec.MaxMemoryPolicy != "" {
		policy := string(valkey.Spec.MaxMemoryPolicy)
		userConfig.ValkeyMaxmemoryPolicy = &policy
	}

	// Set keyspace notifications if specified
	if valkey.Spec.NotifyKeyspaceEvents != "" {
		userConfig.ValkeyNotifyKeyspaceEvents = &valkey.Spec.NotifyKeyspaceEvents
	}

	// Build tags
	tags := map[string]string{
		"team":       valkey.GetNamespace(),
		"app":        valkey.GetName(),
		"managed-by": "pgrator",
	}
	if cfg.AivenTenantName != "" {
		tags["tenant"] = cfg.AivenTenantName
	}

	aivenValkey.Spec = aiven_v1alpha1.ValkeySpec{
		Project:               cfg.AivenProject,
		Plan:                  aivenPlan,
		ProjectVPCID:          cfg.AivenProjectVPCID,
		TerminationProtection: ptr.To(true),
		Tags:                  tags,
		ConnInfoSecretTarget: &aiven_v1alpha1.ConnInfoSecretTarget{
			Name: valkey.GetName(),
		},
		UserConfig: userConfig,
	}

	return aivenValkey
}

// MinimalServiceIntegration creates a minimal ServiceIntegration object for use in delete operations
func MinimalServiceIntegration(valkey *v1.Valkey, integrationName string) *aiven_v1alpha1.ServiceIntegration {
	objectMeta := CreateValkeyObjectMeta(valkey)
	objectMeta.Name = integrationName

	return &aiven_v1alpha1.ServiceIntegration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceIntegration",
			APIVersion: "aiven.io/v1alpha1",
		},
		ObjectMeta: objectMeta,
	}
}

// CreateServiceIntegrationSpec creates a ServiceIntegration for metrics/logs integration
// integrationType should be one of: "metrics", "logs", "datadog", etc.
func CreateServiceIntegrationSpec(
	valkey *v1.Valkey,
	cfg *config.Config,
	integrationName string,
	integrationType string,
	destinationEndpointID string,
) *aiven_v1alpha1.ServiceIntegration {
	integration := MinimalServiceIntegration(valkey, integrationName)

	integration.Spec = aiven_v1alpha1.ServiceIntegrationSpec{
		Project:               cfg.AivenProject,
		IntegrationType:       integrationType,
		SourceServiceName:     valkey.GetName(),
		DestinationEndpointID: destinationEndpointID,
	}

	return integration
}
