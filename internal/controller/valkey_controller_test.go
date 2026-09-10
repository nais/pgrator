package controller

import (
	"context"
	"testing"

	"github.com/nais/pgrator/internal/config"
	rcvalkey "github.com/nais/pgrator/internal/resourcecreator/valkey"
	"github.com/nais/pgrator/internal/synchronizer"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	reconcilerpkg "github.com/nais/pgrator/internal/synchronizer/reconciler"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	aiven_v1alpha1 "github.com/nais/pgrator/internal/thirdparty/aiven/v1alpha1"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	kevents "k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	_ reconcilerpkg.Reconciler[*v1.Valkey, ValkeyPreparedData] = &ValkeyReconciler{}
	_ reconcilerpkg.FinalizerNamer                             = &ValkeyReconciler{}
)

func TestCreateAivenValkeySpec(t *testing.T) {
	const (
		testValkeyName = "my-valkey"
		testTeamName   = "my-team"
	)
	aiven := config.Aiven{Project: "test-project", ProjectVPCID: "vpc-123"}
	tenant := config.Tenant{Name: "test-tenant"}

	t.Run("creates basic valkey with correct fields", func(t *testing.T) {
		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: testValkeyName, Namespace: testTeamName, Labels: map[string]string{"team": testTeamName}},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1},
		}

		result, err := rcvalkey.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
		requireNoError(t, err)
		requireEqual(t, result.Spec.Project, "test-project", "project")
		requireEqual(t, result.Spec.Plan, "startup-4", "plan")
		requireEqual(t, result.Spec.ProjectVPCID, "vpc-123", "project vpc")
		requireNotNil(t, result.Spec.TerminationProtection, "terminationProtection")
		requireTrue(t, *result.Spec.TerminationProtection, "terminationProtection should be true")
		requireNotNil(t, result.Spec.UserConfig, "user config")
		requireEqual(t, *result.Spec.UserConfig.ValkeyVersion, "9.1", "valkey version")
		requireEqual(t, result.Spec.Tags["team"], testTeamName, "team tag")
		requireEqual(t, result.Spec.Tags["app"], testValkeyName, "app tag")
		requireEqual(t, result.Spec.Tags["tenant"], "test-tenant", "tenant tag")
		requireEqual(t, result.Name, "valkey-"+testTeamName+"-"+testValkeyName, "namespaced name")
	})

	t.Run("refuses to build a spec with an unresolved version", func(t *testing.T) {
		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: testValkeyName, Namespace: testTeamName},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB},
		}

		_, err := rcvalkey.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
		requireErrorContains(t, err, "spec.version is unset")
	})

	t.Run("sets maxMemoryPolicy in user config", func(t *testing.T) {
		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: "configured-valkey", Namespace: "config-team"},
			Spec: v1.ValkeySpec{
				Tier:            v1.ValkeyTierSingleNode,
				Memory:          v1.ValkeyMemory4GB,
				Version:         v1.ValkeyVersionV9_1,
				MaxMemoryPolicy: v1.ValkeyMaxMemoryPolicyVolatileLRU,
			},
		}

		result, err := rcvalkey.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
		requireNoError(t, err)
		requireNotNil(t, result.Spec.UserConfig, "user config")
		requireNotNil(t, result.Spec.UserConfig.ValkeyMaxmemoryPolicy, "maxmemory policy")
		requireEqual(t, *result.Spec.UserConfig.ValkeyMaxmemoryPolicy, "volatile-lru", "maxmemory policy")
	})

	t.Run("sets notifyKeyspaceEvents in user config", func(t *testing.T) {
		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: "events-valkey", Namespace: "events-team"},
			Spec: v1.ValkeySpec{
				Tier:                 v1.ValkeyTierSingleNode,
				Memory:               v1.ValkeyMemory4GB,
				Version:              v1.ValkeyVersionV9_1,
				NotifyKeyspaceEvents: "KEA",
			},
		}

		result, err := rcvalkey.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
		requireNoError(t, err)
		requireNotNil(t, result.Spec.UserConfig, "user config")
		requireNotNil(t, result.Spec.UserConfig.ValkeyNotifyKeyspaceEvents, "notify keyspace events")
		requireEqual(t, *result.Spec.UserConfig.ValkeyNotifyKeyspaceEvents, "KEA", "notify keyspace events")
	})

	t.Run("sets all user config options", func(t *testing.T) {
		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: "full-config-valkey", Namespace: "full-team"},
			Spec: v1.ValkeySpec{
				Tier:                 v1.ValkeyTierHighAvailability,
				Memory:               v1.ValkeyMemory14GB,
				Version:              v1.ValkeyVersionV8_1,
				MaxMemoryPolicy:      v1.ValkeyMaxMemoryPolicyAllkeysLFU,
				NotifyKeyspaceEvents: "Ex",
				Persistence:          &v1.ValkeyPersistence{Disabled: true},
				Databases:            new(32),
			},
		}

		result, err := rcvalkey.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
		requireNoError(t, err)
		requireEqual(t, result.Spec.Plan, "business-14", "plan")
		requireNotNil(t, result.Spec.UserConfig, "user config")
		requireEqual(t, *result.Spec.UserConfig.ValkeyVersion, "8.1", "valkey version")
		requireEqual(t, *result.Spec.UserConfig.ValkeyMaxmemoryPolicy, "allkeys-lfu", "maxmemory policy")
		requireEqual(t, *result.Spec.UserConfig.ValkeyNotifyKeyspaceEvents, "Ex", "notify events")
		requireEqual(t, *result.Spec.UserConfig.ValkeyPersistence, "off", "persistence")
		requireEqual(t, *result.Spec.UserConfig.ValkeyNumberOfDatabases, 32, "databases")
	})

	t.Run("preserves labels from source valkey", func(t *testing.T) {
		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "labeled-valkey",
				Namespace: "labeled-team",
				Labels: map[string]string{
					"team":         "labeled-team",
					"custom-label": "custom-value",
					"another":      "label",
				},
			},
			Spec: v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1},
		}

		result, err := rcvalkey.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
		requireNoError(t, err)
		requireEqual(t, result.Labels["team"], "labeled-team", "team label")
		requireEqual(t, result.Labels["custom-label"], "custom-value", "custom label")
		requireEqual(t, result.Labels["another"], "label", "another label")
		requireEqual(t, result.Labels["valkey.nais.io/name"], "labeled-valkey", "name label")
		requireEqual(t, result.Name, "valkey-labeled-team-labeled-valkey", "namespaced name")
	})
}

