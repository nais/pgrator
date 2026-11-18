package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	rcmonitoring "github.com/nais/pgrator/internal/resourcecreator/postgresmonitoring"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Namespace alerts are shared, not physical children of one Postgres. They must
// stay outside the physical ownership journal and its exclusive deletion path.
type ensurePostgresAlerts struct {
	postgres *v1.Postgres
	rule     *monitoringv1.PrometheusRule
}

func newEnsurePostgresAlerts(pg *v1.Postgres) (action.Action, error) {
	rule, err := rcmonitoring.Rule(pg.Namespace)
	if err != nil {
		return nil, fmt.Errorf("rendering Postgres alerts: %w", err)
	}
	return &ensurePostgresAlerts{postgres: pg, rule: rule}, nil
}

func (a *ensurePostgresAlerts) GetObject() client.Object { return a.rule }
func (a *ensurePostgresAlerts) GetOwner() api.NaisObject { return a.postgres }
func (*ensurePostgresAlerts) SkipOwnership() bool        { return true }

func (a *ensurePostgresAlerts) Do(ctx context.Context, c client.Client, _ *runtime.Scheme, _ ownership.OwnerManager) error {
	// Read namespace membership at execution time, not from a single Postgres's
	// prepared child inventory. Concurrent writers use resourceVersion conflicts
	// to retry instead of overwriting one another's owner references.
	postgres := &v1.PostgresList{}
	if err := c.List(ctx, postgres, client.InNamespace(a.postgres.Namespace)); err != nil {
		return err
	}
	owners := make([]metav1.OwnerReference, 0, len(postgres.Items))
	for _, pg := range postgres.Items {
		// The deletion path invokes this only after its physical children are
		// gone. Keep other deleting parents until they reach their own cleanup.
		if pg.UID == a.postgres.UID && !a.postgres.DeletionTimestamp.IsZero() {
			continue
		}
		owners = append(owners, metav1.OwnerReference{APIVersion: v1.GroupVersion.String(), Kind: postgresKind, Name: pg.Name, UID: pg.UID})
	}
	slices.SortFunc(owners, func(a, b metav1.OwnerReference) int { return strings.Compare(a.Name, b.Name) })
	current := &monitoringv1.PrometheusRule{}
	err := c.Get(ctx, client.ObjectKeyFromObject(a.rule), current)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if apierrors.IsNotFound(err) {
		if len(owners) == 0 {
			return nil
		}
		created := a.rule.DeepCopy()
		created.OwnerReferences = owners
		return c.Create(ctx, created)
	}
	// Preserve other Postgres references from the fresh resource read as well:
	// a cached namespace list may not yet include a concurrently added owner.
	for _, owner := range current.OwnerReferences {
		if owner.APIVersion != v1.GroupVersion.String() || owner.Kind != postgresKind ||
			(owner.UID == a.postgres.UID && !a.postgres.DeletionTimestamp.IsZero()) {
			continue
		}
		if !slices.ContainsFunc(owners, func(candidate metav1.OwnerReference) bool { return candidate.UID == owner.UID }) {
			owner.Controller = nil
			owner.BlockOwnerDeletion = nil
			owners = append(owners, owner)
		}
	}
	if len(owners) == 0 {
		if current.Labels[rcmonitoring.ManagedLabel] != "true" {
			return nil // Never delete a rule we have not taken over.
		}
		return client.IgnoreNotFound(c.Delete(ctx, current, client.Preconditions{UID: &current.UID, ResourceVersion: &current.ResourceVersion}))
	}
	if !current.DeletionTimestamp.IsZero() {
		return fmt.Errorf("Postgres alert rule is terminating")
	}
	slices.SortFunc(owners, func(a, b metav1.OwnerReference) int { return strings.Compare(a.Name, b.Name) })
	original := current.DeepCopy()
	if current.Labels == nil {
		current.Labels = map[string]string{}
	}
	current.Labels[rcmonitoring.ManagedLabel] = "true"
	// Non-controller references allow the rule to survive deletion of any
	// single database. GC also covers concurrent deletion of the last parents.
	current.OwnerReferences = owners
	current.Spec = *a.rule.Spec.DeepCopy()
	if equality.Semantic.DeepEqual(original, current) {
		return nil
	}
	return c.Patch(ctx, current, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
}

func postgresAlertsWatch() reconciler.RelationshipWatch {
	return reconciler.RelationshipWatch{
		Type: &monitoringv1.PrometheusRule{},
		Predicate: predicate.And(predicate.ResourceVersionChangedPredicate{}, predicate.NewPredicateFuncs(func(object client.Object) bool {
			return object.GetName() == rcmonitoring.RuleName
		})),
		Map: func(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
			if object.GetName() != rcmonitoring.RuleName {
				return nil, nil
			}
			postgres := &v1.PostgresList{}
			if err := reader.List(ctx, postgres, client.InNamespace(object.GetNamespace())); err != nil {
				return nil, err
			}
			requests := make([]reconcile.Request, 0, len(postgres.Items))
			for i := range postgres.Items {
				requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&postgres.Items[i])})
			}
			return requests, nil
		},
	}
}
