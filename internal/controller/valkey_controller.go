package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/nais/pgrator/internal/config"
	rcvalkey "github.com/nais/pgrator/internal/resourcecreator/valkey"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	aiven_v1alpha1 "github.com/nais/pgrator/internal/thirdparty/aiven/v1alpha1"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	core_v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ValkeyReconciler reconciles a Valkey object
type ValkeyReconciler struct {
	Aiven  config.Aiven
	Tenant config.Tenant

	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

var _ reconciler.Reconciler[*v1.Valkey, ValkeyPreparedData] = &ValkeyReconciler{}

// ValkeyPreparedData contains data prepared during the Prepare phase
type ValkeyPreparedData struct{}

func (r *ValkeyReconciler) Name() string {
	return "valkey.nais.io"
}

func (r *ValkeyReconciler) New() *v1.Valkey {
	return &v1.Valkey{}
}

func (r *ValkeyReconciler) FinalizerName() string {
	return "valkey.nais.io/finalizer"
}

func (r *ValkeyReconciler) Prepare(_ context.Context, _ client.Reader, _ *v1.Valkey) (ValkeyPreparedData, ctrl.Result, error) {
	return ValkeyPreparedData{}, ctrl.Result{}, nil
}

func (r *ValkeyReconciler) OwnedTypes() []reconciler.OwnedType {
	return []reconciler.OwnedType{
		{
			Type: &aiven_v1alpha1.Valkey{},
			AdditionalPredicate: predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldObj, ok1 := e.ObjectOld.(*aiven_v1alpha1.Valkey)
					newObj, ok2 := e.ObjectNew.(*aiven_v1alpha1.Valkey)
					if !ok1 || !ok2 {
						return false
					}

					// We're only watching for status.state changes
					return oldObj.Status.State != newObj.Status.State
				},
			},
		},
		{Type: &aiven_v1alpha1.ServiceIntegration{}},
	}
}

func (r *ValkeyReconciler) AdditionalTypes() []client.Object {
	return nil
}

func (r *ValkeyReconciler) Update(obj *v1.Valkey, _ ValkeyPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	var actions []action.Action

	aivenValkey, err := rcvalkey.CreateSpec(r.Scheme, obj, r.Aiven, r.Tenant)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating Aiven Valkey spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(aivenValkey, obj, aivenValkeyConditionGetter, r.Recorder))

	serviceIntegration, err := rcvalkey.CreateServiceIntegrationSpec(r.Scheme, obj, r.Aiven)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating ServiceIntegration spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(serviceIntegration, obj, serviceIntegrationConditionGetter, r.Recorder))

	return actions, ctrl.Result{}, nil
}

func aivenValkeyConditionGetter(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition {
	aivenValkey := obj.(*aiven_v1alpha1.Valkey)

	state := aivenValkey.Status.State

	return []meta_v1.Condition{
		{
			Type:               fmt.Sprintf("%s/%s", typePrefix(obj, scheme), "ObservedState"),
			Status:             makeCondition(state != ""),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             "Reconciled",
			Message:            fmt.Sprintf("Valkey is in state: %s", state),
		},
	}
}

func serviceIntegrationConditionGetter(_ client.Object, _ *runtime.Scheme) []meta_v1.Condition {
	return nil
}

func (r *ValkeyReconciler) Delete(obj *v1.Valkey, _ ValkeyPreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	if reason, ok := obj.CanBeDeleted(); !ok {
		r.Recorder.RecordEvent(obj, core_v1.EventTypeWarning, "DeletionRefused", "Deletion requires the annotation %q set to \"true\"", api.AllowDeletionAnnotation)
		return nil, ctrl.Result{}, fmt.Errorf("refusing to delete resource: %s", reason)
	}

	aivenValkey := rcvalkey.Minimal(obj)
	existing := relatedObjects.GetMatching(aivenValkey)
	if existing == nil {
		return nil, ctrl.Result{}, nil
	}

	existingValkey, ok := existing.(*aiven_v1alpha1.Valkey)
	if !ok {
		return nil, ctrl.Result{}, fmt.Errorf("unexpected type for existing Valkey: %T", existing)
	}

	if existingValkey.Spec.TerminationProtection != nil && *existingValkey.Spec.TerminationProtection {
		existingValkey.Spec.TerminationProtection = new(false)
		r.Recorder.RecordEvent(obj, core_v1.EventTypeNormal, "DisablingTerminationProtection", "Disabling termination protection before deletion")

		actions := []action.Action{
			action.Update(existingValkey, obj, aivenValkeyConditionGetter, r.Recorder),
		}

		return actions, ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	serviceIntegration := rcvalkey.MinimalServiceIntegration(obj)
	actions := []action.Action{
		action.DeleteIfExists(serviceIntegration, obj, serviceIntegrationConditionGetter, r.Recorder),
		action.DeleteIfExists(aivenValkey, obj, aivenValkeyConditionGetter, r.Recorder),
	}

	return actions, ctrl.Result{}, nil
}
