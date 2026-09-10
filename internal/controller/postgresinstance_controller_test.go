package controller

import (
	"context"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/initscheme"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	rciam "github.com/nais/pgrator/internal/resourcecreator/iam"
	rcstorage "github.com/nais/pgrator/internal/resourcecreator/storage"
	syncaction "github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

	for _, plannedAction := range actions {
		if _, ok := plannedAction.GetObject().(*cnpgv1.ScheduledBackup); ok {
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

func TestRecoveryCompletionStatusChangeTriggersReconcile(t *testing.T) {
	clusterEventFilter := (&PostgresInstanceReconciler{Config: &config.Config{}}).OwnedTypes()[0].AdditionalPredicate
	oldCluster := &cnpgv1.Cluster{}
	completedCluster := &cnpgv1.Cluster{Status: cnpgv1.ClusterStatus{
		Conditions: []metav1.Condition{{
			Type:   string(cnpgv1.ConditionInitialized),
			Status: metav1.ConditionTrue,
		}, {
			Type:   string(cnpgv1.ConditionClusterReady),
			Status: metav1.ConditionTrue,
		}},
	}}
	if !clusterEventFilter.Update(event.UpdateEvent{ObjectOld: oldCluster, ObjectNew: completedCluster}) {
		t.Error("recovery completion did not trigger reconcile")
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

func TestPrepareRecoveryRejectsInvalidProvenance(t *testing.T) {
	tests := []struct {
		name     string
		instance *v1.PostgresInstance
		source   *v1.PostgresInstance
		want     string
	}{
		{name: "missing source", instance: recoveryInstance("missing"), want: "getting recovery source instance"},
		{name: "self source", instance: recoveryInstance("orders-restore"), want: "cannot be itself"},
		{name: "other Postgres", instance: recoveryInstance("other-primary"), source: &v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "other-primary", Namespace: "team"}, Spec: v1.PostgresInstanceSpec{Postgres: "other"}}, want: "belongs to Postgres"},
		{name: "non UTC target", instance: func() *v1.PostgresInstance {
			instance := recoveryInstance("orders-primary")
			instance.Spec.Bootstrap.Recovery.TargetTime = metav1.NewTime(time.Date(2026, time.September, 9, 13, 10, 0, 0, time.FixedZone("CEST", 7200)))
			return instance
		}(), want: "must be UTC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			objects := []client.Object{
				&v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}},
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team", Labels: map[string]string{ProjectIDLabel: "team-gcp-project"}}},
			}
			if tt.source != nil {
				objects = append(objects, tt.source)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			_, _, err := (&PostgresInstanceReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal"}}}).Prepare(context.Background(), reader, tt.instance)
			requireErrorContains(t, err, tt.want)
		})
	}
}

func TestPrepareCompletedRecoveryDoesNotRequireSource(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	instance := recoveryInstance("deleted-source")
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team", Labels: map[string]string{ProjectIDLabel: "team-gcp-project"}}},
		&cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg-orders-restore", Namespace: "team"}, Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{Type: string(cnpgv1.ConditionInitialized), Status: metav1.ConditionTrue}, {Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionTrue}}}},
	).Build()
	_, _, err := (&PostgresInstanceReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal"}}}).Prepare(context.Background(), reader, instance)
	if err != nil {
		t.Fatalf("preparing completed recovery without source: %v", err)
	}
}

func TestRecoverySourceChangesEnqueueRecoveries(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	recovery := recoveryInstance("orders-primary")
	reader := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&v1.PostgresInstance{}, postgresInstanceRecoverySourceIndex, recoverySourceInstanceIndex).
		WithObjects(recovery).Build()
	reconciler := &PostgresInstanceReconciler{}

	requests, err := reconciler.instancesForRecoverySource(context.Background(), reader, &v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders-primary", Namespace: "team"}})
	if err != nil {
		t.Fatalf("mapping source instance: %v", err)
	}
	if len(requests) != 1 || requests[0].Name != recovery.Name {
		t.Fatalf("source instance requests = %#v, want recovery %q", requests, recovery.Name)
	}
}

func TestRecoverySourceBucketChangesEnqueueRecoveries(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	recovery := recoveryInstance("orders-primary")
	bucket := &storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: "orders-primary-archive", Namespace: "team"}}
	reader := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&v1.PostgresInstance{}, postgresInstanceRecoverySourceIndex, recoverySourceInstanceIndex).
		WithObjects(recovery, bucket, &barmanv1.ObjectStore{ObjectMeta: metav1.ObjectMeta{Name: bucket.Name, Namespace: bucket.Namespace, Labels: map[string]string{rcstorage.OwnerNameLabel: "orders-primary"}}}).Build()
	reconciler := &PostgresInstanceReconciler{}

	requests, err := reconciler.instancesForRecoverySourceBucket(context.Background(), reader, bucket)
	if err != nil {
		t.Fatalf("mapping source bucket: %v", err)
	}
	if len(requests) != 1 || requests[0].Name != recovery.Name {
		t.Fatalf("source bucket requests = %#v, want recovery %q", requests, recovery.Name)
	}
}

