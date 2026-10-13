package action

import (
	"context"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/pkg/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

type countingRecorder struct{ changes int }

func TestManagedUpdatePreservesExternalFieldsDuringAdoptionAndRemoval(t *testing.T) {
	const externalValue = "preserved-external-value"
	const previousValue = "previous-managed-value"
	for _, baseline := range []bool{false, true} {
		name := "adoption"
		if baseline {
			name = "removal"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			scheme := newExclusiveCreateOrUpdateScheme(t)
			owner := newExclusiveOwner("owner")
			stored := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name: "config", Namespace: owner.Namespace,
				Labels:      map[string]string{"managed": previousValue},
				Annotations: map[string]string{"managed": previousValue},
			}, Data: map[string]string{"managed": previousValue}}
			if baseline {
				if err := prepareCreate(stored); err != nil {
					t.Fatal(err)
				}
			}
			stored.Labels["external"] = externalValue
			stored.Annotations["external"] = externalValue
			stored.Data["external"] = externalValue
			stored.BinaryData = map[string][]byte{"external": []byte(externalValue)}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stored).Build()
			desired := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: stored.Name, Namespace: stored.Namespace}}
			if !baseline {
				desired.Data = map[string]string{"managed": "new"}
			}
			perform := func() {
				t.Helper()
				if err := Update(desired.DeepCopy(), owner, noConditions, &mockRecorder{}).Do(ctx, c, scheme, &mockOwnerManager{}); err != nil {
					t.Fatal(err)
				}
			}
			perform()
			got := &corev1.ConfigMap{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(stored), got); err != nil {
				t.Fatal(err)
			}
			if got.Labels["external"] != externalValue || got.Annotations["external"] != externalValue || got.Data["external"] != externalValue || string(got.BinaryData["external"]) != externalValue {
				t.Fatalf("external fields lost: %#v", got)
			}
			if baseline {
				if _, ok := got.Data["managed"]; ok {
					t.Fatal("managed data was not removed")
				}
				if _, ok := got.Labels["managed"]; ok {
					t.Fatal("managed label was not removed")
				}
				if _, ok := got.Annotations["managed"]; ok {
					t.Fatal("managed annotation was not removed")
				}
			} else if got.Data["managed"] != "new" || got.Labels["managed"] != previousValue || got.Annotations["managed"] != previousValue {
				t.Fatal("adoption did not limit ownership to desired fields")
			}
			rv := got.ResourceVersion
			perform()
			if err := c.Get(ctx, client.ObjectKeyFromObject(stored), got); err != nil {
				t.Fatal(err)
			}
			if got.ResourceVersion != rv {
				t.Fatal("reconcile did not converge")
			}
		})
	}
}

func TestManagedUpdateRemovesEmptyManagedMap(t *testing.T) {
	stored := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config"}, Data: map[string]string{"managed": "removed-value"}}
	if err := prepareCreate(stored); err != nil {
		t.Fatal(err)
	}
	desired := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: stored.Name}}
	if _, err := prepareUpdate(desired, stored); err != nil {
		t.Fatal(err)
	}
	if desired.Data != nil {
		t.Fatalf("empty map left behind: %v", desired.Data)
	}
}

func TestRemovingRecoveryBootstrapAlsoRemovesDefaultedRecoveryFields(t *testing.T) {
	for _, baseline := range []bool{false, true} {
		name := "adoption"
		if baseline {
			name = "recorded baseline"
		}
		t.Run(name, func(t *testing.T) {
			cluster := &cnpgv1.Cluster{Spec: cnpgv1.ClusterSpec{Bootstrap: &cnpgv1.BootstrapConfiguration{
				Recovery: &cnpgv1.BootstrapRecovery{Source: "source"},
			}, ExternalClusters: []cnpgv1.ExternalCluster{{Name: "source"}}}}
			if baseline {
				if err := prepareCreate(cluster); err != nil {
					t.Fatal(err)
				}
			}
			// Admission adds a nested field not present in the original desired spec.
			cluster.Spec.Bootstrap.Recovery.Database = "app"
			desired := &cnpgv1.Cluster{Spec: cnpgv1.ClusterSpec{Bootstrap: &cnpgv1.BootstrapConfiguration{
				InitDB: &cnpgv1.BootstrapInitDB{Database: "app"},
			}}}
			changed, err := prepareUpdate(desired, cluster)
			if err != nil || !changed {
				t.Fatalf("removing recovery: changed=%t, err=%v", changed, err)
			}
			if desired.Spec.Bootstrap.Recovery != nil {
				t.Fatal("defaulted fields kept the recovery subtree alive")
			}
			if len(desired.Spec.ExternalClusters) != 0 {
				t.Fatal("recovery source references were not removed")
			}
		})
	}
}

func TestSecretStringDataConvergesWithoutStoringValuesInMetadata(t *testing.T) {
	owner := newExclusiveOwner("owner")
	desired := newExclusiveSecret(owner, "sensitive-value")
	if err := prepareCreate(desired); err != nil {
		t.Fatal(err)
	}
	stored := desired.DeepCopy()
	stored.Data = map[string][]byte{"value": []byte("sensitive-value")}
	stored.StringData = nil
	if changed, err := prepareUpdate(newExclusiveSecret(owner, "sensitive-value"), stored); err != nil || changed {
		t.Fatalf("persisted Secret comparison: changed=%t, err=%v", changed, err)
	}
	if strings.Contains(stored.Annotations[managedFieldsAnnotation], "sensitive-value") {
		t.Fatal("managed field metadata contains credential values")
	}
}

