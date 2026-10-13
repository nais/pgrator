package v1

import (
	"context"
	"fmt"
	"reflect"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// PostgresBindingValidator enforces immutable binding identity. Admin credentials
// may be consumed by multiple bindings; the branch owns the app role and certificate.
// +kubebuilder:object:generate=false
type PostgresBindingValidator struct{}

func (p *PostgresBinding) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, p).
		WithValidator(&PostgresBindingValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-nais-io-v1-postgresbinding,mutating=false,failurePolicy=fail,sideEffects=None,groups=nais.io,resources=postgresbindings,verbs=create;update,versions=v1,name=vpostgresbinding.nais.io,admissionReviewVersions=v1

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *PostgresBindingValidator) ValidateCreate(_ context.Context, _ *PostgresBinding) (admission.Warnings, error) {
	return nil, nil
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *PostgresBindingValidator) ValidateUpdate(_ context.Context, oldObj *PostgresBinding, newObj *PostgresBinding) (admission.Warnings, error) {
	if oldObj.Spec.Postgres != newObj.Spec.Postgres || (oldObj.Spec.SecretName != "" && oldObj.Spec.SecretName != newObj.Spec.SecretName) || !reflect.DeepEqual(oldObj.Spec.Consumer, newObj.Spec.Consumer) {
		return nil, fmt.Errorf("postgres, secretName, and consumer are immutable")
	}
	return nil, nil
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type.
func (v *PostgresBindingValidator) ValidateDelete(_ context.Context, _ *PostgresBinding) (admission.Warnings, error) {
	return nil, nil
}