// Exercises the version Update resolves against what Aiven reports as running: the value it puts
// on the object, the one it sends in user config, and whether it asks for the change to be
// recorded. Recording itself is covered by TestRecordValkeyVersion.
func TestValkeyVersionAdoption(t *testing.T) {
	const adoptionNamespace = "adoption-team"

	reconciler := &ValkeyReconciler{
		Aiven:    config.Aiven{Project: "test-project"},
		Tenant:   config.Tenant{Name: "test-tenant"},
		Recorder: events.NewRecorder(kevents.NewFakeRecorder(100)),
		Scheme:   scheme.Scheme,
	}

	runningAt := func(valkeyName, reportedVersion string) reconcilerpkg.RelatedObjects {
		related := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
		related.Insert(&aiven_v1alpha1.Valkey{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "valkey-" + adoptionNamespace + "-" + valkeyName,
				Namespace: adoptionNamespace,
			},
			Status: aiven_v1alpha1.ServiceStatus{State: stateRunning, Version: reportedVersion},
		})
		return related
	}

	newValkeyAt := func(name string, version v1.ValkeyVersion) *v1.Valkey {
		return &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: adoptionNamespace},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: version},
		}
	}

	tests := []struct {
		name            string
		specVersion     v1.ValkeyVersion
		reportedVersion string
		want            v1.ValkeyVersion
	}{
		{name: "legacy object adopts the running version", specVersion: "", reportedVersion: "9.1.2", want: v1.ValkeyVersionV9_1},
		{name: "pin follows an Aiven-initiated upgrade", specVersion: v1.ValkeyVersionV9_0, reportedVersion: "9.1.2", want: v1.ValkeyVersionV9_1},
		{name: "pin is kept while its upgrade is still pending", specVersion: v1.ValkeyVersionV9_1, reportedVersion: "8.1.4", want: v1.ValkeyVersionV9_1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valkey := newValkeyAt("adopting", tt.specVersion)

			actions, _, err := reconciler.Update(valkey, ValkeyPreparedData{}, runningAt("adopting", tt.reportedVersion))
			requireNoError(t, err)
			requireEqual(t, valkey.Spec.Version, tt.want, "version recorded on the object")
			requireEqual(t, aivenValkeyUserConfigVersion(t, actions), string(tt.want), "version configured at Aiven")

			adopted := tt.specVersion != tt.want
			requireEqual(t, hasRecordVersionAction(actions, tt.want), adopted, "record-version action emitted")
		})
	}

	t.Run("refuses a running version that is newer but not a legal step", func(t *testing.T) {
		valkey := newValkeyAt("blocked", v1.ValkeyVersionV8_1)

		_, _, err := reconciler.Update(valkey, ValkeyPreparedData{}, runningAt("blocked", "9.0.4"))
		requireErrorContains(t, err, "which cannot be recorded")
	})
}

