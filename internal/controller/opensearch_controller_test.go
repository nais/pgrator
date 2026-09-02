package controller

import (
	"context"
	"testing"

	"github.com/nais/pgrator/internal/config"
	rcopensearch "github.com/nais/pgrator/internal/resourcecreator/opensearch"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/events"
	reconcilerpkg "github.com/nais/pgrator/internal/synchronizer/reconciler"
	aiven_v1alpha1 "github.com/nais/pgrator/internal/thirdparty/aiven/v1alpha1"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	kevents "k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	_ reconcilerpkg.Reconciler[*v1.OpenSearch, OpenSearchPreparedData] = &OpenSearchReconciler{}
	_ reconcilerpkg.FinalizerNamer                                     = &OpenSearchReconciler{}
)

const stateRunning = "RUNNING"

func TestCreateAivenOpenSearchSpec(t *testing.T) {
	const (
		testOpenSearchName = "my-opensearch"
		testTeamName       = "my-team"
	)
	aiven := config.Aiven{Project: "test-project", ProjectVPCID: "vpc-123"}
	tenant := config.Tenant{Name: "test-tenant"}

	t.Run("creates basic opensearch with correct fields", func(t *testing.T) {
		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: testOpenSearchName, Namespace: testTeamName, Labels: map[string]string{"team": testTeamName}},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2, StorageGB: 80},
		}

		result, err := rcopensearch.CreateSpec(scheme.Scheme, opensearch, aiven, tenant)
		requireNoError(t, err)
		requireEqual(t, result.Spec.Project, "test-project", "project")
		requireEqual(t, result.Spec.Plan, "startup-4", "plan")
		requireEqual(t, result.Spec.ProjectVPCID, "vpc-123", "project vpc")
		requireEqual(t, result.Spec.DiskSpace, "80GiB", "disk space")
		requireNotNil(t, result.Spec.TerminationProtection, "terminationProtection")
		requireTrue(t, *result.Spec.TerminationProtection, "terminationProtection should be true")
		requireNotNil(t, result.Spec.UserConfig, "user config")
		requireNotNil(t, result.Spec.UserConfig.OpenSearchVersion, "opensearch version")
		requireEqual(t, *result.Spec.UserConfig.OpenSearchVersion, "2", "opensearch version")
		requireEqual(t, result.Spec.Tags["team"], testTeamName, "team tag")
		requireEqual(t, result.Spec.Tags["app"], testOpenSearchName, "app tag")
		requireEqual(t, result.Spec.Tags["tenant"], "test-tenant", "tenant tag")
		requireEqual(t, result.Name, "opensearch-"+testTeamName+"-"+testOpenSearchName, "namespaced name")
	})

	t.Run("sets correct version for V2_19", func(t *testing.T) {
		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "versioned-opensearch", Namespace: "version-team"},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2_19, StorageGB: 80},
		}

		result, err := rcopensearch.CreateSpec(scheme.Scheme, opensearch, aiven, tenant)
		requireNoError(t, err)
		requireNotNil(t, result.Spec.UserConfig, "user config")
		requireNotNil(t, result.Spec.UserConfig.OpenSearchVersion, "opensearch version")
		requireEqual(t, *result.Spec.UserConfig.OpenSearchVersion, "2.19", "opensearch version")
	})

	t.Run("sets correct version for V3_3", func(t *testing.T) {
		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "v3-opensearch", Namespace: "v3-team"},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierHighAvailability, Memory: v1.OpenSearchMemory8GB, Version: v1.OpenSearchVersionV3_3, StorageGB: 525},
		}

		result, err := rcopensearch.CreateSpec(scheme.Scheme, opensearch, aiven, tenant)
		requireNoError(t, err)
		requireEqual(t, result.Spec.Plan, "business-8", "plan")
		requireEqual(t, result.Spec.DiskSpace, "525GiB", "disk space")
		requireNotNil(t, result.Spec.UserConfig, "user config")
		requireNotNil(t, result.Spec.UserConfig.OpenSearchVersion, "opensearch version")
		requireEqual(t, *result.Spec.UserConfig.OpenSearchVersion, "3.3", "opensearch version")
	})

	t.Run("does not set optional opensearch settings when omitted", func(t *testing.T) {
		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "basic-opensearch", Namespace: "basic-team"},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2, StorageGB: 80},
		}

		result, err := rcopensearch.CreateSpec(scheme.Scheme, opensearch, aiven, tenant)
		requireNoError(t, err)
		requireNotNil(t, result.Spec.UserConfig, "user config")
		requireNil(t, result.Spec.UserConfig.OpenSearch, "opensearch settings should be nil")
	})

	t.Run("sets optional opensearch settings when specified", func(t *testing.T) {
		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "configured-opensearch", Namespace: "config-team"},
			Spec: v1.OpenSearchSpec{
				Tier:                  v1.OpenSearchTierSingleNode,
				Memory:                v1.OpenSearchMemory4GB,
				Version:               v1.OpenSearchVersionV2,
				StorageGB:             80,
				ShardIndexingPressure: &v1.OpenSearchShardIndexingPressure{Enabled: true, Enforced: true},
				Indices:               &v1.OpenSearchIndices{QueryBoolMaxClauseCount: new(2048)},
				Http:                  &v1.OpenSearchHttp{MaxContentLength: new(resource.MustParse("200Mi"))},
			},
		}

		result, err := rcopensearch.CreateSpec(scheme.Scheme, opensearch, aiven, tenant)
		requireNoError(t, err)
		requireNotNil(t, result.Spec.UserConfig, "user config")
		requireNotNil(t, result.Spec.UserConfig.OpenSearch, "opensearch settings")
		requireNotNil(t, result.Spec.UserConfig.OpenSearch.ShardIndexingPressure, "shard indexing pressure")
		requireTrue(t, *result.Spec.UserConfig.OpenSearch.ShardIndexingPressure.Enabled, "enabled")
		requireTrue(t, *result.Spec.UserConfig.OpenSearch.ShardIndexingPressure.Enforced, "enforced")
		requireEqual(t, *result.Spec.UserConfig.OpenSearch.IndicesQueryBoolMaxClauseCount, 2048, "max clause count")
		requireEqual(t, *result.Spec.UserConfig.OpenSearch.HttpMaxContentLength, 209715200, "max content length")
	})

	t.Run("creates hobbyist plan for 2GB memory", func(t *testing.T) {
		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: "hobbyist-opensearch", Namespace: "dev-team"},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory2GB, Version: v1.OpenSearchVersionV1, StorageGB: 16},
		}

		result, err := rcopensearch.CreateSpec(scheme.Scheme, opensearch, aiven, tenant)
		requireNoError(t, err)
		requireEqual(t, result.Spec.Plan, "hobbyist", "plan")
		requireEqual(t, result.Spec.DiskSpace, "16GiB", "disk space")
	})

	t.Run("preserves labels from source opensearch", func(t *testing.T) {
		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "labeled-opensearch",
				Namespace: "labeled-team",
				Labels: map[string]string{
					"team":         "labeled-team",
					"custom-label": "custom-value",
					"another":      "label",
				},
			},
			Spec: v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2, StorageGB: 80},
		}

		result, err := rcopensearch.CreateSpec(scheme.Scheme, opensearch, aiven, tenant)
		requireNoError(t, err)
		requireEqual(t, result.Labels["team"], "labeled-team", "team label")
		requireEqual(t, result.Labels["custom-label"], "custom-value", "custom label")
		requireEqual(t, result.Labels["another"], "label", "another label")
		requireEqual(t, result.Labels["opensearch.nais.io/name"], "labeled-opensearch", "name label")
		requireEqual(t, result.Name, "opensearch-labeled-team-labeled-opensearch", "namespaced name")
	})
}

