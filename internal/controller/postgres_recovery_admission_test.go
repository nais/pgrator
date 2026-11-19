package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSameSecondUnacceptedRecoveryCannotBlockSourceDeletion(t *testing.T) {
	r, c, source, pg := unacceptedDirectRecoveryFixture(t)
	requireNoError(t, c.Delete(t.Context(), source))
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(source), source))
	pg.CreationTimestamp = *source.DeletionTimestamp
	requireNoError(t, c.Update(t.Context(), pg))
	if _, _, err := r.Prepare(t.Context(), c, pg); err == nil {
		t.Error("accepted a new recovery with the same-second deletion timestamp")
	}
	blocking, err := blockingPostgresRecoveries(t.Context(), c, source)
	requireNoError(t, err)
	if len(blocking) != 0 {
		t.Errorf("unaccepted same-second recovery blocks source deletion: %v", blocking)
	}
}

func TestSameSecondAcceptedRecoveryCanFinishAndBlocksSourceDeletion(t *testing.T) {
	r, c, source, pg := directRecoveryFixture(t)
	requireNoError(t, c.Delete(t.Context(), source))
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(source), source))
	pg.CreationTimestamp = *source.DeletionTimestamp
	requireNoError(t, c.Update(t.Context(), pg))
	_, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	blocking, err := blockingPostgresRecoveries(t.Context(), c, source)
	requireNoError(t, err)
	if fmt.Sprint(blocking) != "[restored]" {
		t.Fatalf("accepted recovery lost its archive protection: %v", blocking)
	}
}

func TestRecoveryReservesSourceBeforePlanningAnyChildren(t *testing.T) {
	r, c, source, pg := unacceptedDirectRecoveryFixture(t)
	p, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	actions, result, err := r.Update(pg, p, relatedobjectsmap.NewRelatedObjectsMap(r.Scheme))
	requireNoError(t, err)
	if len(actions) != 2 || result.RequeueAfter == 0 {
		t.Fatal("first recovery pass must only protect target and reserve source")
	}
	owners := ownership.NewOwnerManager(r.Name() + "/owner")
	for _, a := range actions {
		if _, ok := a.GetObject().(*v1.Postgres); !ok {
			t.Fatalf("planned a child before admission: %T", a.GetObject())
		}
		requireNoError(t, a.Do(t.Context(), c, r.Scheme, owners))
	}
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(source), source))
	accepted, err := recoveryWasAccepted(source, pg)
	requireNoError(t, err)
	if !accepted {
		t.Fatal("source acceptance checkpoint was not persisted")
	}
	p, _, err = r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	if p.RecoveryAdmissionRequired {
		t.Fatal("persisted admission was not observed on retry")
	}
}

func TestDeletedRecoveryAdmissionIsPrunedWithoutMatchingReusedName(t *testing.T) {
	_, c, source, pg := directRecoveryFixture(t)
	// Retain the name, but replace the target UID. A new incarnation must not
	// inherit either acceptance or the old incarnation's source deletion lock.
	pg.Finalizers = nil
	requireNoError(t, c.Update(t.Context(), pg))
	requireNoError(t, c.Delete(t.Context(), pg))
	pg.UID, pg.ResourceVersion, pg.Status = "replacement-target-uid", "", nil
	requireNoError(t, c.Create(t.Context(), pg))
	requireNoError(t, pruneRecoveryAdmissions(t.Context(), c, source))
	if source.Annotations[recoveryAdmissionsAnnotation] != "" {
		t.Fatal("completed/deleted reservation was not pruned")
	}
	accepted, err := recoveryWasAccepted(source, pg)
	requireNoError(t, err)
	if accepted {
		t.Fatal("reused target name inherited an admission")
	}
}

// This seam injects an actual API-server deletion immediately before the
// admission's resourceVersion-guarded metadata write.
type deleteBeforeRecoveryAdmission struct{ client.Client }

func (c deleteBeforeRecoveryAdmission) Update(ctx context.Context, object client.Object, options ...client.UpdateOption) error {
	if err := c.Delete(ctx, object); err != nil {
		return err
	}
	return c.Client.Update(ctx, object, options...)
}

func admissionServerFixture(t *testing.T, suffix string) (*v1.Postgres, *v1.Postgres) {
	t.Helper()
	source := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "admission-source-" + suffix, Namespace: "default", Finalizers: []string{"postgres.nais.io"}}, Spec: v1.PostgresSpec{MajorVersion: "18"}}
	requireNoError(t, k8sClient.Create(t.Context(), source))
	target := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "admission-target-" + suffix, Namespace: "default", Finalizers: []string{"postgres.nais.io"}}, Spec: v1.PostgresSpec{MajorVersion: "18", Recovery: &v1.PostgresRecovery{SourcePostgres: source.Name, SourceUID: string(source.UID), BucketName: "archive", ServerName: "source-cluster", TargetTime: metav1.NewTime(time.Now().Truncate(time.Second))}}}
	requireNoError(t, k8sClient.Create(t.Context(), target))
	return source, target
}

