package v1

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// ValkeyValidator validates Valkey resources
type ValkeyValidator struct{}

// SetupWebhookWithManager sets up the webhook with the Manager.
func (v *Valkey) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, v).
		WithValidator(&ValkeyValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-nais-io-v1-valkey,mutating=false,failurePolicy=fail,sideEffects=None,groups=nais.io,resources=valkeys,verbs=create;delete,versions=v1,name=vvalkey.nais.io,admissionReviewVersions=v1

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *ValkeyValidator) ValidateCreate(_ context.Context, obj *Valkey) (admission.Warnings, error) {
	// Validate name length for generated Aiven service name
	// Format: valkey-{namespace}-{name} must be <= 63 characters
	maxNameLength := 63 - len("valkey-") - len(obj.GetNamespace()) - len("-")
	if maxNameLength <= 0 {
		return nil, fmt.Errorf("metadata.namespace is too long; cannot construct service name \"valkey-%s-%s\" within 63 characters", obj.GetNamespace(), obj.GetName())
	}
	if len(obj.GetName()) > maxNameLength {
		return nil, fmt.Errorf("metadata.name is too long; max length is %d characters", maxNameLength)
	}
	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *ValkeyValidator) ValidateUpdate(_ context.Context, _ *Valkey, _ *Valkey) (admission.Warnings, error) {
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *ValkeyValidator) ValidateDelete(_ context.Context, obj *Valkey) (admission.Warnings, error) {
	reason, ok := obj.CanBeDeleted()
	if !ok {
		return nil, fmt.Errorf("refusing deletion: %s", reason)
	}
	return nil, nil
}