func TestRecordValkeyVersion(t *testing.T) {
	const recordNamespace = "record-version-test"
	ensureNamespace(t, recordNamespace)

	create := func(t *testing.T, name string, version v1.ValkeyVersion) *v1.Valkey {
		t.Helper()
		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: recordNamespace},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: version},
		}
		requireNoError(t, k8sClient.Create(ctx, valkey))
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, valkey) })
		return valkey
	}

	get := func(t *testing.T, valkey *v1.Valkey) *v1.Valkey {
		t.Helper()
		stored := &v1.Valkey{}
		requireNoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: valkey.Name, Namespace: valkey.Namespace}, stored))
		return stored
	}

	t.Run("records the version on the stored object", func(t *testing.T) {
		valkey := create(t, "adopts", "")

		err := (&recordValkeyVersion{valkey: valkey.DeepCopy(), version: v1.ValkeyVersionV9_1, recorder: events.NewRecorder(kevents.NewFakeRecorder(10))}).Do(ctx, k8sClient, scheme.Scheme, nil)
		requireNoError(t, err)

		requireEqual(t, get(t, valkey).Spec.Version, v1.ValkeyVersionV9_1, "recorded version")
	})

	// The whole point of re-reading: the copy the action carries is stale by the time it runs, and
	// must not be written back over whatever the object looks like now.
	t.Run("preserves changes made after the action was built", func(t *testing.T) {
		valkey := create(t, "concurrently-edited", "")
		stale := valkey.DeepCopy()

		current := get(t, valkey)
		current.Spec.Memory = v1.ValkeyMemory8GB
		requireNoError(t, k8sClient.Update(ctx, current))

		err := (&recordValkeyVersion{valkey: stale, version: v1.ValkeyVersionV9_1, recorder: events.NewRecorder(kevents.NewFakeRecorder(10))}).Do(ctx, k8sClient, scheme.Scheme, nil)
		requireNoError(t, err)

		stored := get(t, valkey)
		requireEqual(t, stored.Spec.Version, v1.ValkeyVersionV9_1, "recorded version")
		requireEqual(t, stored.Spec.Memory, v1.ValkeyMemory8GB, "memory set after the action was built")
	})

	t.Run("writes nothing when the stored version already matches", func(t *testing.T) {
		valkey := create(t, "already-current", v1.ValkeyVersionV9_1)
		before := get(t, valkey).ResourceVersion

		err := (&recordValkeyVersion{valkey: valkey.DeepCopy(), version: v1.ValkeyVersionV9_1, recorder: events.NewRecorder(kevents.NewFakeRecorder(10))}).Do(ctx, k8sClient, scheme.Scheme, nil)
		requireNoError(t, err)

		requireEqual(t, get(t, valkey).ResourceVersion, before, "resource version")
	})

	t.Run("ignores an object that no longer exists", func(t *testing.T) {
		valkey := &v1.Valkey{ObjectMeta: metav1.ObjectMeta{Name: "already-gone", Namespace: recordNamespace}}

		err := (&recordValkeyVersion{valkey: valkey, version: v1.ValkeyVersionV9_1, recorder: events.NewRecorder(kevents.NewFakeRecorder(10))}).Do(ctx, k8sClient, scheme.Scheme, nil)
		requireNoError(t, err)
	})
}

func TestValkeyAdoptionReconciliation(t *testing.T) {
	const adoptNamespace = "adoption-reconcile-test"
	ensureNamespace(t, adoptNamespace)
	syncReconciler := newValkeySynchronizer(scheme.Scheme)

	// Reconciles once so the Aiven Valkey exists, then has Aiven report a newer running version.
	reconciledValkeyOn := func(t *testing.T, name, reportedVersion string) types.NamespacedName {
		t.Helper()

		key := types.NamespacedName{Name: name, Namespace: adoptNamespace}
		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: adoptNamespace},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_0},
		}
		requireNoError(t, k8sClient.Create(ctx, valkey))
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, valkey) })

		_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		requireNoError(t, err)

		aivenValkey := &aiven_v1alpha1.Valkey{}
		aivenKey := types.NamespacedName{Name: "valkey-" + adoptNamespace + "-" + name, Namespace: adoptNamespace}
		requireNoError(t, k8sClient.Get(ctx, aivenKey, aivenValkey))
		aivenValkey.Status.State = stateRunning
		aivenValkey.Status.Version = reportedVersion
		requireNoError(t, k8sClient.Status().Update(ctx, aivenValkey))

		return key
	}

	t.Run("adopts an Aiven-initiated upgrade in a single reconcile", func(t *testing.T) {
		key := reconciledValkeyOn(t, "steady-state", "9.1.2")

		_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		requireNoError(t, err)

		stored := &v1.Valkey{}
		requireNoError(t, k8sClient.Get(ctx, key, stored))
		requireEqual(t, stored.Spec.Version, v1.ValkeyVersionV9_1, "adopted version")
	})

	// Recording the version bumps the object's resourceVersion mid-reconcile, so a metadata write in
	// the same pass is left holding a stale one. Only reachable without a finalizer already present.
	t.Run("converges when adoption coincides with a finalizer write", func(t *testing.T) {
		key := reconciledValkeyOn(t, "coinciding", "9.1.2")

		stored := &v1.Valkey{}
		requireNoError(t, k8sClient.Get(ctx, key, stored))
		stored.Finalizers = nil
		requireNoError(t, k8sClient.Update(ctx, stored))

		if _, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key}); err != nil {
			t.Logf("first reconcile failed as expected: %v", err)
		}

		_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		requireNoError(t, err)

		requireNoError(t, k8sClient.Get(ctx, key, stored))
		requireEqual(t, stored.Spec.Version, v1.ValkeyVersionV9_1, "adopted version after recovery")
		requireSliceContains(t, stored.GetFinalizers(), "valkey.nais.io/finalizer")
	})
}

