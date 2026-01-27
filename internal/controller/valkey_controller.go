package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/aiven/go-client-codegen/handler/service"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/controller/resourcecreator"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	aiven_v1alpha1 "github.com/nais/pgrator/pkg/api/thirdparty/aiven/v1alpha1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ValkeyReconciler reconciles a Valkey object
type ValkeyReconciler struct {
	Aiven  *config.Aiven
	Tenant *config.Tenant

	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

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

func (r *ValkeyReconciler) Prepare(_ctx context.Context, _reader client.Reader, obj *v1.Valkey) (ValkeyPreparedData, ctrl.Result, error) {
	return ValkeyPreparedData{}, ctrl.Result{}, nil
}

func (r *ValkeyReconciler) OwnedTypes() []client.Object {
	return []client.Object{
		&aiven_v1alpha1.Valkey{},
		&aiven_v1alpha1.ServiceIntegration{},
	}
}

func (r *ValkeyReconciler) AdditionalTypes() []client.Object {
	return nil
}

func (r *ValkeyReconciler) Update(obj *v1.Valkey, preparedData ValkeyPreparedData) ([]action.Action, ctrl.Result, error) {
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

// aivenValkeyConditionGetter extracts conditions from an Aiven Valkey resource
func aivenValkeyConditionGetter(obj client.Object, _ *runtime.Scheme) []meta_v1.Condition {
	typePrefix := strings.ToLower(obj.GetObjectKind().GroupVersionKind().GroupKind().String())
	aivenValkey := obj.(*aiven_v1alpha1.Valkey)

	state := service.ServiceStateType(aivenValkey.Status.State)

	reason := ""
	message := string(state)
	lastTransition := meta_v1.Time{}
	for _, c := range aivenValkey.Status.Conditions {
		if c.LastTransitionTime.After(lastTransition.Time) {
			lastTransition = c.LastTransitionTime
			reason = makeReason(&c)
			message = makeMessage(&c)
		}
	}

	if reason == "" {
		reason = "NoConditions"
	}

	// Aiven states: POWEROFF, REBUILDING, REBALANCING, RUNNING (available in [service.ServiceStateTypeChoices()])
	type conditionConfig struct {
		Type   string
		Status bool
	}
	conditions := []conditionConfig{
		{
			Type:   "Available",
			Status: state == service.ServiceStateTypeRunning,
		},
		{
			Type: "Progressing",
			Status: slices.Contains([]service.ServiceStateType{
				service.ServiceStateTypeRebuilding,
				service.ServiceStateTypeRebalancing,
				"",
			}, state),
		},
		{
			Type:   "Degraded",
			Status: state == service.ServiceStateTypePoweroff,
		},
	}

	result := make([]meta_v1.Condition, 0, len(conditions))
	for _, condition := range conditions {
		t := fmt.Sprintf("%s/%s", typePrefix, condition.Type)

		result = append(result, meta_v1.Condition{
			Type:               t,
			Status:             makeCondition(condition.Status),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             reason,
			Message:            message,
		})
	}

	return result
}

// serviceIntegrationConditionGetter extracts conditions from an Aiven ServiceIntegration resource
func serviceIntegrationConditionGetter(obj client.Object, _ *runtime.Scheme) []meta_v1.Condition {
	typePrefix := strings.ToLower(obj.GetObjectKind().GroupVersionKind().GroupKind().String())
	integration := obj.(*aiven_v1alpha1.ServiceIntegration)

	if len(integration.Status.Conditions) == 0 {
		return nil
	}

	conditions := integration.Status.Conditions
	result := make([]meta_v1.Condition, 0, len(conditions))

	// Progressing
	initialized := meta.FindStatusCondition(conditions, "Initialized")
	meta.SetStatusCondition(&result, meta_v1.Condition{
		Type:               fmt.Sprintf("%s/%s", typePrefix, "Progressing"),
		Status:             makeCondition(initialized != nil && initialized.Status == meta_v1.ConditionTrue),
		ObservedGeneration: obj.GetGeneration(),
		Reason:             makeReason(initialized),
		Message:            makeMessage(initialized),
	})

	// Degraded
	errorCondition := meta.FindStatusCondition(conditions, "Error")
	meta.SetStatusCondition(&result, meta_v1.Condition{
		Type:               fmt.Sprintf("%s/%s", typePrefix, "Degraded"),
		Status:             makeCondition(errorCondition != nil && errorCondition.Status == meta_v1.ConditionTrue),
		ObservedGeneration: obj.GetGeneration(),
		Reason:             makeReason(errorCondition),
		Message:            makeMessage(errorCondition),
	})

	// Available
	running := meta.FindStatusCondition(conditions, "Running")
	meta.SetStatusCondition(&result, meta_v1.Condition{
		Type:               fmt.Sprintf("%s/%s", typePrefix, "Available"),
		Status:             makeCondition(running != nil && running.Status == meta_v1.ConditionTrue),
		ObservedGeneration: obj.GetGeneration(),
		Reason:             makeReason(running),
		Message:            makeMessage(running),
	})

	return result
}

func (r *ValkeyReconciler) Delete(obj *v1.Valkey) ([]action.Action, ctrl.Result, error) {
	var actions []action.Action

	serviceIntegration := resourcecreator.MinimalServiceIntegration(obj)
	actions = append(actions, action.DeleteIfExists(serviceIntegration, obj, serviceIntegrationConditionGetter, r.Recorder))

	aivenValkey := resourcecreator.MinimalAivenValkey(obj)
	actions = append(actions, action.DeleteIfExists(aivenValkey, obj, aivenValkeyConditionGetter, r.Recorder))

	return actions, ctrl.Result{}, nil
}