func TestCreateOpenSearchServiceIntegrationSpec(t *testing.T) {
	opensearch := &v1.OpenSearch{
		ObjectMeta: metav1.ObjectMeta{Name: "my-opensearch", Namespace: "my-team", Labels: map[string]string{"team": "my-team"}},
		Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2, StorageGB: 80},
	}
	cfg := config.Aiven{Project: "test-project", MetricsDestinationEndpointID: "metrics-service"}

	result, err := rcopensearch.CreateServiceIntegrationSpec(scheme.Scheme, opensearch, cfg)
	requireNoError(t, err)
	requireEqual(t, result.Name, "opensearch-my-team-my-opensearch", "name")
	requireEqual(t, result.Namespace, "my-team", "namespace")
	requireEqual(t, result.Spec.Project, "test-project", "project")
	requireEqual(t, result.Spec.IntegrationType, "prometheus", "integration type")
	requireEqual(t, result.Spec.SourceServiceName, "opensearch-my-team-my-opensearch", "source service")
	requireEqual(t, result.Spec.DestinationEndpointID, "metrics-service", "destination endpoint")
}

func TestAivenOpenSearchConditionGetter(t *testing.T) {
	testCases := []struct {
		name        string
		state       string
		wantStatus  metav1.ConditionStatus
		wantMessage string
	}{
		{name: "non-empty state", state: stateRunning, wantStatus: metav1.ConditionTrue, wantMessage: "OpenSearch is in state: RUNNING"},
		{name: "empty state", state: "", wantStatus: metav1.ConditionFalse, wantMessage: "OpenSearch is in state: "},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			aivenOpenSearch := &aiven_v1alpha1.OpenSearch{Status: aiven_v1alpha1.ServiceStatus{State: tc.state}}
			aivenOpenSearch.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("OpenSearch"))

			conditions := aivenOpenSearchConditionGetter(aivenOpenSearch, scheme.Scheme)
			requireEqual(t, len(conditions), 1, "condition count")

			observedState := meta.FindStatusCondition(conditions, "opensearch.aiven.io/ObservedState")
			requireNotNil(t, observedState, "observed state condition")
			requireEqual(t, observedState.Status, tc.wantStatus, "status")
			requireEqual(t, observedState.Reason, "Reconciled", "reason")
			requireEqual(t, observedState.Message, tc.wantMessage, "message")
		})
	}
}

