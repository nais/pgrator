package action

import (
	"context"
	"testing"

	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	core_v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type mockRecorder struct{}

func (m *mockRecorder) RecordEvent(obj api.NaisObject, eventType, reason, messageFmt string, args ...any) {
	// Mock implementation - does nothing
}

func (m *mockRecorder) RecordErrorEvent(obj api.NaisObject, phase string, err error) {
	// Mock implementation - does nothing
}

type mockOwnerManager struct{}

func (m mockOwnerManager) HasOwnerAnnotation(obj, owner client.Object) bool {
	return false
}

func (m mockOwnerManager) AddOwnerAnnotation(obj client.Object, owner client.Object) {
}

func (m mockOwnerManager) RemoveOwnerAnnotation(obj client.Object, owner client.Object) {
}

func (m mockOwnerManager) GetOwnerAnnotations(obj client.Object) []string {
	return []string{}
}

func (m mockOwnerManager) SetOwnerAnnotations(obj client.Object, ownerReferences []string) {
}

func newRecreateTestFixtures() (*runtime.Scheme, *v1.Postgres, *mockRecorder, *mockOwnerManager, ConditionGetter) {
	scheme := runtime.NewScheme()
	if err := core_v1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	if err := v1.AddToScheme(scheme); err != nil {
		panic(err)
	}

	postgres := &v1.Postgres{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      "test-postgres",
			Namespace: "test-namespace",
		},
		Spec: v1.PostgresSpec{},
	}
	postgres.Status = &v1.PostgresStatus{}

	recorder := &mockRecorder{}
	ownerManager := &mockOwnerManager{}
	conditionGetter := func(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition {
		return []meta_v1.Condition{
			{
				Type:   "serviceaccount/Available",
				Status: meta_v1.ConditionTrue,
				Reason: "Exists",
			},
		}
	}

	return scheme, postgres, recorder, ownerManager, conditionGetter
}

func TestRecreateCreatesResourceWhenItDoesNotExist(t *testing.T) {
	ctx := context.Background()
	scheme, postgres, recorder, ownerManager, conditionGetter := newRecreateTestFixtures()
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	serviceAccount := &core_v1.ServiceAccount{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      "test-sa",
			Namespace: "test-namespace",
		},
	}

	action := Recreate(serviceAccount, postgres, conditionGetter, recorder)
	if err := action.Do(ctx, fakeClient, scheme, ownerManager); err != nil {
		t.Fatalf("Do: %v", err)
	}

	var created core_v1.ServiceAccount
	if err := fakeClient.Get(ctx, client.ObjectKey{Name: "test-sa", Namespace: "test-namespace"}, &created); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

func TestRecreateDeletesAndRecreatesExistingResourceClearingOldMetadata(t *testing.T) {
	ctx := context.Background()
	scheme, postgres, recorder, ownerManager, conditionGetter := newRecreateTestFixtures()

	existingServiceAccount := &core_v1.ServiceAccount{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      "test-sa",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"old-label": "old-value",
			},
			Annotations: map[string]string{
				"creation-timestamp": "2024-01-01",
				"old-annotation":     "should-be-removed",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existingServiceAccount).
		Build()

	newServiceAccount := &core_v1.ServiceAccount{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      "test-sa",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"new-label": "new-value",
			},
		},
	}

	action := Recreate(newServiceAccount, postgres, conditionGetter, recorder)
	if err := action.Do(ctx, fakeClient, scheme, ownerManager); err != nil {
		t.Fatalf("Do: %v", err)
	}

	var recreated core_v1.ServiceAccount
	if err := fakeClient.Get(ctx, client.ObjectKey{Name: "test-sa", Namespace: "test-namespace"}, &recreated); err != nil {
		t.Fatalf("Get: %v", err)
	}

	// CRITICAL: Old annotations/labels should be COMPLETELY gone (not merged).
	// This proves Delete+Create happened, not Update.
	if _, ok := recreated.Annotations["creation-timestamp"]; ok {
		t.Error("old annotation should be cleared by recreation (Delete+Create), not merged")
	}
	if _, ok := recreated.Labels["old-label"]; ok {
		t.Error("old label should be cleared by recreation (Delete+Create), not merged")
	}
	if got := recreated.Labels["new-label"]; got != "new-value" {
		t.Errorf("new-label = %q, want %q", got, "new-value")
	}
	if len(recreated.Labels) != 1 {
		t.Errorf("expected exactly 1 label after recreation, got %d: %v", len(recreated.Labels), recreated.Labels)
	}
}

