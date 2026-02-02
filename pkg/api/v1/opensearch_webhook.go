package v1

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var _ webhook.CustomValidator = &OpenSearchValidator{}

// OpenSearchValidator validates OpenSearch resources
type OpenSearchValidator struct{}

// SetupWebhookWithManager sets up the webhook with the Manager.
func (o *OpenSearch) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(o).
		WithValidator(&OpenSearchValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-nais-io-v1-opensearch,mutating=false,failurePolicy=fail,sideEffects=None,groups=nais.io,resources=opensearches,verbs=create;update,versions=v1,name=vopensearch.kb.io,admissionReviewVersions=v1

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *OpenSearchValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	o, ok := obj.(*OpenSearch)
	if !ok {
		return nil, fmt.Errorf("expected OpenSearch but got %T", obj)
	}
	return o.validate()
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *OpenSearchValidator) ValidateUpdate(_ context.Context, _ runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	o, ok := newObj.(*OpenSearch)
	if !ok {
		return nil, fmt.Errorf("expected OpenSearch but got %T", newObj)
	}
	return o.validate()
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *OpenSearchValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	// No validation needed for delete
	return nil, nil
}

func (o *OpenSearch) validate() (admission.Warnings, error) {
	var errs []string

	// Validate name length for generated Aiven service name
	// Format: opensearch-{namespace}-{name} must be <= 63 characters
	// "opensearch-" is 11 characters, "-" is 1 character = 12 characters overhead
	maxNameLength := 63 - 12 - len(o.GetNamespace())
	if len(o.GetName()) > maxNameLength {
		errs = append(errs, fmt.Sprintf("metadata.name is too long; max length is %d characters (generated service name would exceed 63 characters)", maxNameLength))
	}

	// Validate tier and memory combination
	machineType, err := o.GetMachineType()
	if err != nil {
		errs = append(errs, fmt.Sprintf("invalid tier/memory combination: %s", err))
	} else {
		// Validate storage
		storageErrs := o.validateStorage(machineType)
		errs = append(errs, storageErrs...)
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("validation failed: %s", strings.Join(errs, "; "))
	}

	return nil, nil
}

func (o *OpenSearch) validateStorage(machineType *MachineType) []string {
	var errs []string

	storage := o.Spec.StorageGB

	// For hobbyist plan, storage is fixed
	if machineType.AivenPlan == "hobbyist" {
		if storage != machineType.StorageMin {
			errs = append(errs, fmt.Sprintf("storage for hobbyist plan must be exactly %dGB", machineType.StorageMin))
		}
		return errs
	}

	// Check storage bounds
	if storage < machineType.StorageMin {
		errs = append(errs, fmt.Sprintf("storage must be at least %dGB for tier %s with memory %s", machineType.StorageMin, o.Spec.Tier, o.Spec.Memory))
	}

	if storage > machineType.StorageMax {
		errs = append(errs, fmt.Sprintf("storage must be at most %dGB for tier %s with memory %s", machineType.StorageMax, o.Spec.Tier, o.Spec.Memory))
	}

	// Check storage increments
	if machineType.StorageIncrements > 1 {
		offset := storage - machineType.StorageMin
		if offset%machineType.StorageIncrements != 0 {
			// Generate example valid values
			var examples []string
			for i := machineType.StorageMin; i <= machineType.StorageMax && len(examples) < 5; i += machineType.StorageIncrements {
				examples = append(examples, fmt.Sprintf("%d", i))
			}
			errs = append(errs, fmt.Sprintf("storage must be in increments of %dGB starting from %dGB (valid examples: %s, ...)", machineType.StorageIncrements, machineType.StorageMin, strings.Join(examples, ", ")))
		}
	}

	return errs
}
