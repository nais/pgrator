package controller

import (
	"context"
	"fmt"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/controller/resourcecreator"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	aiven_v1alpha1 "github.com/nais/pgrator/internal/thirdparty/aiven/v1alpha1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
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

	aivenOpenSearch, err := resourcecreator.CreateAivenOpenSearchSpec(r.Scheme, obj, r.Aiven, r.Tenant)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating Aiven OpenSearch spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(aivenOpenSearch, obj, aivenOpenSearchConditionGetter, r.Recorder))

	serviceIntegration, err := resourcecreator.CreateOpenSearchServiceIntegrationSpec(r.Scheme, obj, r.Aiven)
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

func (r *OpenSearchReconciler) Delete(obj *v1.OpenSearch, _ OpenSearchPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	// TODO: refuse deletion if the resource does not have some annotation set
	//  this should also be checked by a validating webhook for immediate feedback

	var actions []action.Action

	// TODO: terminationProtection is enabled;
	//  the deletion process should:
	//  - disable terminationProtection - requires Update action on the OpenSearch resource
	//  - block (set a finalizer?) until aiven-operator has propagated the setting which we need to observe via the OpenSearch's status conditions
	//  - once propagated (remove finalizer and) delete the child resources

	serviceIntegration := resourcecreator.MinimalOpenSearchServiceIntegration(obj)
	actions = append(actions, action.DeleteIfExists(serviceIntegration, obj, openSearchServiceIntegrationConditionGetter, r.Recorder))

	aivenOpenSearch := resourcecreator.MinimalAivenOpenSearch(obj)
	actions = append(actions, action.DeleteIfExists(aivenOpenSearch, obj, aivenOpenSearchConditionGetter, r.Recorder))

	return actions, ctrl.Result{}, nil
}