func TestRecreateClearsOldMetadataUnlikeCreateOrUpdate(t *testing.T) {
	ctx := context.Background()
	scheme, postgres, recorder, ownerManager, conditionGetter := newRecreateTestFixtures()

	// First, exercise CreateOrUpdate behavior for comparison.
	existingForUpdate := &core_v1.ServiceAccount{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      "test-sa-update",
			Namespace: "test-namespace",
			Annotations: map[string]string{
				"external-state": "should-remain-with-update",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(existingForUpdate).
		Build()

	updateServiceAccount := &core_v1.ServiceAccount{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      "test-sa-update",
			Namespace: "test-namespace",
			Annotations: map[string]string{
				"new-annotation": "added-by-update",
			},
		},
	}

	updateAction := CreateOrUpdate(updateServiceAccount, postgres, conditionGetter, recorder)
	if err := updateAction.Do(ctx, fakeClient, scheme, ownerManager); err != nil {
		t.Fatalf("CreateOrUpdate.Do: %v", err)
	}

	// Now test Recreate with a separate resource.
	existingForRecreate := &core_v1.ServiceAccount{
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      "test-sa-recreate",
			Namespace: "test-namespace",
			Annotations: map[string]string{
				"external-state":     "should-be-cleared",
				"another-annotation": "also-should-be-cleared",
			},
			Labels: map[string]string{
				"old-label": "old-value",
			},
		},
	}

	if err := fakeClient.Create(ctx, existingForRecreate); err != nil {
		t.Fatalf("Create: %v", err)
	}

	newRecreateServiceAccount := &core_v1.ServiceAccount{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: meta_v1.ObjectMeta{
			Name:      "test-sa-recreate",
			Namespace: "test-namespace",
			Annotations: map[string]string{
				"recreate-annotation": "added-by-recreate",
			},
			Labels: map[string]string{
				"new-label": "new-value",
			},
		},
	}

	recreateAction := Recreate(newRecreateServiceAccount, postgres, conditionGetter, recorder)
	if err := recreateAction.Do(ctx, fakeClient, scheme, ownerManager); err != nil {
		t.Fatalf("Recreate.Do: %v", err)
	}

	var afterRecreate core_v1.ServiceAccount
	if err := fakeClient.Get(ctx, client.ObjectKey{Name: "test-sa-recreate", Namespace: "test-namespace"}, &afterRecreate); err != nil {
		t.Fatalf("Get: %v", err)
	}

	// CRITICAL: old annotations should be COMPLETELY gone (not merged): Recreate deletes, doesn't update.
	if _, ok := afterRecreate.Annotations["external-state"]; ok {
		t.Error("Recreate should DELETE old resource, not UPDATE it")
	}
	if _, ok := afterRecreate.Annotations["another-annotation"]; ok {
		t.Error("Recreate should DELETE old resource, not UPDATE it")
	}
	if _, ok := afterRecreate.Labels["old-label"]; ok {
		t.Error("Recreate should DELETE old resource, not UPDATE it")
	}
	if got := afterRecreate.Annotations["recreate-annotation"]; got != "added-by-recreate" {
		t.Errorf("recreate-annotation = %q, want %q", got, "added-by-recreate")
	}
	if got := afterRecreate.Labels["new-label"]; got != "new-value" {
		t.Errorf("new-label = %q, want %q", got, "new-value")
	}
	if len(afterRecreate.Annotations) != 1 {
		t.Errorf("expected exactly 1 annotation, got %d: %v", len(afterRecreate.Annotations), afterRecreate.Annotations)
	}
	if len(afterRecreate.Labels) != 1 {
		t.Errorf("expected exactly 1 label, got %d: %v", len(afterRecreate.Labels), afterRecreate.Labels)
	}
}

