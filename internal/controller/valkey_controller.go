package controller

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/controller/resourcecreator"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	aiven_v1alpha1 "github.com/nais/pgrator/pkg/api/thirdparty/aiven/v1alpha1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
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

	aivenValkey, err := resourcecreator.CreateAivenValkeySpec(r.Scheme, obj, r.Aiven, r.Tenant)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating Aiven Valkey spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(aivenValkey, obj, aivenValkeyConditionGetter, r.Recorder))

	serviceIntegration, err := resourcecreator.CreateServiceIntegrationSpec(r.Scheme, obj, r.Aiven)
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

func (r *ValkeyReconciler) Delete(obj *v1.Valkey, _ ValkeyPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	var actions []action.Action

	serviceIntegration := resourcecreator.MinimalServiceIntegration(obj)
	actions = append(actions, action.DeleteIfExists(serviceIntegration, obj, serviceIntegrationConditionGetter, r.Recorder))

	aivenValkey := resourcecreator.MinimalAivenValkey(obj)
	actions = append(actions, action.DeleteIfExists(aivenValkey, obj, aivenValkeyConditionGetter, r.Recorder))

	return actions, ctrl.Result{}, nil
}