func (r *countingRecorder) RecordEvent(_ api.NaisObject, _, reason, _ string, _ ...any) {
	if reason == "Updated" || reason == "Claimed" {
		r.changes++
	}
}

func (*countingRecorder) RecordErrorEvent(api.NaisObject, string, error) {}

func TestUpdatesConvergeAndStillObserveConditions(t *testing.T) {
	for _, constructor := range []struct {
		name string
		new  func(client.Object, api.NaisObject, ConditionGetter, *countingRecorder) Action
	}{
		{"update", func(o client.Object, owner api.NaisObject, get ConditionGetter, r *countingRecorder) Action {
			return Update(o, owner, get, r)
		}},
		{"create or update", func(o client.Object, owner api.NaisObject, get ConditionGetter, r *countingRecorder) Action {
			return CreateOrUpdate(o, owner, get, r)
		}},
		{"exclusive create or update", func(o client.Object, owner api.NaisObject, get ConditionGetter, r *countingRecorder) Action {
			return ExclusiveCreateOrUpdate(o, owner, get, r)
		}},
	} {
		t.Run(constructor.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := newExclusiveCreateOrUpdateScheme(t)
			owner := newExclusiveOwner("owner")
			desired := newExclusiveRole(owner, "managed")
			writes := 0
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(desired.DeepCopy()).WithStatusSubresource(&cnpgv1.DatabaseRole{}).
				WithInterceptorFuncs(interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					writes++
					return c.Update(ctx, obj, opts...)
				}}).Build()
			recorder := &countingRecorder{}
			observations := 0
			get := func(obj client.Object, _ *runtime.Scheme) []metav1.Condition {
				observations++
				return []metav1.Condition{{Type: "Available", Status: metav1.ConditionTrue, Reason: "Observed", Message: obj.(*cnpgv1.DatabaseRole).Status.Message}}
			}
			perform := func() {
				t.Helper()
				if err := constructor.new(desired.DeepCopy(), owner, get, recorder).Do(ctx, c, scheme, &mockOwnerManager{}); err != nil {
					t.Fatal(err)
				}
			}
			perform() // Establish the managed field baseline.
			stored := &cnpgv1.DatabaseRole{}
			key := client.ObjectKeyFromObject(desired)
			if err := c.Get(ctx, key, stored); err != nil {
				t.Fatal(err)
			}
			// Simulate fields added by admission/another controller and a live status.
			stored.Spec.ConnectionLimit = 100
			metav1.SetMetaDataAnnotation(&stored.ObjectMeta, "other-controller/annotation", "keep")
			if err := c.Update(ctx, stored); err != nil {
				t.Fatal(err)
			}
			stored.Status.Message = "ready"
			if err := c.Status().Update(ctx, stored); err != nil {
				t.Fatal(err)
			}
			writes, recorder.changes = 0, 0
			perform()
			perform()
			if writes != 0 || recorder.changes != 0 {
				t.Fatalf("stable reconcile: writes=%d, change events=%d, want 0", writes, recorder.changes)
			}
			if observations != 3 || owner.GetStatus().GetConditions()[0].Message != "ready" {
				t.Fatal("no-op did not observe live conditions")
			}
			// Drift in a managed field must be corrected, without removing external fields.
			if err := c.Get(ctx, key, stored); err != nil {
				t.Fatal(err)
			}
			stored.Spec.Comment = "drift"
			stored.Spec.Superuser = true
			if err := c.Update(ctx, stored); err != nil {
				t.Fatal(err)
			}
			writes = 0
			perform()
			if writes != 1 || recorder.changes != 1 {
				t.Fatalf("drift: writes=%d, events=%d, want 1", writes, recorder.changes)
			}
			if err := c.Get(ctx, key, stored); err != nil {
				t.Fatal(err)
			}
			if stored.Spec.Comment != "managed" || stored.Spec.Superuser || stored.Spec.ConnectionLimit != 100 || stored.Annotations["other-controller/annotation"] != "keep" {
				t.Fatal("drift correction did not preserve external fields")
			}
			// Omitting a previously managed field is a deletion, not a wildcard.
			desired.Spec.Comment = ""
			perform()
			if err := c.Get(ctx, key, stored); err != nil {
				t.Fatal(err)
			}
			if stored.Spec.Comment != "" {
				t.Fatal("previously managed field was not removed")
			}
			writes, recorder.changes = 0, 0
			perform()
			if writes != 0 || recorder.changes != 0 {
				t.Fatal("removal did not converge")
			}
		})
	}
}

func TestClaimOnlyWritesWhenOwnershipChanges(t *testing.T) {
	ctx := context.Background()
	scheme := newExclusiveCreateOrUpdateScheme(t)
	owner := newExclusiveOwner("owner")
	obj := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: owner.Namespace}}
	patches := 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			patches++
			return c.Patch(ctx, obj, patch, opts...)
		},
	}).Build()
	manager := ownership.NewOwnerManager("test/owner")
	recorder := &countingRecorder{}
	observations := 0
	get := func(client.Object, *runtime.Scheme) []metav1.Condition { observations++; return nil }
	for range 3 {
		if err := Claim(obj, owner, get, recorder).Do(ctx, c, scheme, manager); err != nil {
			t.Fatal(err)
		}
	}
	if patches != 1 || recorder.changes != 1 || observations != 3 {
		t.Fatalf("patches=%d, changes=%d, observations=%d, want 1, 1, 3", patches, recorder.changes, observations)
	}
}