func TestRecreateMetadataClearingScenarios(t *testing.T) {
	tests := []struct {
		name             string
		oldAnnotations   map[string]string
		oldLabels        map[string]string
		newAnnotations   map[string]string
		newLabels        map[string]string
		expectOldCleared bool
	}{
		{
			name:             "single old annotation should be cleared",
			oldAnnotations:   map[string]string{"old": "value"},
			oldLabels:        map[string]string{},
			newAnnotations:   map[string]string{"new": "value"},
			newLabels:        map[string]string{},
			expectOldCleared: true,
		},
		{
			name:             "multiple old annotations should be cleared",
			oldAnnotations:   map[string]string{"old1": "value1", "old2": "value2"},
			oldLabels:        map[string]string{},
			newAnnotations:   map[string]string{"new": "value"},
			newLabels:        map[string]string{},
			expectOldCleared: true,
		},
		{
			name:             "old labels and annotations should both be cleared",
			oldAnnotations:   map[string]string{"old-annotation": "value"},
			oldLabels:        map[string]string{"old-label": "value"},
			newAnnotations:   map[string]string{"new-annotation": "value"},
			newLabels:        map[string]string{"new-label": "value"},
			expectOldCleared: true,
		},
		{
			name:             "empty old metadata should work",
			oldAnnotations:   map[string]string{},
			oldLabels:        map[string]string{},
			newAnnotations:   map[string]string{"new": "value"},
			newLabels:        map[string]string{"label": "value"},
			expectOldCleared: true,
		},
		{
			name: "complex scenario with many fields",
			oldAnnotations: map[string]string{
				"external-state": "should-clear",
				"timestamp":      "2024-01-01",
				"version":        "v1",
			},
			oldLabels: map[string]string{
				"env":  "prod",
				"team": "platform",
			},
			newAnnotations: map[string]string{
				"iam.gke.io/gcp-service-account": "sa@project.iam.gserviceaccount.com",
			},
			newLabels: map[string]string{
				"app": "postgres",
			},
			expectOldCleared: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			scheme, postgres, recorder, ownerManager, conditionGetter := newRecreateTestFixtures()

			existing := &core_v1.ServiceAccount{
				ObjectMeta: meta_v1.ObjectMeta{
					Name:        "test-sa-table",
					Namespace:   "test-namespace",
					Annotations: tt.oldAnnotations,
					Labels:      tt.oldLabels,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(existing).
				Build()

			newSA := &core_v1.ServiceAccount{
				TypeMeta: meta_v1.TypeMeta{
					Kind:       "ServiceAccount",
					APIVersion: "v1",
				},
				ObjectMeta: meta_v1.ObjectMeta{
					Name:        "test-sa-table",
					Namespace:   "test-namespace",
					Annotations: tt.newAnnotations,
					Labels:      tt.newLabels,
				},
			}

			action := Recreate(newSA, postgres, conditionGetter, recorder)
			if err := action.Do(ctx, fakeClient, scheme, ownerManager); err != nil {
				t.Fatalf("Do: %v", err)
			}

			var result core_v1.ServiceAccount
			if err := fakeClient.Get(ctx, client.ObjectKey{Name: "test-sa-table", Namespace: "test-namespace"}, &result); err != nil {
				t.Fatalf("Get: %v", err)
			}

			if tt.expectOldCleared {
				for key := range tt.oldAnnotations {
					if _, ok := result.Annotations[key]; ok {
						t.Errorf("old annotation %q should be cleared", key)
					}
				}
				for key := range tt.oldLabels {
					if _, ok := result.Labels[key]; ok {
						t.Errorf("old label %q should be cleared", key)
					}
				}
			}

			for key, value := range tt.newAnnotations {
				if got := result.Annotations[key]; got != value {
					t.Errorf("annotation %q = %q, want %q", key, got, value)
				}
			}
			for key, value := range tt.newLabels {
				if got := result.Labels[key]; got != value {
					t.Errorf("label %q = %q, want %q", key, got, value)
				}
			}
		})
	}
}
