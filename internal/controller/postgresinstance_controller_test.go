package controller

import (
	"context"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/initscheme"
	rcstorage "github.com/nais/pgrator/internal/resourcecreator/storage"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestContinuousArchivingReady(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		want       bool
	}{
		{
			name: "is not ready without a condition",
		},
		{
			name: "is not ready when condition is false",
			conditions: []metav1.Condition{{
				Type:   string(cnpgv1.ConditionContinuousArchiving),
				Status: metav1.ConditionFalse,
			}},
		},
		{
			name: "is ready when condition is true",
			conditions: []metav1.Condition{{
				Type:   string(cnpgv1.ConditionContinuousArchiving),
				Status: metav1.ConditionTrue,
			}},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := &cnpgv1.Cluster{Status: cnpgv1.ClusterStatus{Conditions: tt.conditions}}
			if got := continuousArchivingReady(cluster); got != tt.want {
				t.Errorf("continuousArchivingReady() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestUpdateDoesNotCreateScheduledBackupBeforeContinuousArchiving(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)

	reconciler := &PostgresInstanceReconciler{
		Config: &config.Config{
			GoogleProjectID: "cluster-gcp-project",
			Google:          config.Google{Location: "europe-north1"},
			CNPG:            config.CNPG{WalBucketPrefix: "wal-bucket-prefix"},
		},
		Scheme: scheme,
	}
	instance := &v1.PostgresInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mydb",
			Namespace: "myteam",
			UID:       "d3adb33f-beef-cafe-babe-700d1e100d1e",
		},
		Spec: v1.PostgresInstanceSpec{Postgres: "mydb"},
	}

	actions, _, err := reconciler.Update(instance, PostgresInstancePreparedData{
		PostgresUID:         types.UID("feedab1e-beef-cafe-babe-700d1e100d1e"),
		TeamGoogleProjectID: "team-gcp-project",
		PostgresSpec:        v1.PostgresSpec{MajorVersion: "18"},
	}, relatedobjectsmap.NewRelatedObjectsMap(scheme))
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	for _, action := range actions {
		if _, ok := action.GetObject().(*cnpgv1.ScheduledBackup); ok {
			t.Fatal("Update() created ScheduledBackup before continuous archiving was ready")
		}
	}
}

func TestContinuousArchivingStatusChangeTriggersReconcile(t *testing.T) {
	clusterEventFilter := (&PostgresInstanceReconciler{Config: &config.Config{}}).OwnedTypes()[0].AdditionalPredicate

	oldCluster := &cnpgv1.Cluster{}
	readyCluster := &cnpgv1.Cluster{Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{
		Type:   string(cnpgv1.ConditionContinuousArchiving),
		Status: metav1.ConditionTrue,
	}}}}
	if !clusterEventFilter.Update(event.UpdateEvent{ObjectOld: oldCluster, ObjectNew: readyCluster}) {
		t.Error("ContinuousArchiving transition to ready did not trigger reconcile")
	}
}

func TestUpdateCreatesOrKeepsScheduledBackupWhenEligible(t *testing.T) {
	tests := []struct {
		name              string
		continuousArchive bool
		existingBackup    bool
	}{
		{name: "creates after continuous archiving is ready", continuousArchive: true},
		{name: "keeps existing backup if archiving later becomes unavailable", existingBackup: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			reconciler := &PostgresInstanceReconciler{
				Config: &config.Config{
					GoogleProjectID: "cluster-gcp-project",
					Google:          config.Google{Location: "europe-north1"},
					CNPG:            config.CNPG{WalBucketPrefix: "wal-bucket-prefix"},
				},
				Scheme: scheme,
			}
			instance := &v1.PostgresInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "mydb", Namespace: "myteam", UID: "d3adb33f-beef-cafe-babe-700d1e100d1e"},
				Spec:       v1.PostgresInstanceSpec{Postgres: "mydb"},
			}
			relatedObjects := relatedobjectsmap.NewRelatedObjectsMap(scheme)
			if tt.continuousArchive {
				relatedObjects.Insert(&cnpgv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{Name: "pg-mydb", Namespace: "myteam"},
					Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{
						Type:   string(cnpgv1.ConditionContinuousArchiving),
						Status: metav1.ConditionTrue,
					}}},
				})
			}
			if tt.existingBackup {
				relatedObjects.Insert(&cnpgv1.ScheduledBackup{ObjectMeta: metav1.ObjectMeta{Name: "pg-mydb", Namespace: "myteam"}})
			}

			actions, _, err := reconciler.Update(instance, PostgresInstancePreparedData{
				PostgresUID:         types.UID("feedab1e-beef-cafe-babe-700d1e100d1e"),
				TeamGoogleProjectID: "team-gcp-project",
				PostgresSpec:        v1.PostgresSpec{MajorVersion: "18"},
			}, relatedObjects)
			if err != nil {
				t.Fatalf("Update() error = %v", err)
			}

			for _, action := range actions {
				if _, ok := action.GetObject().(*cnpgv1.ScheduledBackup); ok {
					return
				}
			}
			t.Fatal("Update() did not create or keep ScheduledBackup")
		})
	}
}

func TestPrepareRecoveryUsesSourceInstanceArchive(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	targetTime := metav1.NewTime(time.Date(2026, time.September, 9, 13, 10, 0, 0, time.UTC))
	restore := &v1.PostgresInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-restore", Namespace: "team"},
		Spec: v1.PostgresInstanceSpec{
			Postgres: "orders",
			Bootstrap: &v1.PostgresInstanceBootstrap{Recovery: &v1.PostgresInstanceRecovery{
				SourceInstance: "orders-primary",
				TargetTime:     targetTime,
			}},
		},
	}
	source := &v1.PostgresInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "orders-primary", Namespace: "team"},
		Spec:       v1.PostgresInstanceSpec{Postgres: "orders"},
	}
	archiveName := "wal-bucket-prefix-team-orders-primary-feedab1ebeef"
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", UID: "feedab1e-beef-cafe-babe-700d1e100d1e"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team", Labels: map[string]string{ProjectIDLabel: "team-gcp-project"}}},
		source,
		&barmanv1.ObjectStore{ObjectMeta: metav1.ObjectMeta{
			Name:      archiveName,
			Namespace: "team",
			Labels:    map[string]string{rcstorage.OwnerNameLabel: "orders-primary"},
		}},
		&storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: archiveName, Namespace: "team"}},
	).Build()
	reconciler := &PostgresInstanceReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal-bucket-prefix"}}}

	prepared, _, err := reconciler.Prepare(context.Background(), reader, restore)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if prepared.RecoverySource == nil {
		t.Fatal("Prepare() did not resolve recovery source")
	}
	if got, want := prepared.RecoverySource.BucketName, archiveName; got != want {
		t.Errorf("recovery bucket = %q, want %q", got, want)
	}
	if got, want := prepared.RecoverySource.ServerName, "pg-orders-primary"; got != want {
		t.Errorf("recovery server = %q, want %q", got, want)
	}
}
