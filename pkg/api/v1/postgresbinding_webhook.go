package v1

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// PostgresBindingValidator ensures only one binding manages a cluster's app role.
// +kubebuilder:object:generate=false
type PostgresBindingValidator struct {
	reader client.Reader
}

// SetupWebhookWithManager sets up the webhook with the Manager.
func (p *PostgresBinding) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, p).
		WithValidator(&PostgresBindingValidator{reader: mgr.GetAPIReader()}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-nais-io-v1-postgresbinding,mutating=false,failurePolicy=fail,sideEffects=None,groups=nais.io,resources=postgresbindings,verbs=create;update,versions=v1,name=vpostgresbinding.nais.io,admissionReviewVersions=v1

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *PostgresBindingValidator) ValidateCreate(ctx context.Context, obj *PostgresBinding) (admission.Warnings, error) {
	return nil, v.validate(ctx, obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *PostgresBindingValidator) ValidateUpdate(ctx context.Context, oldObj *PostgresBinding, newObj *PostgresBinding) (admission.Warnings, error) {
	if oldObj.Spec != newObj.Spec {
		return nil, fmt.Errorf("spec is immutable")
	}
	return nil, v.validate(ctx, newObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *PostgresBindingValidator) ValidateDelete(_ context.Context, _ *PostgresBinding) (admission.Warnings, error) {
	return nil, nil
}

func (v *PostgresBindingValidator) validate(ctx context.Context, obj *PostgresBinding) error {
	return v.validateAdminBinding(ctx, obj)
}

func (v *PostgresBindingValidator) validateAdminBinding(ctx context.Context, obj *PostgresBinding) error {
	if obj.Spec.Role != PostgresBindingRoleAdmin {
		return nil
	}

	bindings := &PostgresBindingList{}
	if err := v.reader.List(ctx, bindings, client.InNamespace(obj.GetNamespace())); err != nil {
		return fmt.Errorf("listing PostgresBindings: %w", err)
	}
	for _, binding := range bindings.Items {
		if binding.GetName() == obj.GetName() {
			continue
		}
		if binding.Spec.Postgres == obj.Spec.Postgres && binding.Spec.Role == PostgresBindingRoleAdmin {
			return fmt.Errorf("Postgres %q already has admin binding %q", obj.Spec.Postgres, binding.GetName())
		}
	}
	return nil
}
