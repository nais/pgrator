package v1

import (
	"context"
	"fmt"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// OpenSearchValidator validates OpenSearch resources
type OpenSearchValidator struct{}

// SetupWebhookWithManager sets up the webhook with the Manager.
func (o *OpenSearch) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, o).
		WithValidator(&OpenSearchValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-nais-io-v1-opensearch,mutating=false,failurePolicy=fail,sideEffects=None,groups=nais.io,resources=opensearches,verbs=create;update;delete,versions=v1,name=vopensearch.nais.io,admissionReviewVersions=v1

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *OpenSearchValidator) ValidateCreate(_ context.Context, obj *OpenSearch) (admission.Warnings, error) {
	return obj.validate()
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *OpenSearchValidator) ValidateUpdate(_ context.Context, oldObj *OpenSearch, newObj *OpenSearch) (admission.Warnings, error) {
	return newObj.validateUpdate(oldObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *OpenSearchValidator) ValidateDelete(_ context.Context, obj *OpenSearch) (admission.Warnings, error) {
	reason, ok := obj.CanBeDeleted()
	if !ok {
		return nil, fmt.Errorf("refusing deletion: %s", reason)
	}
	return nil, nil
}

func (o *OpenSearch) validate() (admission.Warnings, error) {
	var errs []string

	// Validate name length for generated Aiven service name
	// Format: opensearch-{namespace}-{name} must be <= 63 characters
	maxNameLength := 63 - len("opensearch-") - len(o.GetNamespace()) - len("-")
	if maxNameLength <= 0 {
		return nil, fmt.Errorf("metadata.namespace is too long; cannot construct service name \"opensearch-%s-%s\" within 63 characters", o.GetNamespace(), o.GetName())
	}
	if len(o.GetName()) > maxNameLength {
		errs = append(errs, fmt.Sprintf("metadata.name is too long; max length is %d characters", maxNameLength))
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

	// Validate http.maxContentLength bounds
	if o.Spec.Http != nil && o.Spec.Http.MaxContentLength != nil {
		bytes := o.Spec.Http.MaxContentLength.Value()
		if bytes < 1 {
			errs = append(errs, "http.maxContentLength must be at least 1 byte")
		}
		if bytes > 2147483647 {
			errs = append(errs, "http.maxContentLength must be at most 2147483647 bytes (2047Mi or less)")
		}
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