func aivenValkeyUserConfigVersion(t *testing.T, actions []action.Action) string {
	t.Helper()

	for _, a := range actions {
		aivenValkey, ok := a.GetObject().(*aiven_v1alpha1.Valkey)
		if !ok {
			continue
		}
		requireNotNil(t, aivenValkey.Spec.UserConfig, "user config")
		requireNotNil(t, aivenValkey.Spec.UserConfig.ValkeyVersion, "valkey version")
		return *aivenValkey.Spec.UserConfig.ValkeyVersion
	}

	t.Fatal("no Aiven Valkey action was produced")
	return ""
}

func hasRecordVersionAction(actions []action.Action, version v1.ValkeyVersion) bool {
	for _, a := range actions {
		if record, ok := a.(*recordValkeyVersion); ok && record.version == version {
			return true
		}
	}
	return false
}

func TestCreateValkeyServiceIntegrationSpec(t *testing.T) {
	valkey := &v1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: "my-valkey", Namespace: "my-team", Labels: map[string]string{"team": "my-team"}},
		Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1},
	}
	cfg := config.Aiven{Project: "test-project", MetricsDestinationEndpointID: "metrics-service"}

	result, err := rcvalkey.CreateServiceIntegrationSpec(scheme.Scheme, valkey, cfg)
	requireNoError(t, err)
	requireEqual(t, result.Name, "valkey-my-team-my-valkey", "name")
	requireEqual(t, result.Namespace, "my-team", "namespace")
	requireEqual(t, result.Spec.Project, "test-project", "project")
	requireEqual(t, result.Spec.IntegrationType, "prometheus", "integration type")
	requireEqual(t, result.Spec.SourceServiceName, "valkey-my-team-my-valkey", "source service")
	requireEqual(t, result.Spec.DestinationEndpointID, "metrics-service", "destination endpoint")
}

func TestAivenValkeyConditionGetter(t *testing.T) {
	testCases := []struct {
		name          string
		state         string
		wantStatus    metav1.ConditionStatus
		wantMessage   string
		wantCondition string
	}{
		{name: "non-empty state", state: stateRunning, wantStatus: metav1.ConditionTrue, wantMessage: "Valkey is in state: RUNNING", wantCondition: "valkey.aiven.io/ObservedState"},
		{name: "empty state", state: "", wantStatus: metav1.ConditionFalse, wantMessage: "Valkey is in state: ", wantCondition: "valkey.aiven.io/ObservedState"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			aivenValkey := &aiven_v1alpha1.Valkey{Status: aiven_v1alpha1.ServiceStatus{State: tc.state}}
			aivenValkey.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("Valkey"))

			conditions := aivenValkeyConditionGetter(aivenValkey, scheme.Scheme)
			requireEqual(t, len(conditions), 1, "condition count")

			observedState := meta.FindStatusCondition(conditions, tc.wantCondition)
			requireNotNil(t, observedState, "observed state condition")
			requireEqual(t, observedState.Status, tc.wantStatus, "status")
			requireEqual(t, observedState.Reason, "Reconciled", "reason")
			requireEqual(t, observedState.Message, tc.wantMessage, "message")
		})
	}
}

