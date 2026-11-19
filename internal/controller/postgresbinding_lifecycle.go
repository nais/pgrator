package controller

import (
	"context"
	"fmt"
	"reflect"
	"slices"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// retireBindingRoles covers both whole-binding deletion and mutable credential
// removal. It works on existing owned roles, without requiring the source
// database, credentials, or certificates to still be available.
func (r *PostgresBindingReconciler) retireBindingRoles(binding *v1.PostgresBinding, related reconciler.RelatedObjects, all bool, missingClusters map[string]bool) ([]action.Action, bool, error) {
	var actions []action.Action
	pending := false
	for _, object := range related.GetMatchingType(&cnpgv1.DatabaseRole{}) {
		role := object.(*cnpgv1.DatabaseRole)
		if !metav1.IsControlledBy(role, binding) {
			continue
		}
		if !all && bindingRoleRequested(binding, role) {
			continue
		}
		if role.Spec.Name != rccnpg.OwnerRole && role.Spec.Name != binding.RoleName(v1.PostgresBindingCredentialRead) && role.Spec.Name != binding.RoleName(v1.PostgresBindingCredentialReadWrite) {
			return nil, false, fmt.Errorf("owned DatabaseRole %q has an unexpected SQL identity", role.Name)
		}
		pending = true
		if !role.DeletionTimestamp.IsZero() {
			if role.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimRetain {
				return nil, false, fmt.Errorf("DatabaseRole %q is already deleting with destructive reclaim policy", role.Name)
			}
			actions = append(actions, action.Claim(role, binding, databaseRoleConditionGetter, r.Recorder))
			continue
		}
		disabled := role.DeepCopy()
		disabled.Spec.ReclaimPolicy = cnpgv1.DatabaseRoleReclaimRetain
		// Old admin aliases may be binding-owned, but app is shared and durable.
		// Migrate their reclaim policy without disabling the database owner.
		if role.Spec.Name != rccnpg.OwnerRole {
			disabled.Spec.Login = false
			disabled.Spec.Superuser = false
			disabled.Spec.CreateDB = false
			disabled.Spec.CreateRole = false
			disabled.Spec.Replication = false
			disabled.Spec.BypassRLS = false
			disabled.Spec.DisablePassword = true
			disabled.Spec.PasswordSecret = nil
			disabled.Spec.InRoles = nil
			disabled.Spec.ClientCertificate = &cnpgv1.ClientCertificateConfiguration{Enabled: new(false)}
		}
		if !reflect.DeepEqual(disabled.Spec, role.Spec) {
			actions = append(actions, action.ExclusiveCreateOrUpdate(disabled, binding, databaseRoleConditionGetter, r.Recorder))
		} else if !missingClusters[role.Spec.ClusterRef.Name] && !databaseRoleIsReady(role) {
			actions = append(actions, action.Claim(role, binding, databaseRoleConditionGetter, r.Recorder))
		} else {
			actions = append(actions, action.DeleteIfExists(role, binding, existsConditionGetter, r.Recorder))
		}
	}
	return actions, pending, nil
}

func bindingRoleRequested(binding *v1.PostgresBinding, role *cnpgv1.DatabaseRole) bool {
	for _, credential := range binding.Spec.Credentials {
		if role.Spec.Name == binding.RoleName(credential) {
			return true
		}
	}
	return false
}

// Whole database deletion must not strand a binding waiting for a SQL revoke
// that can no longer run. Absence is explicit; timeouts or a deleting-but-still-
// present cluster do not bypass the applied-generation check.
func missingRetiringBindingClusters(ctx context.Context, reader client.Reader, binding *v1.PostgresBinding) (map[string]bool, error) {
	roles := &cnpgv1.DatabaseRoleList{}
	if err := reader.List(ctx, roles, client.InNamespace(binding.Namespace)); err != nil {
		return nil, err
	}
	var missing map[string]bool
	checked := map[string]bool{}
	for i := range roles.Items {
		role := &roles.Items[i]
		if !metav1.IsControlledBy(role, binding) || binding.DeletionTimestamp.IsZero() && bindingRoleRequested(binding, role) {
			continue
		}
		name := role.Spec.ClusterRef.Name
		if checked[name] {
			continue
		}
		checked[name] = true
		err := reader.Get(ctx, client.ObjectKey{Namespace: binding.Namespace, Name: name}, &cnpgv1.Cluster{})
		if apierrors.IsNotFound(err) {
			if missing == nil {
				missing = map[string]bool{}
			}
			missing[name] = true
		} else if err != nil {
			return nil, fmt.Errorf("checking retiring binding cluster: %w", err)
		}
	}
	return missing, nil
}

// A retained SQL identity must never be activated before its binding's cleanup
// finalizer is durable, including a partial first reconcile that later fails.
type protectPostgresBinding struct{ binding *v1.PostgresBinding }

func (a *protectPostgresBinding) GetObject() client.Object { return a.binding }
func (a *protectPostgresBinding) GetOwner() api.NaisObject { return a.binding }
func (*protectPostgresBinding) SkipOwnership() bool        { return true }
func (a *protectPostgresBinding) Do(ctx context.Context, c client.Client, _ *runtime.Scheme, _ ownership.OwnerManager) error {
	current := &v1.PostgresBinding{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(a.binding), current); err != nil {
		return err
	}
	if current.UID == "" || current.UID != a.binding.UID || !current.DeletionTimestamp.IsZero() || current.ResourceVersion != a.binding.ResourceVersion {
		return fmt.Errorf("binding changed before role reconciliation")
	}
	if !slices.Contains(current.Finalizers, "postgresbinding.nais.io") {
		original := current.DeepCopy()
		current.Finalizers = append(current.Finalizers, "postgresbinding.nais.io")
		if err := c.Patch(ctx, current, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
	}
	a.binding.ResourceVersion = current.ResourceVersion
	a.binding.Finalizers = slices.Clone(current.Finalizers)
	return nil
}