func TestUpdateRecoveryWaitsForInfrastructureAndRemovesSourcePolicyWhenComplete(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	reconciler := &PostgresInstanceReconciler{Config: &config.Config{
		GoogleProjectID: "cluster-gcp-project",
		Google:          config.Google{Location: "europe-north1"},
		CNPG:            config.CNPG{WalBucketPrefix: "wal-bucket-prefix"},
	}, Scheme: scheme}
	instance := recoveryInstance("orders-primary")
	prepared := PostgresInstancePreparedData{
		PostgresUID:         "feedab1e-beef-cafe-babe-700d1e100d1e",
		TeamGoogleProjectID: "team-gcp-project",
		PostgresSpec:        v1.PostgresSpec{MajorVersion: "18"},
		RecoverySource:      &rccnpg.RecoverySource{BucketName: "orders-primary-archive", ServerName: "pg-orders-primary", TargetTime: instance.Spec.Bootstrap.Recovery.TargetTime},
	}

	actions, _, err := reconciler.Update(instance, prepared, relatedobjectsmap.NewRelatedObjectsMap(scheme))
	if err != nil {
		t.Fatalf("updating recovery without prerequisites: %v", err)
	}
	assertNoClusterAction(t, actions)
	assertSourcePolicyAction(t, actions, true)

	prepared.RecoverySourceReady = true
	related := readyRecoveryInfrastructure(t, scheme, reconciler, instance, prepared)
	actions, _, err = reconciler.Update(instance, prepared, related)
	if err != nil {
		t.Fatalf("updating recovery with prerequisites: %v", err)
	}
	assertClusterAction(t, actions)
	assertSourcePolicyAction(t, actions, true)

	related.Insert(&cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "pg-orders-restore", Namespace: "team"}, Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{Type: string(cnpgv1.ConditionInitialized), Status: metav1.ConditionTrue}, {Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionTrue}}}})
	actions, _, err = reconciler.Update(instance, prepared, related)
	if err != nil {
		t.Fatalf("updating completed recovery: %v", err)
	}
	assertSourcePolicyAction(t, actions, false)
}

func recoveryInstance(source string) *v1.PostgresInstance {
	return &v1.PostgresInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders-restore", Namespace: "team"}, Spec: v1.PostgresInstanceSpec{Postgres: "orders", Bootstrap: &v1.PostgresInstanceBootstrap{Recovery: &v1.PostgresInstanceRecovery{SourceInstance: source, TargetTime: metav1.NewTime(time.Date(2026, time.September, 9, 13, 10, 0, 0, time.UTC))}}}}
}

func readyRecoveryInfrastructure(t *testing.T, scheme *runtime.Scheme, reconciler *PostgresInstanceReconciler, instance *v1.PostgresInstance, prepared PostgresInstancePreparedData) *relatedobjectsmap.RelatedObjectsMap {
	t.Helper()
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme)
	ready := func(object client.Object) client.Object {
		switch resource := object.(type) {
		case *iamcnrm.IAMServiceAccount:
			resource.Generation = 1
			resource.Status.ObservedGeneration = 1
			resource.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
		case *iamcnrm.IAMPolicyMember:
			resource.Generation = 1
			resource.Status.ObservedGeneration = 1
			resource.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
		case *storagecnrm.StorageBucket:
			generation := int64(1)
			resource.Generation = generation
			resource.Status.ObservedGeneration = &generation
			resource.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
		}
		return object
	}
	for _, object := range []client.Object{
		rciam.CreateIAMServiceAccount(gsaNameFor(instance.Name), instance.Namespace),
		rciam.CreateWorkloadIdentityPolicyMember(workloadIdentityPolicyNameFor(instance.Name), instance.Namespace, instance.Namespace, reconciler.Config.GoogleProjectID, gsaNameFor(instance.Name), rccnpg.ClusterNameFor(instance.Name)),
		rciam.CreateStorageBucketPolicyMember(storageBucketPolicyNameFor(instance.Name), instance.Namespace, prepared.TeamGoogleProjectID, gsaNameFor(instance.Name), reconcilerBucketName(instance, prepared), rciam.StorageObjectUserRole),
		rciam.CreateStorageBucketPolicyMember(storageBucketViewerPolicyNameFor(instance.Name), instance.Namespace, prepared.TeamGoogleProjectID, gsaNameFor(instance.Name), reconcilerBucketName(instance, prepared), rciam.StorageBucketViewerRole),
		rciam.CreateStorageBucketPolicyMember(recoverySourcePolicyNameFor(instance.Name), instance.Namespace, prepared.TeamGoogleProjectID, gsaNameFor(instance.Name), prepared.RecoverySource.BucketName, rciam.StorageObjectViewerRole),
		&storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: reconcilerBucketName(instance, prepared), Namespace: instance.Namespace}},
	} {
		related.Insert(ready(object))
	}
	related.Insert(&barmanv1.ObjectStore{ObjectMeta: metav1.ObjectMeta{Name: reconcilerBucketName(instance, prepared), Namespace: instance.Namespace}})
	return related
}

func reconcilerBucketName(instance *v1.PostgresInstance, prepared PostgresInstancePreparedData) string {
	return (&PostgresInstanceReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal-bucket-prefix"}}}).bucketName(instance, prepared)
}

func assertClusterAction(t *testing.T, actions []syncaction.Action) {
	t.Helper()
	for _, plannedAction := range actions {
		if _, ok := plannedAction.GetObject().(*cnpgv1.Cluster); ok {
			return
		}
	}
	t.Fatal("actions did not include Cluster")
}

func assertNoClusterAction(t *testing.T, actions []syncaction.Action) {
	t.Helper()
	for _, plannedAction := range actions {
		if _, ok := plannedAction.GetObject().(*cnpgv1.Cluster); ok {
			t.Fatal("actions unexpectedly included Cluster")
		}
	}
}

func assertSourcePolicyAction(t *testing.T, actions []syncaction.Action, want bool) {
	t.Helper()
	for _, plannedAction := range actions {
		if plannedAction.GetObject().GetName() == recoverySourcePolicyNameFor("orders-restore") {
			if !want {
				t.Fatal("actions unexpectedly included recovery source policy")
			}
			return
		}
	}
	if want {
		t.Fatal("actions did not include recovery source policy")
	}
}