func TestRecoveryAdmissionConflictsWhenSourceDeletionWins(t *testing.T) {
	source, target := admissionServerFixture(t, "race")
	a := newAcceptPostgresRecovery(target)
	err := a.Do(t.Context(), deleteBeforeRecoveryAdmission{k8sClient}, nil, nil)
	if !apierrors.IsConflict(err) {
		t.Fatalf("admission racing deletion must conflict: %v", err)
	}
	if err := a.Do(t.Context(), k8sClient, nil, nil); err == nil {
		t.Fatal("retried admission was allowed after source deletion")
	}
	requireNoError(t, k8sClient.Get(t.Context(), client.ObjectKeyFromObject(source), source))
	accepted, err := recoveryWasAccepted(source, target)
	requireNoError(t, err)
	if accepted {
		t.Fatal("failed admission left a deletion blocker")
	}
}

func TestRecoveryAdmissionSurvivesConcurrentStatusRetry(t *testing.T) {
	source, target := admissionServerFixture(t, "status")
	stale := source.DeepCopy()
	requireNoError(t, newAcceptPostgresRecovery(target).Do(t.Context(), k8sClient, nil, nil))
	stale.GetStatus().SetReconcilePhase("Completed")
	if err := k8sClient.Status().Update(t.Context(), stale); !apierrors.IsConflict(err) {
		t.Fatalf("stale status write must conflict: %v", err)
	}
	requireNoError(t, k8sClient.Get(t.Context(), client.ObjectKeyFromObject(source), source))
	// Model the synchronizer's status retry, which refreshes only resourceVersion.
	stale.ResourceVersion = source.ResourceVersion
	requireNoError(t, k8sClient.Status().Update(t.Context(), stale))
	requireNoError(t, k8sClient.Get(t.Context(), client.ObjectKeyFromObject(source), source))
	accepted, err := recoveryWasAccepted(source, target)
	requireNoError(t, err)
	if !accepted {
		t.Fatal("status retry erased a concurrent source admission")
	}
}

func TestRecoveryCRDRejectsFractionalTargetBeforeTypedDecoding(t *testing.T) {
	_, target := admissionServerFixture(t, "precision")
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(target)
	requireNoError(t, err)
	u := &unstructured.Unstructured{Object: content}
	u.SetAPIVersion(v1.GroupVersion.String())
	u.SetKind("Postgres")
	u.SetName(target.Name + "-fractional")
	u.SetUID("")
	u.SetResourceVersion("")
	requireNoError(t, unstructured.SetNestedField(u.Object, "2026-11-16T13:10:00.750Z", "spec", "recovery", "targetTime"))
	if err := k8sClient.Create(t.Context(), u); !apierrors.IsInvalid(err) {
		t.Fatalf("CRD must reject unsupported precision, not admit an object typed clients cannot read: %v", err)
	}
}

type staleAdmissionSource struct {
	client.Client
	snapshot *v1.Postgres
}

func (c staleAdmissionSource) Get(ctx context.Context, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
	if pg, ok := object.(*v1.Postgres); ok && key == client.ObjectKeyFromObject(c.snapshot) {
		*pg = *c.snapshot.DeepCopy()
		return nil
	}
	return c.Client.Get(ctx, key, object, options...)
}

func TestConcurrentRecoveryAdmissionsCannotOverwriteEachOther(t *testing.T) {
	source, first := admissionServerFixture(t, "concurrent")
	second := first.DeepCopy()
	second.Name, second.UID, second.ResourceVersion = first.Name+"-second", "", ""
	requireNoError(t, k8sClient.Create(t.Context(), second))
	requireNoError(t, newAcceptPostgresRecovery(first).Do(t.Context(), k8sClient, nil, nil))
	err := newAcceptPostgresRecovery(second).Do(t.Context(), staleAdmissionSource{Client: k8sClient, snapshot: source.DeepCopy()}, nil, nil)
	if !apierrors.IsConflict(err) {
		t.Fatalf("concurrent admission must conflict rather than overwrite its peer: %v", err)
	}
	requireNoError(t, newAcceptPostgresRecovery(second).Do(t.Context(), k8sClient, nil, nil))
	requireNoError(t, k8sClient.Get(t.Context(), client.ObjectKeyFromObject(source), source))
	for _, target := range []*v1.Postgres{first, second} {
		accepted, err := recoveryWasAccepted(source, target)
		requireNoError(t, err)
		if !accepted {
			t.Errorf("lost concurrent admission for %s", target.Name)
		}
	}
}
