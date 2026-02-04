package resourcecreator

import (
	"fmt"
	"maps"

	"github.com/nais/pgrator/internal/config"
	aiven_v1alpha1 "github.com/nais/pgrator/internal/thirdparty/aiven/v1alpha1"
	"github.com/nais/pgrator/pkg/annotation"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// AivenValkeyServiceName returns the namespaced Aiven service name for a Valkey instance.
// Format: valkey-{teamSlug}-{instanceName}
func AivenValkeyServiceName(valkey *v1.Valkey) string {
	return "valkey-" + valkey.GetNamespace() + "-" + valkey.GetName()
}

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
		Name:        AivenValkeyServiceName(valkey),
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
func CreateAivenValkeySpec(
	scheme *runtime.Scheme,
	valkey *v1.Valkey,
	aiven config.Aiven,
	tenant config.Tenant,
) (*aiven_v1alpha1.Valkey, error) {
	aivenValkey := MinimalAivenValkey(valkey)

	plan, err := valkey.AivenPlan()
	if err != nil {
		return nil, err
	}

	aivenValkey.Spec = aiven_v1alpha1.ValkeySpec{
		Project:      aiven.Project,
		Plan:         plan,
		ProjectVPCID: aiven.ProjectVPCID,
		// Disable termination protection because Nais API will just set it to false before deleting
		TerminationProtection: ptr.To(false),
		Tags: map[string]string{
			"team":   valkey.GetNamespace(),
			"app":    valkey.GetName(),
			"tenant": tenant.Name,
		},
	}

	if valkey.Spec.MaxMemoryPolicy != "" || valkey.Spec.NotifyKeyspaceEvents != "" {
		userConfig := &aiven_v1alpha1.ValkeyUserConfig{}

		if valkey.Spec.MaxMemoryPolicy != "" {
			policy := string(valkey.Spec.MaxMemoryPolicy)
			userConfig.ValkeyMaxmemoryPolicy = &policy
		}

		if valkey.Spec.NotifyKeyspaceEvents != "" {
			userConfig.ValkeyNotifyKeyspaceEvents = &valkey.Spec.NotifyKeyspaceEvents
		}

		aivenValkey.Spec.UserConfig = userConfig
	}

	err = controllerutil.SetControllerReference(valkey, aivenValkey, scheme)
	if err != nil {
		return nil, fmt.Errorf("setting controller reference: %w", err)
	}

	return aivenValkey, nil
}

// MinimalServiceIntegration creates a minimal ServiceIntegration object for use in delete operations
func MinimalServiceIntegration(valkey *v1.Valkey) *aiven_v1alpha1.ServiceIntegration {
	objectMeta := CreateValkeyObjectMeta(valkey)

	return &aiven_v1alpha1.ServiceIntegration{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ServiceIntegration",
			APIVersion: "aiven.io/v1alpha1",
		},
		ObjectMeta: objectMeta,
	}
}

// CreateServiceIntegrationSpec creates a ServiceIntegration for metrics/logs integration
func CreateServiceIntegrationSpec(scheme *runtime.Scheme, valkey *v1.Valkey, cfg config.Aiven) (*aiven_v1alpha1.ServiceIntegration, error) {
	integration := MinimalServiceIntegration(valkey)

	integration.Spec = aiven_v1alpha1.ServiceIntegrationSpec{
		Project:               cfg.Project,
		IntegrationType:       "prometheus",
		SourceServiceName:     AivenValkeyServiceName(valkey),
		DestinationEndpointID: cfg.MetricsDestinationEndpointID,
	}

	err := controllerutil.SetControllerReference(valkey, integration, scheme)
	if err != nil {
		return nil, fmt.Errorf("setting controller reference: %w", err)
	}

	return integration, nil
}