func TestValkeyServiceIntegrationConditionGetter(t *testing.T) {
	integration := &aiven_v1alpha1.ServiceIntegration{
		Status: aiven_v1alpha1.ServiceIntegrationStatus{
			Conditions: []metav1.Condition{{Type: "Running", Status: metav1.ConditionTrue, Reason: "CheckRunning", Message: "Integration is running"}},
		},
	}
	integration.SetGroupVersionKind(aiven_v1alpha1.GroupVersion.WithKind("ServiceIntegration"))

	conditions := serviceIntegrationConditionGetter(integration, scheme.Scheme)
	requireNil(t, conditions, "service integration conditions should be nil")
}

func TestValkeyStateChangeReconciliation(t *testing.T) {
	const valkeyStateTestNamespace = "valkey-state-test"

	ensureNamespace(t, valkeyStateTestNamespace)
	controllerReconciler := newValkeySynchronizer(k8sClient.Scheme())

	t.Run("updates condition when state changes from empty to RUNNING", func(t *testing.T) {
		valkeyName := "state-change-test"
		valkeyKey := types.NamespacedName{Name: valkeyName, Namespace: valkeyStateTestNamespace}
		aivenValkeyName := "valkey-" + valkeyStateTestNamespace + "-" + valkeyName

		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: valkeyName, Namespace: valkeyStateTestNamespace},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1},
		}
		requireNoError(t, k8sClient.Create(context.Background(), valkey))
		t.Cleanup(func() {
			_ = k8sClient.Delete(context.Background(), valkey)
		})

		_, err := controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)

		aivenValkey := &aiven_v1alpha1.Valkey{}
		requireNoError(t, k8sClient.Get(context.Background(), types.NamespacedName{Name: aivenValkeyName, Namespace: valkeyStateTestNamespace}, aivenValkey))

		requireNoError(t, k8sClient.Get(context.Background(), valkeyKey, valkey))
		initialCondition := meta.FindStatusCondition(valkey.GetStatus().GetConditions(), "valkey.aiven.io/ObservedState")
		requireNotNil(t, initialCondition, "initial observed state condition")
		requireEqual(t, initialCondition.Status, metav1.ConditionFalse, "initial status")
		requireEqual(t, initialCondition.Message, "Valkey is in state: ", "initial message")
		requireFalse(t, initialCondition.LastTransitionTime.IsZero(), "initial transition time should be set")

		aivenValkey.Status.State = stateRunning
		requireNoError(t, k8sClient.Status().Update(context.Background(), aivenValkey))

		_, err = controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)

		requireNoError(t, k8sClient.Get(context.Background(), valkeyKey, valkey))
		updatedCondition := meta.FindStatusCondition(valkey.GetStatus().GetConditions(), "valkey.aiven.io/ObservedState")
		requireNotNil(t, updatedCondition, "updated observed state condition")
		requireEqual(t, updatedCondition.Status, metav1.ConditionTrue, "updated status")
		requireEqual(t, updatedCondition.Message, "Valkey is in state: RUNNING", "updated message")
		requireFalse(t, updatedCondition.LastTransitionTime.IsZero(), "updated transition time should be set")
	})

	t.Run("does not update transition time when status remains true", func(t *testing.T) {
		valkeyName := "state-no-transition-test"
		valkeyKey := types.NamespacedName{Name: valkeyName, Namespace: valkeyStateTestNamespace}
		aivenValkeyName := "valkey-" + valkeyStateTestNamespace + "-" + valkeyName

		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: valkeyName, Namespace: valkeyStateTestNamespace},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1},
		}
		requireNoError(t, k8sClient.Create(context.Background(), valkey))
		t.Cleanup(func() {
			_ = k8sClient.Delete(context.Background(), valkey)
		})

		_, err := controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)

		aivenValkey := &aiven_v1alpha1.Valkey{}
		requireNoError(t, k8sClient.Get(context.Background(), types.NamespacedName{Name: aivenValkeyName, Namespace: valkeyStateTestNamespace}, aivenValkey))

		aivenValkey.Status.State = "REBALANCING"
		requireNoError(t, k8sClient.Status().Update(context.Background(), aivenValkey))

		_, err = controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)

		requireNoError(t, k8sClient.Get(context.Background(), valkeyKey, valkey))
		rebalancingCondition := meta.FindStatusCondition(valkey.GetStatus().GetConditions(), "valkey.aiven.io/ObservedState")
		requireNotNil(t, rebalancingCondition, "rebalancing condition")
		requireEqual(t, rebalancingCondition.Status, metav1.ConditionTrue, "rebalancing status")
		requireEqual(t, rebalancingCondition.Message, "Valkey is in state: REBALANCING", "rebalancing message")
		rebalancingTransitionTime := rebalancingCondition.LastTransitionTime

		requireNoError(t, k8sClient.Get(context.Background(), types.NamespacedName{Name: aivenValkeyName, Namespace: valkeyStateTestNamespace}, aivenValkey))
		aivenValkey.Status.State = stateRunning
		requireNoError(t, k8sClient.Status().Update(context.Background(), aivenValkey))

		_, err = controllerReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)

		requireNoError(t, k8sClient.Get(context.Background(), valkeyKey, valkey))
		runningCondition := meta.FindStatusCondition(valkey.GetStatus().GetConditions(), "valkey.aiven.io/ObservedState")
		requireNotNil(t, runningCondition, "running condition")
		requireEqual(t, runningCondition.Status, metav1.ConditionTrue, "running status")
		requireEqual(t, runningCondition.Message, "Valkey is in state: RUNNING", "running message")
		requireEqual(t, runningCondition.LastTransitionTime.Time, rebalancingTransitionTime.Time, "transition time should not change")
	})
}

