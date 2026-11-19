package controller

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/nais/pgrator/internal/initscheme"
	rcmonitoring "github.com/nais/pgrator/internal/resourcecreator/postgresmonitoring"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPostgresAlertsNamespaceLifecycle(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	first := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", UID: "first", Finalizers: []string{"test"}}}
	second := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "analytics", Namespace: "team", UID: "second", Finalizers: []string{"test"}}, Spec: v1.PostgresSpec{HighAvailability: true}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(first, second).Build()
	ensure := func(pg *v1.Postgres) {
		t.Helper()
		a, err := newEnsurePostgresAlerts(pg, rcmonitoring.Options{})
		requireNoError(t, err)
		requireNoError(t, a.Do(t.Context(), c, scheme, nil))
	}
	get := func() *monitoringv1.PrometheusRule {
		t.Helper()
		rule := &monitoringv1.PrometheusRule{}
		requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: "team", Name: rcmonitoring.RuleName}, rule))
		return rule
	}

	ensure(first)
	ensure(second)
	rule := get()
	if len(rule.OwnerReferences) != 2 {
		t.Fatal("rule must be shared by both databases")
	}
	inventory := rule.Spec.Groups[0]
	if inventory.Name != "nais-postgres-inventory" {
		t.Fatal("databases must have inventory even without pods")
	}
	initialVersion := rule.ResourceVersion
	ensure(first)
	if get().ResourceVersion != initialVersion {
		t.Fatal("unchanged defaults should not cause another API write")
	}
	rule.Spec.Groups = nil
	requireNoError(t, c.Update(t.Context(), rule))
	ensure(first)
	defaults, err := rcmonitoring.Rule(first.Namespace, rcmonitoring.Options{}, *first, *second)
	requireNoError(t, err)
	if diff := cmp.Diff(defaults.Spec, get().Spec); diff != "" {
		t.Fatalf("defaults not reconciled: %s", diff)
	}

	requireNoError(t, c.Delete(t.Context(), first))
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(first), first))
	ensure(first)
	rule = get()
	if owners := rule.OwnerReferences; len(owners) != 1 || owners[0].UID != second.UID {
		t.Fatalf("first deletion must retain the shared rule: %v", owners)
	}
	for _, record := range rule.Spec.Groups[0].Rules {
		if record.Labels["postgres"] != second.Name {
			t.Fatal("intentional deletion must remove the database from alert inventory")
		}
	}
	first.Finalizers = nil
	requireNoError(t, c.Update(t.Context(), first))
	requireNoError(t, c.Delete(t.Context(), second))
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(second), second))
	ensure(second)
	if err := c.Get(t.Context(), client.ObjectKey{Namespace: "team", Name: rcmonitoring.RuleName}, &monitoringv1.PrometheusRule{}); !apierrors.IsNotFound(err) {
		t.Fatalf("last database cleanup did not remove the rule: %v", err)
	}
}
