package synchronizer

import (
	"context"
	"errors"
	"testing"

	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	kevents "k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// update, when set, stands in for a reconciler that discovers something during reconciliation and
// records it on the object it was handed.
type stubReconciler struct {
	prepare func(*v1.Valkey) error
	update  func(*v1.Valkey) []action.Action
}

func (stubReconciler) Name() string                       { return "valkey" }
func (stubReconciler) New() *v1.Valkey                    { return &v1.Valkey{} }
func (stubReconciler) OwnedTypes() []reconciler.OwnedType { return nil }
func (stubReconciler) AdditionalTypes() []client.Object   { return nil }

func (s stubReconciler) Prepare(_ context.Context, _ client.Reader, obj *v1.Valkey) (struct{}, ctrl.Result, error) {
	if s.prepare == nil {
		return struct{}{}, ctrl.Result{}, nil
	}
	return struct{}{}, ctrl.Result{}, s.prepare(obj)
}

func (s stubReconciler) Update(obj *v1.Valkey, _ struct{}, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	if s.update == nil {
		return nil, ctrl.Result{}, nil
	}
	return s.update(obj), ctrl.Result{}, nil
}

func (stubReconciler) Delete(*v1.Valkey, struct{}, reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	return nil, ctrl.Result{}, nil
}

// setCondition stands in for the condition an ordinary action records on its owner while running.
type setCondition struct {
	obj       client.Object
	owner     api.NaisObject
	condition metav1.Condition
}

func (a *setCondition) Do(context.Context, client.Client, *runtime.Scheme, ownership.OwnerManager) error {
	a.owner.GetStatus().SetCondition(a.condition)
	return nil
}

func (a *setCondition) GetObject() client.Object { return a.obj }

func (a *setCondition) GetOwner() api.NaisObject { return a.owner }

func TestRetryStatusUpdate(t *testing.T) {
	const (
		namespace   = "retry-status"
		donePhase   = "Completed"
		storedUID   = "stored-uid"
		replacedUID = "replaced-uid"
	)

	ctx := context.Background()

	newValkey := func(name string, uid types.UID) *v1.Valkey {
		return &v1.Valkey{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, UID: uid},
			Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_0},
		}
	}

	setup := func(t *testing.T, stored *v1.Valkey) (*Synchronizer[*v1.Valkey, struct{}], client.Client) {
		t.Helper()

		sch := runtime.NewScheme()
		if err := v1.AddToScheme(sch); err != nil {
			t.Fatalf("adding types to scheme: %v", err)
		}

		k8sClient := fake.NewClientBuilder().
			WithScheme(sch).
			WithObjects(stored).
			WithStatusSubresource(&v1.Valkey{}).
			Build()

		return &Synchronizer[*v1.Valkey, struct{}]{client: k8sClient, scheme: sch, reconciler: stubReconciler{}}, k8sClient
	}

	get := func(t *testing.T, k8sClient client.Client, name string) *v1.Valkey {
		t.Helper()

		fetched := &v1.Valkey{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, fetched); err != nil {
			t.Fatalf("reading %q: %v", name, err)
		}
		return fetched
	}

	t.Run("re-applies the status after the object was written mid-reconcile", func(t *testing.T) {
		s, k8sClient := setup(t, newValkey("adopting", storedUID))

		inFlight := get(t, k8sClient, "adopting")

		concurrent := get(t, k8sClient, "adopting")
		concurrent.Spec.Version = v1.ValkeyVersionV9_1
		if err := k8sClient.Update(ctx, concurrent); err != nil {
			t.Fatalf("writing the object out of band: %v", err)
		}

		inFlight.GetStatus().SetReconcilePhase(donePhase)
		if err := k8sClient.Status().Update(ctx, inFlight); !apierrors.IsConflict(err) {
			t.Fatalf("expected the closing write to conflict, got %v", err)
		}

		if err := s.retryStatusUpdate(ctx, inFlight); err != nil {
			t.Fatalf("retrying the status update: %v", err)
		}

		if got := get(t, k8sClient, "adopting").GetStatus().GetReconcilePhase(); got != donePhase {
			t.Errorf("reconcile phase = %q, want %q", got, donePhase)
		}
	})

	t.Run("leaves the status alone when the object has been replaced", func(t *testing.T) {
		s, k8sClient := setup(t, newValkey("replaced", replacedUID))

		inFlight := get(t, k8sClient, "replaced")
		inFlight.UID = storedUID
		inFlight.GetStatus().SetReconcilePhase(donePhase)

		if err := s.retryStatusUpdate(ctx, inFlight); err != nil {
			t.Fatalf("retrying the status update: %v", err)
		}

		if got := get(t, k8sClient, "replaced").GetStatus().GetReconcilePhase(); got != "" {
			t.Errorf("reconcile phase = %q, want it untouched", got)
		}
	})
}