func TestMinimalAivenValkey(t *testing.T) {
	valkey := &v1.Valkey{ObjectMeta: metav1.ObjectMeta{Name: "test-valkey", Namespace: "test-ns", Labels: map[string]string{"team": "test-team"}}}
	result := rcvalkey.Minimal(valkey)
	requireEqual(t, result.Name, "valkey-test-ns-test-valkey", "name")
	requireEqual(t, result.Namespace, "test-ns", "namespace")
	requireEqual(t, result.Kind, "Valkey", "kind")
	requireEqual(t, result.APIVersion, "aiven.io/v1alpha1", "apiVersion")
}

func TestMinimalValkeyServiceIntegration(t *testing.T) {
	valkey := &v1.Valkey{ObjectMeta: metav1.ObjectMeta{Name: "test-valkey", Namespace: "test-ns"}}
	result := rcvalkey.MinimalServiceIntegration(valkey)
	requireEqual(t, result.Name, "valkey-test-ns-test-valkey", "name")
	requireEqual(t, result.Namespace, "test-ns", "namespace")
	requireEqual(t, result.Kind, "ServiceIntegration", "kind")
	requireEqual(t, result.APIVersion, "aiven.io/v1alpha1", "apiVersion")
}

func TestMaxMemoryPolicyHandling(t *testing.T) {
	policies := []v1.ValkeyMaxMemoryPolicy{
		v1.ValkeyMaxMemoryPolicyAllkeysLFU,
		v1.ValkeyMaxMemoryPolicyAllkeysLRU,
		v1.ValkeyMaxMemoryPolicyAllkeysRandom,
		v1.ValkeyMaxMemoryPolicyNoEviction,
		v1.ValkeyMaxMemoryPolicyVolatileLFU,
		v1.ValkeyMaxMemoryPolicyVolatileLRU,
		v1.ValkeyMaxMemoryPolicyVolatileRandom,
		v1.ValkeyMaxMemoryPolicyVolatileTTL,
	}

	aiven := config.Aiven{Project: "test-project"}
	tenant := config.Tenant{Name: "test-tenant"}

	for _, policy := range policies {
		t.Run(string(policy), func(t *testing.T) {
			valkey := &v1.Valkey{
				ObjectMeta: metav1.ObjectMeta{Name: "test-valkey", Namespace: "test-ns"},
				Spec: v1.ValkeySpec{
					Tier:            v1.ValkeyTierSingleNode,
					Memory:          v1.ValkeyMemory4GB,
					Version:         v1.ValkeyVersionV9_1,
					MaxMemoryPolicy: policy,
				},
			}

			result, err := rcvalkey.CreateSpec(scheme.Scheme, valkey, aiven, tenant)
			requireNoError(t, err)
			requireNotNil(t, result.Spec.UserConfig, "user config")
			requireNotNil(t, result.Spec.UserConfig.ValkeyMaxmemoryPolicy, "maxmemory policy")
			requireEqual(t, *result.Spec.UserConfig.ValkeyMaxmemoryPolicy, string(policy), "maxmemory policy")
		})
	}
}

