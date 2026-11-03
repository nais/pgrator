package controller

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/initscheme"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

func TestPostgresCRDValidatesDatabaseExtensions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		extension string
		wantErr   string
	}{
		{name: "image extension", extension: "postgis"},
		{name: "plain extension", extension: "pg_trgm"},
		{name: "hyphenated extension", extension: "uuid-ossp"},
		{name: "platform managed pg_stat_statements", extension: "pg_stat_statements", wantErr: "managed by the platform"},
		{name: "platform managed pgaudit", extension: "pgaudit", wantErr: "managed by the platform"},
		{name: "SQL injection attempt", extension: `x"; DROP DATABASE app; --`, wantErr: "should match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			postgres := &v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{GenerateName: "dbext-", Namespace: "default"},
				Spec: v1.PostgresSpec{
					MajorVersion: "18",
					Extensions:   []v1.PostgresExtension{{Name: tc.extension}},
				},
			}
			err := k8sClient.Create(ctx, postgres)
			if tc.wantErr == "" {
				requireNoError(t, err)
				requireNoError(t, k8sClient.Delete(ctx, postgres))
				return
			}
			requireErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestDatabaseConditionGetter(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	for _, tc := range []struct {
		name               string
		applied            *bool
		observedGeneration int64
		message            string
		wantStatus         metav1.ConditionStatus
		wantReason         string
		wantMessage        string
	}{
		{name: "not yet reconciled", wantStatus: metav1.ConditionUnknown, wantReason: "Pending", wantMessage: "Waiting for CloudNativePG to reconcile the Database"},
		{name: "stale generation", applied: new(true), observedGeneration: 1, wantStatus: metav1.ConditionUnknown, wantReason: "Pending", wantMessage: "Waiting for CloudNativePG to reconcile the Database"},
		{name: "applied", applied: new(true), observedGeneration: 2, wantStatus: metav1.ConditionTrue, wantReason: "Applied", wantMessage: "Database reconciled by CloudNativePG"},
		{
			name: "extension failure", applied: new(false), observedGeneration: 2, message: `extension "timescaledb" is not available`,
			wantStatus: metav1.ConditionFalse, wantReason: "ReconciliationFailed", wantMessage: `extension "timescaledb" is not available`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := &cnpgv1.Database{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Status:     cnpgv1.DatabaseStatus{Applied: tc.applied, ObservedGeneration: tc.observedGeneration, Message: tc.message},
			}
			conditions := databaseConditionGetter(database, scheme)
			if len(conditions) != 1 {
				t.Fatalf("got %d conditions, want 1", len(conditions))
			}
			condition := conditions[0]
			if condition.Type != "database.postgresql.cnpg.io/Available" || condition.ObservedGeneration != database.Generation {
				t.Errorf("unexpected condition metadata: %+v", condition)
			}
			if condition.Status != tc.wantStatus || condition.Reason != tc.wantReason || condition.Message != tc.wantMessage {
				t.Errorf("got condition %+v, want status %q, reason %q, message %q", condition, tc.wantStatus, tc.wantReason, tc.wantMessage)
			}
		})
	}
}

func TestDatabaseStatusChangeTriggersReconcile(t *testing.T) {
	var filter predicate.Predicate
	for _, ownedType := range (&PostgresBranchReconciler{Config: &config.Config{}}).OwnedTypes() {
		if _, ok := ownedType.Type.(*cnpgv1.Database); ok {
			filter = ownedType.AdditionalPredicate
		}
	}
	if filter == nil {
		t.Fatal("Database owned type has no status predicate")
	}
	pending := &cnpgv1.Database{ObjectMeta: metav1.ObjectMeta{Generation: 1}}
	failed := pending.DeepCopy()
	failed.Status = cnpgv1.DatabaseStatus{ObservedGeneration: 1, Applied: new(false), Message: "extension failure"}
	if !filter.Update(event.UpdateEvent{ObjectOld: pending, ObjectNew: failed}) {
		t.Error("Database status change does not trigger reconciliation")
	}
	if filter.Update(event.UpdateEvent{ObjectOld: failed, ObjectNew: failed.DeepCopy()}) {
		t.Error("unchanged Database status triggers reconciliation")
	}
}