func TestOpenSearchServiceIntegrationConditionGetter(t *testing.T) {
	integration := &aiven_v1alpha1.ServiceIntegration{
		Status: aiven_v1alpha1.ServiceIntegrationStatus{
			Conditions: []metav1.Condition{{Type: stateRunning, Status: metav1.ConditionTrue, Reason: "CheckRunning", Message: "Integration is running"}},
		},
	}
	integration.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("ServiceIntegration"))

	conditions := openSearchServiceIntegrationConditionGetter(integration, scheme.Scheme)
	requireNil(t, conditions, "service integration conditions should be nil")
}

func TestOpenSearchStateChangeReconciliation(t *testing.T) {
	const opensearchStateTestNamespace = "opensearch-state-test"
	ensureNamespace(t, opensearchStateTestNamespace)
	controllerReconciler := newOpenSearchSynchronizer(scheme.Scheme)

	t.Run("updates condition when state changes from empty to RUNNING", func(t *testing.T) {
		opensearchName := "state-change-test"
		opensearchKey := types.NamespacedName{Name: opensearchName, Namespace: opensearchStateTestNamespace}
		aivenOpenSearchName := "opensearch-" + opensearchStateTestNamespace + "-" + opensearchName

		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: opensearchName, Namespace: opensearchStateTestNamespace},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2, StorageGB: 80},
		}
		requireNoError(t, k8sClient.Create(context.Background(), opensearch))
		t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), opensearch) })

		_, err := controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)

		aivenOpenSearch := &aiven_v1alpha1.OpenSearch{}
		requireNoError(t, k8sClient.Get(context.Background(), types.NamespacedName{Name: aivenOpenSearchName, Namespace: opensearchStateTestNamespace}, aivenOpenSearch))

		requireNoError(t, k8sClient.Get(context.Background(), opensearchKey, opensearch))
		initialCondition := meta.FindStatusCondition(opensearch.GetStatus().GetConditions(), "opensearch.aiven.io/ObservedState")
		requireNotNil(t, initialCondition, "initial condition")
		requireEqual(t, initialCondition.Status, metav1.ConditionFalse, "initial status")
		requireEqual(t, initialCondition.Message, "OpenSearch is in state: ", "initial message")
		requireFalse(t, initialCondition.LastTransitionTime.IsZero(), "initial transition time should be set")

		aivenOpenSearch.Status.State = stateRunning
		requireNoError(t, k8sClient.Status().Update(context.Background(), aivenOpenSearch))

		_, err = controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)

		requireNoError(t, k8sClient.Get(context.Background(), opensearchKey, opensearch))
		updatedCondition := meta.FindStatusCondition(opensearch.GetStatus().GetConditions(), "opensearch.aiven.io/ObservedState")
		requireNotNil(t, updatedCondition, "updated condition")
		requireEqual(t, updatedCondition.Status, metav1.ConditionTrue, "updated status")
		requireEqual(t, updatedCondition.Message, "OpenSearch is in state: RUNNING", "updated message")
		requireFalse(t, updatedCondition.LastTransitionTime.IsZero(), "updated transition time should be set")
	})

	t.Run("does not update transition time when status remains true", func(t *testing.T) {
		opensearchName := "state-no-transition-test"
		opensearchKey := types.NamespacedName{Name: opensearchName, Namespace: opensearchStateTestNamespace}
		aivenOpenSearchName := "opensearch-" + opensearchStateTestNamespace + "-" + opensearchName

		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: opensearchName, Namespace: opensearchStateTestNamespace},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2, StorageGB: 80},
		}
		requireNoError(t, k8sClient.Create(context.Background(), opensearch))
		t.Cleanup(func() { _ = k8sClient.Delete(context.Background(), opensearch) })

		_, err := controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)

		aivenOpenSearch := &aiven_v1alpha1.OpenSearch{}
		requireNoError(t, k8sClient.Get(context.Background(), types.NamespacedName{Name: aivenOpenSearchName, Namespace: opensearchStateTestNamespace}, aivenOpenSearch))

		aivenOpenSearch.Status.State = "REBALANCING"
		requireNoError(t, k8sClient.Status().Update(context.Background(), aivenOpenSearch))

		_, err = controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)

		requireNoError(t, k8sClient.Get(context.Background(), opensearchKey, opensearch))
		rebalancingCondition := meta.FindStatusCondition(opensearch.GetStatus().GetConditions(), "opensearch.aiven.io/ObservedState")
		requireNotNil(t, rebalancingCondition, "rebalancing condition")
		requireEqual(t, rebalancingCondition.Status, metav1.ConditionTrue, "rebalancing status")
		requireEqual(t, rebalancingCondition.Message, "OpenSearch is in state: REBALANCING", "rebalancing message")
		rebalancingTransitionTime := rebalancingCondition.LastTransitionTime

		requireNoError(t, k8sClient.Get(context.Background(), types.NamespacedName{Name: aivenOpenSearchName, Namespace: opensearchStateTestNamespace}, aivenOpenSearch))
		aivenOpenSearch.Status.State = stateRunning
		requireNoError(t, k8sClient.Status().Update(context.Background(), aivenOpenSearch))

		_, err = controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)

		requireNoError(t, k8sClient.Get(context.Background(), opensearchKey, opensearch))
		runningCondition := meta.FindStatusCondition(opensearch.GetStatus().GetConditions(), "opensearch.aiven.io/ObservedState")
		requireNotNil(t, runningCondition, "running condition")
		requireEqual(t, runningCondition.Status, metav1.ConditionTrue, "running status")
		requireEqual(t, runningCondition.Message, "OpenSearch is in state: RUNNING", "running message")
		requireEqual(t, runningCondition.LastTransitionTime.Time, rebalancingTransitionTime.Time, "transition time should not change")
	})
}