func TestValkeyReconcileLifecycle(t *testing.T) {
	const testNamespace = "sync-integration-ns"
	ensureNamespace(t, testNamespace)
	reconciler := newValkeySynchronizer(scheme.Scheme)

	t.Run("sets reconcile status completed after first reconcile", func(t *testing.T) {
		testValkeyName := "sync-first-reconcile"
		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: testValkeyName, Namespace: testNamespace},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1},
		}
		requireNoError(t, k8sClient.Create(ctx, valkey))
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, valkey) })

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: valkey.Name, Namespace: valkey.Namespace}}
		_, err := reconciler.Reconcile(ctx, req)
		requireNoError(t, err)

		updatedValkey := &v1.Valkey{}
		requireNoError(t, k8sClient.Get(ctx, req.NamespacedName, updatedValkey))
		requireNotNil(t, updatedValkey.Status, "status")
		requireEqual(t, updatedValkey.Status.ReconcilePhase, "Completed", "reconcile phase")
		requireSliceContains(t, updatedValkey.GetFinalizers(), "valkey.nais.io/finalizer")
		requireEqual(t, updatedValkey.Status.ObservedGeneration, updatedValkey.Generation, "observed generation")
	})

	t.Run("reaches completed after spec update reconciliation", func(t *testing.T) {
		testValkeyName := "sync-spec-update"
		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: testValkeyName, Namespace: testNamespace},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1},
		}
		requireNoError(t, k8sClient.Create(ctx, valkey))
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, valkey) })

		req := reconcile.Request{NamespacedName: types.NamespacedName{Name: valkey.Name, Namespace: valkey.Namespace}}
		_, err := reconciler.Reconcile(ctx, req)
		requireNoError(t, err)

		firstValkey := &v1.Valkey{}
		requireNoError(t, k8sClient.Get(ctx, req.NamespacedName, firstValkey))
		requireEqual(t, firstValkey.Status.ReconcilePhase, "Completed", "reconcile phase after first reconcile")
		initialGeneration := firstValkey.Generation

		firstValkey.Spec.Memory = v1.ValkeyMemory8GB
		requireNoError(t, k8sClient.Update(ctx, firstValkey))

		requireNoError(t, k8sClient.Get(ctx, req.NamespacedName, firstValkey))
		requireTrue(t, firstValkey.Generation > initialGeneration, "generation should increase after spec update")

		_, err = reconciler.Reconcile(ctx, req)
		requireNoError(t, err)

		updatedValkey := &v1.Valkey{}
		requireNoError(t, k8sClient.Get(ctx, req.NamespacedName, updatedValkey))
		requireNotNil(t, updatedValkey.Status, "status")
		requireEqual(t, updatedValkey.Status.ReconcilePhase, "Completed", "reconcile phase")
		requireEqual(t, updatedValkey.Status.ObservedGeneration, updatedValkey.Generation, "observed generation")
	})
}

