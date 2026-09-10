package v1

import (
	"context"
	"fmt"
	"reflect"
	"strings"

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

// +kubebuilder:webhook:path=/validate-nais-io-v1-valkey,mutating=false,failurePolicy=fail,sideEffects=None,groups=nais.io,resources=valkeys,verbs=create;update;delete,versions=v1,name=vvalkey.nais.io,admissionReviewVersions=v1

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *ValkeyValidator) ValidateCreate(_ context.Context, obj *Valkey) (admission.Warnings, error) {
	if obj.Spec.Version == "" {
		return nil, fmt.Errorf("validation failed: spec.version is required")
	}
	return obj.validate()
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *ValkeyValidator) ValidateUpdate(_ context.Context, oldObj *Valkey, newObj *Valkey) (admission.Warnings, error) {
	// pgrator's own finalizer and annotation writes arrive here too; only spec edits are governed.
	if reflect.DeepEqual(oldObj.Spec, newObj.Spec) {
		return nil, nil
	}
	return newObj.validateUpdate(oldObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *ValkeyValidator) ValidateDelete(_ context.Context, obj *Valkey) (admission.Warnings, error) {
	reason, ok := obj.CanBeDeleted()
	if !ok {
		return nil, fmt.Errorf("refusing deletion: %s", reason)
	}
	return nil, nil
}

func (v *Valkey) validate() (admission.Warnings, error) {
	var errs []string

	// Validate name length for generated Aiven service name
	// Format: valkey-{namespace}-{name} must be <= 63 characters
	maxNameLength := 63 - len("valkey-") - len(v.GetNamespace()) - len("-")
	if maxNameLength <= 0 {
		return nil, fmt.Errorf("metadata.namespace is too long; cannot construct service name \"valkey-%s-%s\" within 63 characters", v.GetNamespace(), v.GetName())
	}
	if len(v.GetName()) > maxNameLength {
		errs = append(errs, fmt.Sprintf("metadata.name is too long; max length is %d characters", maxNameLength))
	}

	// Validate version is known
	if _, ok := valkeyUpgradePaths[v.Spec.Version]; !ok {
		errs = append(errs, fmt.Sprintf("unknown Valkey version: %q", v.Spec.Version))
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("validation failed: %s", strings.Join(errs, "; "))
	}

	var warnings admission.Warnings
	if warning, deprecated := v.Spec.Version.DeprecationWarning(); deprecated {
		warnings = append(warnings, warning)
	}

	return warnings, nil
}

func (v *Valkey) validateUpdate(old *Valkey) (admission.Warnings, error) {
	if v.Spec.Version == "" {
		if old.Spec.Version == "" {
			return nil, fmt.Errorf("validation failed: spec.version is required")
		}
		return nil, fmt.Errorf("validation failed: spec.version cannot be unset once set")
	}

	warnings, err := v.validate()
	if err != nil {
		return warnings, err
	}

	// An object adopting a version for the first time has no prior version to step from. Its
	// direction cannot be checked here either, since the webhook has no client to read what Aiven
	// reports as running; the reconciler refuses the downgrade instead.
	if old.Spec.Version != "" {
		if err := v.Spec.Version.ValidateUpgradePath(old.Spec.Version); err != nil {
			return nil, fmt.Errorf("validation failed: %s", err)
		}
	}

	return warnings, nil
}