func TestMinimalAivenOpenSearch(t *testing.T) {
	opensearch := &v1.OpenSearch{ObjectMeta: metav1.ObjectMeta{Name: "test-opensearch", Namespace: "test-ns", Labels: map[string]string{"team": "test-team"}}}
	result := rcopensearch.Minimal(opensearch)
	requireEqual(t, result.Name, "opensearch-test-ns-test-opensearch", "name")
	requireEqual(t, result.Namespace, "test-ns", "namespace")
	requireEqual(t, result.Kind, "OpenSearch", "kind")
	requireEqual(t, result.APIVersion, "aiven.io/v1alpha1", "apiVersion")
}

func TestMinimalOpenSearchServiceIntegration(t *testing.T) {
	opensearch := &v1.OpenSearch{ObjectMeta: metav1.ObjectMeta{Name: "test-opensearch", Namespace: "test-ns"}}
	result := rcopensearch.MinimalServiceIntegration(opensearch)
	requireEqual(t, result.Name, "opensearch-test-ns-test-opensearch", "name")
	requireEqual(t, result.Namespace, "test-ns", "namespace")
	requireEqual(t, result.Kind, "ServiceIntegration", "kind")
	requireEqual(t, result.APIVersion, "aiven.io/v1alpha1", "apiVersion")
}