func TestValkeyDeletion(t *testing.T) {
	const deleteTestNamespace = "valkey-delete-test"
	ensureNamespace(t, deleteTestNamespace)
	syncReconciler := newValkeySynchronizer(scheme.Scheme)

	t.Run("refuses deletion without allowDeletion annotation", func(t *testing.T) {
		valkeyName := "delete-no-annotation"
		valkeyKey := types.NamespacedName{Name: valkeyName, Namespace: deleteTestNamespace}

		valkey := &v1.Valkey{ObjectMeta: metav1.ObjectMeta{Name: valkeyName, Namespace: deleteTestNamespace}, Spec: v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1}}
		requireNoError(t, k8sClient.Create(ctx, valkey))
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, valkey) })

		_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)

		requireNoError(t, k8sClient.Get(ctx, valkeyKey, valkey))
		requireNoError(t, k8sClient.Delete(ctx, valkey))

		_, _, err = (&ValkeyReconciler{Recorder: recorder}).Delete(valkey, ValkeyPreparedData{}, nil)
		requireErrorContains(t, err, "refusing to delete")

		requireNoError(t, k8sClient.Get(ctx, valkeyKey, valkey))
		requireSliceContains(t, valkey.GetFinalizers(), "valkey.nais.io/finalizer")
	})

	t.Run("disables terminationProtection before deleting child resources", func(t *testing.T) {
		valkeyName := "delete-with-protection"
		valkeyKey := types.NamespacedName{Name: valkeyName, Namespace: deleteTestNamespace}
		aivenValkeyName := "valkey-" + deleteTestNamespace + "-" + valkeyName

		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: valkeyName, Namespace: deleteTestNamespace, Annotations: map[string]string{api.AllowDeletionAnnotation: "true"}},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1},
		}
		requireNoError(t, k8sClient.Create(ctx, valkey))

		_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)

		aivenValkey := &aiven_v1alpha1.Valkey{}
		requireNoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: aivenValkeyName, Namespace: deleteTestNamespace}, aivenValkey))
		requireNotNil(t, aivenValkey.Spec.TerminationProtection, "terminationProtection")
		requireTrue(t, *aivenValkey.Spec.TerminationProtection, "terminationProtection should be true")

		requireNoError(t, k8sClient.Get(ctx, valkeyKey, valkey))
		requireNoError(t, k8sClient.Delete(ctx, valkey))

		result, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)
		requireTrue(t, result.RequeueAfter > 0, "expected requeue while disabling termination protection")

		requireNoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: aivenValkeyName, Namespace: deleteTestNamespace}, aivenValkey))
		requireFalse(t, *aivenValkey.Spec.TerminationProtection, "terminationProtection should be disabled")

		requireNoError(t, k8sClient.Get(ctx, valkeyKey, valkey))
		requireSliceContains(t, valkey.GetFinalizers(), "valkey.nais.io/finalizer")

		result, err = syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)
		requireEqual(t, result.RequeueAfter, 0, "requeue after should be zero")

		err = k8sClient.Get(ctx, types.NamespacedName{Name: aivenValkeyName, Namespace: deleteTestNamespace}, aivenValkey)
		requireTrue(t, apierrors.IsNotFound(err), "aiven valkey should be deleted")

		err = k8sClient.Get(ctx, valkeyKey, valkey)
		requireTrue(t, apierrors.IsNotFound(err), "valkey should be garbage collected")
	})

	t.Run("skips terminationProtection dance when Aiven resource does not exist", func(t *testing.T) {
		valkeyName := "delete-no-aiven"
		valkeyKey := types.NamespacedName{Name: valkeyName, Namespace: deleteTestNamespace}

		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: valkeyName, Namespace: deleteTestNamespace, Annotations: map[string]string{api.AllowDeletionAnnotation: "true"}},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1},
		}
		requireNoError(t, k8sClient.Create(ctx, valkey))

		_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)

		aivenValkeyName := "valkey-" + deleteTestNamespace + "-" + valkeyName
		aivenValkey := &aiven_v1alpha1.Valkey{}
		requireNoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: aivenValkeyName, Namespace: deleteTestNamespace}, aivenValkey))
		requireNoError(t, k8sClient.Delete(ctx, aivenValkey))

		requireNoError(t, k8sClient.Get(ctx, valkeyKey, valkey))
		requireNoError(t, k8sClient.Delete(ctx, valkey))

		result, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)
		requireEqual(t, result.RequeueAfter, 0, "requeue after should be zero")

		err = k8sClient.Get(ctx, valkeyKey, valkey)
		requireTrue(t, apierrors.IsNotFound(err), "valkey should be garbage collected")
	})

	t.Run("deletes when terminationProtection is already disabled", func(t *testing.T) {
		valkeyName := "delete-no-protection"
		valkeyKey := types.NamespacedName{Name: valkeyName, Namespace: deleteTestNamespace}
		aivenValkeyName := "valkey-" + deleteTestNamespace + "-" + valkeyName

		valkey := &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: valkeyName, Namespace: deleteTestNamespace, Annotations: map[string]string{api.AllowDeletionAnnotation: "true"}},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1},
		}
		requireNoError(t, k8sClient.Create(ctx, valkey))

		_, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)

		aivenValkey := &aiven_v1alpha1.Valkey{}
		requireNoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: aivenValkeyName, Namespace: deleteTestNamespace}, aivenValkey))
		aivenValkey.Spec.TerminationProtection = new(false)
		requireNoError(t, k8sClient.Update(ctx, aivenValkey))

		requireNoError(t, k8sClient.Get(ctx, valkeyKey, valkey))
		requireNoError(t, k8sClient.Delete(ctx, valkey))

		result, err := syncReconciler.Reconcile(ctx, reconcile.Request{NamespacedName: valkeyKey})
		requireNoError(t, err)
		requireEqual(t, result.RequeueAfter, 0, "requeue after should be zero")

		err = k8sClient.Get(ctx, types.NamespacedName{Name: aivenValkeyName, Namespace: deleteTestNamespace}, aivenValkey)
		requireTrue(t, apierrors.IsNotFound(err), "aiven valkey should be deleted")

		err = k8sClient.Get(ctx, valkeyKey, valkey)
		requireTrue(t, apierrors.IsNotFound(err), "valkey should be garbage collected")
	})
}

func ensureNamespace(t *testing.T, namespace string) {
	t.Helper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	err := k8sClient.Create(context.Background(), ns)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create namespace %q: %v", namespace, err)
	}
}

func newValkeySynchronizer(sch *runtime.Scheme) *synchronizer.Synchronizer[*v1.Valkey, ValkeyPreparedData] {
	testRecorder := events.NewRecorder(kevents.NewFakeRecorder(100))
	valkeyReconciler := &ValkeyReconciler{
		Aiven: config.Aiven{
			Project:                      "test-project",
			ProjectVPCID:                 "test-vpc-id",
			MetricsDestinationEndpointID: "test-metrics-service",
		},
		Tenant:   config.Tenant{Name: "test-tenant"},
		Recorder: testRecorder,
		Scheme:   sch,
	}
	return synchronizer.NewSynchronizer(k8sClient, sch, valkeyReconciler, testRecorder)
}
