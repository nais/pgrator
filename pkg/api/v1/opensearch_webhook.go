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

// +kubebuilder:webhook:path=/validate-nais-io-v1-opensearch,mutating=false,failurePolicy=fail,sideEffects=None,groups=nais.io,resources=opensearches,verbs=create;update,versions=v1,name=vopensearch.nais.io,admissionReviewVersions=v1

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *OpenSearchValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	o, ok := obj.(*OpenSearch)
	if !ok {
		return nil, fmt.Errorf("expected OpenSearch but got %T", obj)
	}
	return o.validate()
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *OpenSearchValidator) ValidateUpdate(_ context.Context, oldObj runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	o, ok := newObj.(*OpenSearch)
	if !ok {
		return nil, fmt.Errorf("expected OpenSearch but got %T", newObj)
	}
	old, ok := oldObj.(*OpenSearch)
	if !ok {
		return nil, fmt.Errorf("expected OpenSearch but got %T", oldObj)
	}
	return o.validateUpdate(old)
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

	// Validate version is known
	if _, ok := upgradePaths[o.Spec.Version]; !ok {
		errs = append(errs, fmt.Sprintf("unknown OpenSearch version: %q", o.Spec.Version))
	}

	// Validate tier and memory combination
	planConfig, err := o.aivenPlanConfig()
	if err != nil {
		errs = append(errs, fmt.Sprintf("invalid tier/memory combination: %s", err))
	} else {
		// Validate storage
		storageErrs := o.validateStorage(planConfig)
		errs = append(errs, storageErrs...)
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("validation failed: %s", strings.Join(errs, "; "))
	}

	return nil, nil
}

func (o *OpenSearch) validateUpdate(old *OpenSearch) (admission.Warnings, error) {
	warnings, err := o.validate()
	if err != nil {
		return warnings, err
	}

	// Validate version upgrade path
	if err := o.Spec.Version.ValidateUpgradePath(old.Spec.Version); err != nil {
		return nil, fmt.Errorf("validation failed: %s", err)
	}

	return warnings, nil
}

func (o *OpenSearch) validateStorage(planConfig *openSearchPlanConfig) []string {
	var errs []string

	storage := o.Spec.StorageGB

	// Check storage bounds
	if storage < planConfig.Storage.Min {
		errs = append(errs, fmt.Sprintf("storage must be at least %dGB for tier %s with memory %s", planConfig.Storage.Min, o.Spec.Tier, o.Spec.Memory))
	}

	if storage > planConfig.Storage.Max {
		errs = append(errs, fmt.Sprintf("storage must be at most %dGB for tier %s with memory %s", planConfig.Storage.Max, o.Spec.Tier, o.Spec.Memory))
	}

	// Check storage increments
	if planConfig.Storage.Increments > 1 {
		offset := storage - planConfig.Storage.Min
		if offset%planConfig.Storage.Increments != 0 {
			errs = append(errs, fmt.Sprintf("storage must be in increments of %dGB starting from %dGB", planConfig.Storage.Increments, planConfig.Storage.Min))
		}
	}

	return errs
}