func TestOpenSearchMemoryAndTierPlanMapping(t *testing.T) {
	tiers := []struct {
		tier         v1.OpenSearchTier
		memory       v1.OpenSearchMemory
		expectedPlan string
	}{
		{v1.OpenSearchTierSingleNode, v1.OpenSearchMemory2GB, "hobbyist"},
		{v1.OpenSearchTierSingleNode, v1.OpenSearchMemory4GB, "startup-4"},
		{v1.OpenSearchTierSingleNode, v1.OpenSearchMemory8GB, "startup-8"},
		{v1.OpenSearchTierSingleNode, v1.OpenSearchMemory16GB, "startup-16"},
		{v1.OpenSearchTierSingleNode, v1.OpenSearchMemory32GB, "startup-32"},
		{v1.OpenSearchTierSingleNode, v1.OpenSearchMemory64GB, "startup-64"},
		{v1.OpenSearchTierHighAvailability, v1.OpenSearchMemory4GB, "business-4"},
		{v1.OpenSearchTierHighAvailability, v1.OpenSearchMemory8GB, "business-8"},
		{v1.OpenSearchTierHighAvailability, v1.OpenSearchMemory16GB, "business-16"},
		{v1.OpenSearchTierHighAvailability, v1.OpenSearchMemory32GB, "business-32"},
		{v1.OpenSearchTierHighAvailability, v1.OpenSearchMemory64GB, "business-64"},
	}

	aiven := config.Aiven{Project: "test-project"}
	tenant := config.Tenant{Name: "test-tenant"}

	for _, tc := range tiers {
		t.Run(string(tc.tier)+"/"+string(tc.memory), func(t *testing.T) {
			storage := 80
			if tc.memory == v1.OpenSearchMemory2GB {
				storage = 16
			} else if tc.tier == v1.OpenSearchTierHighAvailability {
				switch tc.memory {
				case v1.OpenSearchMemory4GB:
					storage = 240
				case v1.OpenSearchMemory8GB:
					storage = 525
				case v1.OpenSearchMemory16GB:
					storage = 1050
				case v1.OpenSearchMemory32GB:
					storage = 2100
				case v1.OpenSearchMemory64GB:
					storage = 4200
				}
			} else {
				switch tc.memory {
				case v1.OpenSearchMemory8GB:
					storage = 175
				case v1.OpenSearchMemory16GB:
					storage = 350
				case v1.OpenSearchMemory32GB:
					storage = 700
				case v1.OpenSearchMemory64GB:
					storage = 1400
				}
			}

			opensearch := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{Name: "test-opensearch", Namespace: "test-ns"},
				Spec:       v1.OpenSearchSpec{Tier: tc.tier, Memory: tc.memory, Version: v1.OpenSearchVersionV2, StorageGB: storage},
			}
			result, err := rcopensearch.CreateSpec(scheme.Scheme, opensearch, aiven, tenant)
			requireNoError(t, err)
			requireEqual(t, result.Spec.Plan, tc.expectedPlan, "plan")
		})
	}
}

func TestOpenSearchReconcileLifecycle(t *testing.T) {
	const testNamespace = "os-sync-integration-ns"
	ensureNamespace(t, testNamespace)
	reconciler := newOpenSearchSynchronizer(scheme.Scheme)

	t.Run("sets reconcile status completed after first reconcile", func(t *testing.T) {
		testOpenSearchName := "sync-first-reconcile"
		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: testOpenSearchName, Namespace: testNamespace},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2, StorageGB: 80},
		}
		requireNoError(t, k8sClient.Create(ctx, opensearch))
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, opensearch) })

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: opensearch.Name, Namespace: opensearch.Namespace}}
		_, err := reconciler.Reconcile(ctx, req)
		requireNoError(t, err)

		updatedOpenSearch := &v1.OpenSearch{}
		requireNoError(t, k8sClient.Get(ctx, req.NamespacedName, updatedOpenSearch))
		requireNotNil(t, updatedOpenSearch.Status, "status")
		requireEqual(t, updatedOpenSearch.Status.ReconcilePhase, "Completed", "reconcile phase")
		requireSliceContains(t, updatedOpenSearch.GetFinalizers(), "opensearch.nais.io/finalizer")
		requireEqual(t, updatedOpenSearch.Status.ObservedGeneration, updatedOpenSearch.Generation, "observed generation")
	})

	t.Run("reaches completed after spec update reconciliation", func(t *testing.T) {
		testOpenSearchName := "sync-spec-update"
		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: testOpenSearchName, Namespace: testNamespace},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2, StorageGB: 80},
		}
		requireNoError(t, k8sClient.Create(ctx, opensearch))
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, opensearch) })

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: opensearch.Name, Namespace: opensearch.Namespace}}
		_, err := reconciler.Reconcile(ctx, req)
		requireNoError(t, err)

		firstOpenSearch := &v1.OpenSearch{}
		requireNoError(t, k8sClient.Get(ctx, req.NamespacedName, firstOpenSearch))
		requireEqual(t, firstOpenSearch.Status.ReconcilePhase, "Completed", "reconcile phase after first reconcile")
		initialGeneration := firstOpenSearch.Generation

		firstOpenSearch.Spec.StorageGB = 90
		requireNoError(t, k8sClient.Update(ctx, firstOpenSearch))

		requireNoError(t, k8sClient.Get(ctx, req.NamespacedName, firstOpenSearch))
		requireTrue(t, firstOpenSearch.Generation > initialGeneration, "generation should increase after spec update")

		_, err = reconciler.Reconcile(ctx, req)
		requireNoError(t, err)

		updatedOpenSearch := &v1.OpenSearch{}
		requireNoError(t, k8sClient.Get(ctx, req.NamespacedName, updatedOpenSearch))
		requireNotNil(t, updatedOpenSearch.Status, "status")
		requireEqual(t, updatedOpenSearch.Status.ReconcilePhase, "Completed", "reconcile phase")
		requireEqual(t, updatedOpenSearch.Status.ObservedGeneration, updatedOpenSearch.Generation, "observed generation")
	})
}

