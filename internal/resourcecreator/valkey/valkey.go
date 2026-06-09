package valkey

import (
	"fmt"
	"maps"
	"reflect"

	"github.com/nais/pgrator/internal/config"
	aiven_v1alpha1 "github.com/nais/pgrator/internal/thirdparty/aiven/v1alpha1"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// ServiceName returns the namespaced Aiven service name for a Valkey instance.
// Format: valkey-{teamSlug}-{instanceName}
func ServiceName(valkey *v1.Valkey) string {
	return "valkey-" + valkey.GetNamespace() + "-" + valkey.GetName()
}

// ObjectMeta creates a standard ObjectMeta for Valkey-owned resources
func ObjectMeta(valkey *v1.Valkey) metav1.ObjectMeta {
	labels := map[string]string{}
	maps.Copy(labels, valkey.GetLabels())

	labels["valkey.nais.io/name"] = valkey.GetName()

	var annotations map[string]string
	if valkey.GetCorrelationId() != "" {
		annotations = map[string]string{
			api.DeploymentCorrelationIDAnnotation: valkey.GetCorrelationId(),
		}
	}

	return metav1.ObjectMeta{
		Name:        ServiceName(valkey),
		Namespace:   valkey.GetNamespace(),
		Labels:      labels,
		Annotations: annotations,
	}
}

// Minimal creates a minimal Aiven Valkey object for use in delete operations
func Minimal(valkey *v1.Valkey) *aiven_v1alpha1.Valkey {
	objectMeta := ObjectMeta(valkey)

	return &aiven_v1alpha1.Valkey{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Valkey",
			APIVersion: "aiven.io/v1alpha1",
		},
		ObjectMeta: objectMeta,
	}
}

// CreateSpec creates an Aiven Valkey resource from a nais.io Valkey spec
func CreateSpec(
	scheme *runtime.Scheme,
	valkey *v1.Valkey,
	aiven config.Aiven,
	tenant config.Tenant,
) (*aiven_v1alpha1.Valkey, error) {
	aivenValkey := Minimal(valkey)

	plan, err := valkey.AivenPlan()
	if err != nil {
		return nil, err
	}

	aivenValkey.Spec = aiven_v1alpha1.ValkeySpec{
		Project:               aiven.Project,
		Plan:                  plan,
		ProjectVPCID:          aiven.ProjectVPCID,
		TerminationProtection: ptr.To(true),
		Tags: map[string]string{
			"team":   valkey.GetNamespace(),
			"app":    valkey.GetName(),
			"tenant": tenant.Name,
		},
	}

	if userConfig := aivenValkeyUserConfig(valkey); userConfig != nil {
		aivenValkey.Spec.UserConfig = userConfig
	}

	err = controllerutil.SetControllerReference(valkey, aivenValkey, scheme)
	if err != nil {
		return nil, fmt.Errorf("setting controller reference: %w", err)
	}

	return aivenValkey, nil
}

func aivenValkeyUserConfig(valkey *v1.Valkey) *aiven_v1alpha1.ValkeyUserConfig {
	userConfig := aiven_v1alpha1.ValkeyUserConfig{
		ValkeyNumberOfDatabases: valkey.Spec.Databases,
	}

	if valkey.Spec.MaxMemoryPolicy != "" {
		userConfig.ValkeyMaxmemoryPolicy = new(string(valkey.Spec.MaxMemoryPolicy))
	}

	if valkey.Spec.NotifyKeyspaceEvents != "" {
		userConfig.ValkeyNotifyKeyspaceEvents = &valkey.Spec.NotifyKeyspaceEvents
	}

	if valkey.Spec.Persistence != nil && valkey.Spec.Persistence.Disabled {
		userConfig.ValkeyPersistence = new("off")
	}

	if reflect.DeepEqual(userConfig, aiven_v1alpha1.ValkeyUserConfig{}) {
		return nil
	}
	return &userConfig
}

// MinimalServiceIntegration creates a minimal ServiceIntegration object for use in delete operations
func MinimalServiceIntegration(valkey *v1.Valkey) *aiven_v1alpha1.ServiceIntegration {
	objectMeta := ObjectMeta(valkey)

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
		SourceServiceName:     ServiceName(valkey),
		DestinationEndpointID: cfg.MetricsDestinationEndpointID,
	}

	err := controllerutil.SetControllerReference(valkey, integration, scheme)
	if err != nil {
		return nil, fmt.Errorf("setting controller reference: %w", err)
	}

	return integration, nil
}