func reconcileOnce(t *testing.T, stored *v1.Valkey, r stubReconciler) client.Client {
	t.Helper()

	sch := runtime.NewScheme()
	if err := v1.AddToScheme(sch); err != nil {
		t.Fatalf("adding types to scheme: %v", err)
	}

	k8sClient := fake.NewClientBuilder().
		WithScheme(sch).
		WithObjects(stored).
		WithStatusSubresource(&v1.Valkey{}).
		Build()

	s := NewSynchronizer(k8sClient, sch, r, events.NewRecorder(kevents.NewFakeRecorder(100)))
	if _, err := s.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(stored)}); err != nil {
		t.Fatalf("reconciling: %v", err)
	}
	return k8sClient
}

// The write on the Prepare error path exists to save what Prepare stamped, so a Prepare that fails
// without touching the object has nothing for it to save.
func TestReconcileDoesNotPatchWhenPrepareChangedNothing(t *testing.T) {
	stored := &v1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "untouched",
			Namespace:  "prepare-failure",
			UID:        "prepare-failure-uid",
			Finalizers: []string{"valkey"},
		},
		Spec: v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1},
	}

	sch := runtime.NewScheme()
	if err := v1.AddToScheme(sch); err != nil {
		t.Fatalf("adding types to scheme: %v", err)
	}

	patches := 0
	k8sClient := fake.NewClientBuilder().
		WithScheme(sch).
		WithObjects(stored).
		WithStatusSubresource(&v1.Valkey{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patches++
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()

	r := stubReconciler{prepare: func(*v1.Valkey) error { return errors.New("no Postgres named \"missing\"") }}
	s := NewSynchronizer(k8sClient, sch, r, events.NewRecorder(kevents.NewFakeRecorder(100)))

	if _, err := s.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(stored)}); err == nil {
		t.Fatal("expected the Prepare error to propagate")
	}

	if patches != 0 {
		t.Errorf("patch calls = %d, want 0", patches)
	}
}

func storedValkey(t *testing.T, k8sClient client.Client, obj *v1.Valkey) *v1.Valkey {
	t.Helper()

	fetched := &v1.Valkey{}
	if err := k8sClient.Get(context.Background(), client.ObjectKeyFromObject(obj), fetched); err != nil {
		t.Fatalf("reading %q: %v", obj.GetName(), err)
	}
	return fetched
}

// A reconciler that resolves a value during reconciliation has nowhere to put it but the object it
// was handed, so whatever it leaves there has to reach the API server.
func TestReconcilePersistsSpecChangedByReconciler(t *testing.T) {
	stored := &v1.Valkey{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "adopting",
			Namespace:  "spec-persist",
			UID:        "spec-persist-uid",
			Finalizers: []string{"valkey"},
		},
		Spec: v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_0},
	}

	k8sClient := reconcileOnce(t, stored, stubReconciler{update: func(obj *v1.Valkey) []action.Action {
		obj.Spec.Version = v1.ValkeyVersionV9_1
		return nil
	}})

	if got := storedValkey(t, k8sClient, stored).Spec.Version; got != v1.ValkeyVersionV9_1 {
		t.Errorf("stored version = %q, want %q", got, v1.ValkeyVersionV9_1)
	}
}

// The object write that persists a finalizer must not carry the spec back to what was read, undoing
// what the reconciler resolved in the same pass.
func TestReconcilePersistsSpecAndFinalizerTogether(t *testing.T) {
	stored := &v1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: "adopting", Namespace: "spec-and-finalizer", UID: "spec-and-finalizer-uid"},
		Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_0},
	}

	k8sClient := reconcileOnce(t, stored, stubReconciler{update: func(obj *v1.Valkey) []action.Action {
		obj.Spec.Version = v1.ValkeyVersionV9_1
		return nil
	}})

	got := storedValkey(t, k8sClient, stored)
	if got.Spec.Version != v1.ValkeyVersionV9_1 {
		t.Errorf("stored version = %q, want %q", got.Spec.Version, v1.ValkeyVersionV9_1)
	}
	if len(got.GetFinalizers()) != 1 || got.GetFinalizers()[0] != "valkey" {
		t.Errorf("finalizers = %v, want [valkey]", got.GetFinalizers())
	}
}

// Conditions are recorded on the owner while actions run, after the last phase write. Nothing may
// overwrite the object between there and the closing status update.
func TestReconcilePersistsConditionsSetByActions(t *testing.T) {
	const conditionType = "test/Observed"

	stored := &v1.Valkey{
		ObjectMeta: metav1.ObjectMeta{Name: "conditioned", Namespace: "conditions", UID: "conditions-uid"},
		Spec:       v1.ValkeySpec{Tier: v1.ValkeyTierSingleNode, Memory: v1.ValkeyMemory4GB, Version: v1.ValkeyVersionV9_1},
	}

	k8sClient := reconcileOnce(t, stored, stubReconciler{update: func(obj *v1.Valkey) []action.Action {
		return []action.Action{&setCondition{
			obj:   &v1.Valkey{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: obj.GetNamespace()}},
			owner: obj,
			condition: metav1.Condition{
				Type:   conditionType,
				Status: metav1.ConditionTrue,
				Reason: "Reconciled",
			},
		}}
	}})

	conditions := storedValkey(t, k8sClient, stored).GetStatus().GetConditions()
	if len(conditions) != 1 || conditions[0].Type != conditionType {
		t.Errorf("stored conditions = %v, want one %q", conditions, conditionType)
	}
}