func TestOpenSearchDeletion(t *testing.T) {
	const deleteTestNamespace = "opensearch-delete-test"
	ensureNamespace(t, deleteTestNamespace)
	syncReconciler := newOpenSearchSynchronizer(scheme.Scheme)

	t.Run("refuses deletion without allowDeletion annotation", func(t *testing.T) {
		opensearchName := "delete-no-annotation"
		opensearchKey := types.NamespacedName{Name: opensearchName, Namespace: deleteTestNamespace}

		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: opensearchName, Namespace: deleteTestNamespace},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2, StorageGB: 80},
		}
		requireNoError(t, k8sClient.Create(ctx, opensearch))
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, opensearch) })

		_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)

		requireNoError(t, k8sClient.Get(ctx, opensearchKey, opensearch))
		requireNoError(t, k8sClient.Delete(ctx, opensearch))

		_, err = syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: opensearchKey})
		requireErrorContains(t, err, "refusing to delete")

		requireNoError(t, k8sClient.Get(ctx, opensearchKey, opensearch))
		requireSliceContains(t, opensearch.GetFinalizers(), "opensearch.nais.io/finalizer")
	})

	t.Run("disables terminationProtection before deleting child resources", func(t *testing.T) {
		opensearchName := "delete-with-protection"
		opensearchKey := types.NamespacedName{Name: opensearchName, Namespace: deleteTestNamespace}
		aivenOpenSearchName := "opensearch-" + deleteTestNamespace + "-" + opensearchName

		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: opensearchName, Namespace: deleteTestNamespace, Annotations: map[string]string{api.AllowDeletionAnnotation: "true"}},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2, StorageGB: 80},
		}
		requireNoError(t, k8sClient.Create(ctx, opensearch))

		_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)

		aivenOpenSearch := &aiven_v1alpha1.OpenSearch{}
		requireNoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: aivenOpenSearchName, Namespace: deleteTestNamespace}, aivenOpenSearch))
		requireNotNil(t, aivenOpenSearch.Spec.TerminationProtection, "terminationProtection")
		requireTrue(t, *aivenOpenSearch.Spec.TerminationProtection, "terminationProtection should be true")

		requireNoError(t, k8sClient.Get(ctx, opensearchKey, opensearch))
		requireNoError(t, k8sClient.Delete(ctx, opensearch))

		result, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)
		requireTrue(t, result.RequeueAfter > 0, "expected requeue while disabling termination protection")

		requireNoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: aivenOpenSearchName, Namespace: deleteTestNamespace}, aivenOpenSearch))
		requireFalse(t, *aivenOpenSearch.Spec.TerminationProtection, "terminationProtection should be disabled")

		requireNoError(t, k8sClient.Get(ctx, opensearchKey, opensearch))
		requireSliceContains(t, opensearch.GetFinalizers(), "opensearch.nais.io/finalizer")

		result, err = syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)
		requireEqual(t, result.RequeueAfter, 0, "requeue after should be zero")

		err = k8sClient.Get(ctx, types.NamespacedName{Name: aivenOpenSearchName, Namespace: deleteTestNamespace}, aivenOpenSearch)
		requireTrue(t, apierrors.IsNotFound(err), "aiven opensearch should be deleted")

		err = k8sClient.Get(ctx, opensearchKey, opensearch)
		requireTrue(t, apierrors.IsNotFound(err), "opensearch should be garbage collected")
	})

	t.Run("skips terminationProtection dance when Aiven resource does not exist", func(t *testing.T) {
		opensearchName := "delete-no-aiven"
		opensearchKey := types.NamespacedName{Name: opensearchName, Namespace: deleteTestNamespace}

		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: opensearchName, Namespace: deleteTestNamespace, Annotations: map[string]string{api.AllowDeletionAnnotation: "true"}},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2, StorageGB: 80},
		}
		requireNoError(t, k8sClient.Create(ctx, opensearch))

		_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)

		aivenOpenSearchName := "opensearch-" + deleteTestNamespace + "-" + opensearchName
		aivenOpenSearch := &aiven_v1alpha1.OpenSearch{}
		requireNoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: aivenOpenSearchName, Namespace: deleteTestNamespace}, aivenOpenSearch))
		requireNoError(t, k8sClient.Delete(ctx, aivenOpenSearch))

		requireNoError(t, k8sClient.Get(ctx, opensearchKey, opensearch))
		requireNoError(t, k8sClient.Delete(ctx, opensearch))

		result, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)
		requireEqual(t, result.RequeueAfter, 0, "requeue after should be zero")

		err = k8sClient.Get(ctx, opensearchKey, opensearch)
		requireTrue(t, apierrors.IsNotFound(err), "opensearch should be garbage collected")
	})

	t.Run("deletes when terminationProtection is already disabled", func(t *testing.T) {
		opensearchName := "delete-no-protection"
		opensearchKey := types.NamespacedName{Name: opensearchName, Namespace: deleteTestNamespace}
		aivenOpenSearchName := "opensearch-" + deleteTestNamespace + "-" + opensearchName

		opensearch := &v1.OpenSearch{
			ObjectMeta: metav1.ObjectMeta{Name: opensearchName, Namespace: deleteTestNamespace, Annotations: map[string]string{api.AllowDeletionAnnotation: "true"}},
			Spec:       v1.OpenSearchSpec{Tier: v1.OpenSearchTierSingleNode, Memory: v1.OpenSearchMemory4GB, Version: v1.OpenSearchVersionV2, StorageGB: 80},
		}
		requireNoError(t, k8sClient.Create(ctx, opensearch))

		_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)

		aivenOpenSearch := &aiven_v1alpha1.OpenSearch{}
		requireNoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: aivenOpenSearchName, Namespace: deleteTestNamespace}, aivenOpenSearch))
		aivenOpenSearch.Spec.TerminationProtection = new(false)
		requireNoError(t, k8sClient.Update(ctx, aivenOpenSearch))

		requireNoError(t, k8sClient.Get(ctx, opensearchKey, opensearch))
		requireNoError(t, k8sClient.Delete(ctx, opensearch))

		result, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: opensearchKey})
		requireNoError(t, err)
		requireEqual(t, result.RequeueAfter, 0, "requeue after should be zero")

		err = k8sClient.Get(ctx, types.NamespacedName{Name: aivenOpenSearchName, Namespace: deleteTestNamespace}, aivenOpenSearch)
		requireTrue(t, apierrors.IsNotFound(err), "aiven opensearch should be deleted")

		err = k8sClient.Get(ctx, opensearchKey, opensearch)
		requireTrue(t, apierrors.IsNotFound(err), "opensearch should be garbage collected")
	})
}

func newOpenSearchSynchronizer(sch *runtime.Scheme) *synchronizer.Synchronizer[*v1.OpenSearch, OpenSearchPreparedData] {
	testRecorder := events.NewRecorder(kevents.NewFakeRecorder(100))
	opensearchReconciler := &OpenSearchReconciler{
		Aiven: config.Aiven{
			Project:                      "test-project",
			ProjectVPCID:                 "test-vpc-id",
			MetricsDestinationEndpointID: "test-metrics-service",
		},
		Tenant:   config.Tenant{Name: "test-tenant"},
		Recorder: testRecorder,
		Scheme:   sch,
	}
	return synchronizer.NewSynchronizer(k8sClient, sch, opensearchReconciler, testRecorder)
}
