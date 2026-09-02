package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/nais/pgrator/internal/config"
	rcopensearch "github.com/nais/pgrator/internal/resourcecreator/opensearch"
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

// OpenSearchReconciler reconciles an OpenSearch object
type OpenSearchReconciler struct {
	Aiven  config.Aiven
	Tenant config.Tenant

	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

var _ reconciler.Reconciler[*v1.OpenSearch, OpenSearchPreparedData] = &OpenSearchReconciler{}

// OpenSearchPreparedData contains data prepared during the Prepare phase
type OpenSearchPreparedData struct{}

func (r *OpenSearchReconciler) Name() string {
	return "opensearch.nais.io"
}

func (r *OpenSearchReconciler) New() *v1.OpenSearch {
	return &v1.OpenSearch{}
}

func (r *OpenSearchReconciler) FinalizerName() string {
	return "opensearch.nais.io/finalizer"
}

func (r *OpenSearchReconciler) Prepare(_ context.Context, _ client.Reader, _ *v1.OpenSearch) (OpenSearchPreparedData, ctrl.Result, error) {
	return OpenSearchPreparedData{}, ctrl.Result{}, nil
}

func (r *OpenSearchReconciler) OwnedTypes() []reconciler.OwnedType {
	return []reconciler.OwnedType{
		{
			Type: &aiven_v1alpha1.OpenSearch{},
			AdditionalPredicate: predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldObj, ok1 := e.ObjectOld.(*aiven_v1alpha1.OpenSearch)
					newObj, ok2 := e.ObjectNew.(*aiven_v1alpha1.OpenSearch)
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

func (r *OpenSearchReconciler) AdditionalTypes() []client.Object {
	return nil
}

func (r *OpenSearchReconciler) Update(obj *v1.OpenSearch, _ OpenSearchPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	var actions []action.Action

	aivenOpenSearch, err := rcopensearch.CreateSpec(r.Scheme, obj, r.Aiven, r.Tenant)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating Aiven OpenSearch spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(aivenOpenSearch, obj, aivenOpenSearchConditionGetter, r.Recorder))

	serviceIntegration, err := rcopensearch.CreateServiceIntegrationSpec(r.Scheme, obj, r.Aiven)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating ServiceIntegration spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(serviceIntegration, obj, openSearchServiceIntegrationConditionGetter, r.Recorder))

	return actions, ctrl.Result{}, nil
}

func aivenOpenSearchConditionGetter(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition {
	aivenOpenSearch := obj.(*aiven_v1alpha1.OpenSearch)

	state := aivenOpenSearch.Status.State

	return []meta_v1.Condition{
		{
			Type:               fmt.Sprintf("%s/%s", typePrefix(obj, scheme), "ObservedState"),
			Status:             makeCondition(state != ""),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             "Reconciled",
			Message:            fmt.Sprintf("OpenSearch is in state: %s", state),
		},
	}
}

func openSearchServiceIntegrationConditionGetter(_ client.Object, _ *runtime.Scheme) []meta_v1.Condition {
	return nil
}

func (r *OpenSearchReconciler) Delete(obj *v1.OpenSearch, _ OpenSearchPreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	if reason, ok := obj.CanBeDeleted(); !ok {
		r.Recorder.RecordEvent(obj, core_v1.EventTypeWarning, "DeletionRefused", "Deletion requires the annotation %q set to \"true\"", api.AllowDeletionAnnotation)
		return nil, ctrl.Result{}, fmt.Errorf("refusing to delete resource: %s", reason)
	}

	aivenOpenSearch := rcopensearch.Minimal(obj)
	existing := relatedObjects.GetMatching(aivenOpenSearch)
	if existing == nil {
		return nil, ctrl.Result{}, nil
	}

	existingOpenSearch, ok := existing.(*aiven_v1alpha1.OpenSearch)
	if !ok {
		return nil, ctrl.Result{}, fmt.Errorf("unexpected type for existing OpenSearch: %T", existing)
	}

	if existingOpenSearch.Spec.TerminationProtection != nil && *existingOpenSearch.Spec.TerminationProtection {
		existingOpenSearch.Spec.TerminationProtection = new(false)
		r.Recorder.RecordEvent(obj, core_v1.EventTypeNormal, "DisablingTerminationProtection", "Disabling termination protection before deletion")

		actions := []action.Action{
			action.Update(existingOpenSearch, obj, aivenOpenSearchConditionGetter, r.Recorder),
		}

		return actions, ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	serviceIntegration := rcopensearch.MinimalServiceIntegration(obj)
	actions := []action.Action{
		action.DeleteIfExists(serviceIntegration, obj, openSearchServiceIntegrationConditionGetter, r.Recorder),
		action.DeleteIfExists(aivenOpenSearch, obj, aivenOpenSearchConditionGetter, r.Recorder),
	}

	return actions, ctrl.Result{}, nil
}
