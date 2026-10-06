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
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestPersonalAccessGroupHasNoLoginOrPrivileges(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	instance := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "restore"), Namespace: "team"}}
	group, err := createPersonalAccessGroupRole(scheme, instance)
	requireNoError(t, err)
	role := group.Spec.RoleConfiguration
	if group.Spec.ClusterRef.Name != rccnpg.ClusterNameFor(instance.Name) || role.Name != rccnpg.PersonalAccessRole(instance.Name) {
		t.Errorf("personal group not scoped to its physical instance: %#v", group.Spec)
	}
	if role.Login || !role.DisablePassword || role.Superuser || role.CreateDB || role.CreateRole || role.Replication || role.BypassRLS || len(role.InRoles) != 0 || group.Spec.ClientCertificate != nil {
		t.Errorf("personal authentication group can log in or grant privileges: %#v", group.Spec)
	}
	if group.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimDelete {
		t.Errorf("personal authentication group reclaim policy = %q", group.Spec.ReclaimPolicy)
	}
}

func TestPostgresBranchDeleteRespectsActiveBranch(t *testing.T) {
	reconciler := &PostgresBranchReconciler{Recorder: recorder}
	instance := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "primary"), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "primary"}}

	tests := []struct {
		name             string
		prep             PostgresBranchPreparedData
		wantRequeueAfter time.Duration
	}{
		{name: "blocked when active", prep: PostgresBranchPreparedData{ActiveBranch: "primary", PostgresDeleting: false}, wantRequeueAfter: 30 * time.Second},
		{name: "allowed when not active", prep: PostgresBranchPreparedData{ActiveBranch: "restore"}},
		{name: "allowed when Postgres is deleting", prep: PostgresBranchPreparedData{ActiveBranch: "primary", PostgresDeleting: true}},
		{name: "allowed when Postgres is gone", prep: PostgresBranchPreparedData{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, result, err := reconciler.Delete(instance, tt.prep, nil)
			requireNoError(t, err)
			if result.RequeueAfter != tt.wantRequeueAfter {
				t.Errorf("RequeueAfter = %v, want %v", result.RequeueAfter, tt.wantRequeueAfter)
			}
		})
	}
}

func TestPostgresBranchInheritsIdentityLabelsOnCNPGPods(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	reconciler := &PostgresBranchReconciler{
		Config: &config.Config{CNPG: config.CNPG{ImageCatalogName: "postgresql", StorageClass: "standard-rwo"}},
		Scheme: scheme,
	}
	branch := &v1.PostgresBranch{
		ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "main"), Namespace: "team", UID: "branch-uid"},
		Spec:       v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"},
	}
	actions, _, err := reconciler.Update(branch, PostgresBranchPreparedData{
		PostgresSpec: v1.PostgresSpec{MajorVersion: "18", Resources: v1.PostgresResources{
			Cpu: apiresource.MustParse("100m"), Memory: apiresource.MustParse("512Mi"), DiskSize: apiresource.MustParse("10Gi"),
		}},
	}, relatedobjectsmap.NewRelatedObjectsMap(scheme))
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range actions {
		cluster, ok := action.GetObject().(*cnpgv1.Cluster)
		if !ok {
			continue
		}
		labels := cluster.Spec.InheritedMetadata.Labels
		if labels["postgres.nais.io/name"] != "orders" || labels["postgres.nais.io/branch"] != "main" {
			t.Errorf("inherited Postgres identity labels = %v", labels)
		}
		return
	}
	t.Fatal("Update did not produce a CNPG Cluster")
}

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

func TestUpdateRepairsExistingWALBucketAccess(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)

	reconciler := &PostgresBranchReconciler{
		Config: &config.Config{
			GoogleProjectID: "cluster-gcp-project",
			Google:          config.Google{Location: "europe-north1"},
			CNPG:            config.CNPG{WalBucketPrefix: "wal-bucket-prefix"},
		},
		Scheme: scheme,
	}
	instance := &v1.PostgresBranch{
		ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("mydb", "main"), Namespace: "myteam", UID: "d3adb33f-beef-cafe-babe-700d1e100d1e"},
		Spec:       v1.PostgresBranchSpec{Postgres: "mydb", BranchName: "main"},
	}
	prepared := PostgresBranchPreparedData{
		PostgresUID:         types.UID("feedab1e-beef-cafe-babe-700d1e100d1e"),
		TeamGoogleProjectID: "team-gcp-project",
		PostgresSpec:        v1.PostgresSpec{MajorVersion: "18"},
	}
	relatedObjects := relatedobjectsmap.NewRelatedObjectsMap(scheme)
	relatedObjects.Insert(&storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{
		Name: reconcilerBucketName(instance, prepared), Namespace: instance.Namespace,
	}})

	actions, _, err := reconciler.Update(instance, prepared, relatedObjects)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	for _, plannedAction := range actions {
		bucket, ok := plannedAction.GetObject().(*storagecnrm.StorageBucket)
		if !ok {
			continue
		}
		if !bucket.Spec.UniformBucketLevelAccess {
			t.Fatal("existing WAL bucket was not reconciled with uniform bucket-level access")
		}
		return
	}
	t.Fatal("Update() did not reconcile the existing WAL bucket")
}

func TestUpdateDoesNotCreateScheduledBackupBeforeContinuousArchiving(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)

	reconciler := &PostgresBranchReconciler{
		Config: &config.Config{
			GoogleProjectID: "cluster-gcp-project",
			Google:          config.Google{Location: "europe-north1"},
			CNPG:            config.CNPG{WalBucketPrefix: "wal-bucket-prefix"},
		},
		Scheme: scheme,
	}
	instance := &v1.PostgresBranch{
		ObjectMeta: metav1.ObjectMeta{
			Name:      v1.PostgresBranchObjectName("mydb", "main"),
			Namespace: "myteam",
			UID:       "d3adb33f-beef-cafe-babe-700d1e100d1e",
		},
		Spec: v1.PostgresBranchSpec{Postgres: "mydb", BranchName: "main"},
	}

	actions, _, err := reconciler.Update(instance, PostgresBranchPreparedData{
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
	clusterEventFilter := (&PostgresBranchReconciler{Config: &config.Config{}}).OwnedTypes()[0].AdditionalPredicate

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
	clusterEventFilter := (&PostgresBranchReconciler{Config: &config.Config{}}).OwnedTypes()[0].AdditionalPredicate
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
			reconciler := &PostgresBranchReconciler{
				Config: &config.Config{
					GoogleProjectID: "cluster-gcp-project",
					Google:          config.Google{Location: "europe-north1"},
					CNPG:            config.CNPG{WalBucketPrefix: "wal-bucket-prefix"},
				},
				Scheme: scheme,
			}
			instance := &v1.PostgresBranch{
				ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("mydb", "main"), Namespace: "myteam", UID: "d3adb33f-beef-cafe-babe-700d1e100d1e"},
				Spec:       v1.PostgresBranchSpec{Postgres: "mydb", BranchName: "main"},
			}
			relatedObjects := relatedobjectsmap.NewRelatedObjectsMap(scheme)
			if tt.continuousArchive {
				relatedObjects.Insert(&cnpgv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{Name: rccnpg.ClusterNameFor(instance.Name), Namespace: "myteam"},
					Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{
						Type:   string(cnpgv1.ConditionContinuousArchiving),
						Status: metav1.ConditionTrue,
					}}},
				})
			}
			if tt.existingBackup {
				relatedObjects.Insert(&cnpgv1.ScheduledBackup{ObjectMeta: metav1.ObjectMeta{Name: rccnpg.ClusterNameFor(instance.Name), Namespace: "myteam"}})
			}

			actions, _, err := reconciler.Update(instance, PostgresBranchPreparedData{
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

func TestPrepareRecoveryUsesSourceBranchArchive(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	targetTime := metav1.NewTime(time.Date(2026, time.September, 9, 13, 10, 0, 0, time.UTC))
	restore := &v1.PostgresBranch{
		ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "restore"), Namespace: "team"},
		Spec: v1.PostgresBranchSpec{
			Postgres: "orders", BranchName: "restore",
			Bootstrap: &v1.PostgresBranchBootstrap{Recovery: &v1.PostgresBranchRecovery{
				SourceBranch: "primary",
				TargetTime:   targetTime,
			}},
		},
	}
	source := &v1.PostgresBranch{
		ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "primary"), Namespace: "team"},
		Spec:       v1.PostgresBranchSpec{Postgres: "orders", BranchName: "primary"},
	}
	archiveName := reconcilerBucketName(source, PostgresBranchPreparedData{PostgresUID: "feedab1e-beef-cafe-babe-700d1e100d1e"})
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team", UID: "feedab1e-beef-cafe-babe-700d1e100d1e"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team", Labels: map[string]string{ProjectIDLabel: "team-gcp-project"}}},
		source,
		&barmanv1.ObjectStore{ObjectMeta: metav1.ObjectMeta{
			Name:      archiveName,
			Namespace: "team",
			Labels:    map[string]string{rcstorage.OwnerNameLabel: v1.PostgresBranchObjectName("orders", "primary")},
		}},
		&storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: archiveName, Namespace: "team"}},
	).Build()
	reconciler := &PostgresBranchReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal-bucket-prefix"}}}

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
	if got, want := prepared.RecoverySource.ServerName, rccnpg.ClusterNameFor(v1.PostgresBranchObjectName("orders", "primary")); got != want {
		t.Errorf("recovery server = %q, want %q", got, want)
	}
}

func TestPrepareRecoveryRejectsInvalidProvenance(t *testing.T) {
	tests := []struct {
		name     string
		instance *v1.PostgresBranch
		source   *v1.PostgresBranch
		want     string
	}{
		{name: "missing source", instance: recoveryInstance("missing"), want: "getting recovery source branch"},
		{name: "self source", instance: recoveryInstance("restore"), want: "cannot be itself"},
		{name: "deleting source", instance: recoveryInstance("primary"), source: func() *v1.PostgresBranch {
			source := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "primary"), Namespace: "team", Finalizers: []string{"postgresbranch.nais.io"}}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "primary"}}
			now := metav1.Now()
			source.DeletionTimestamp = &now
			return source
		}(), want: "is being deleted"},
		{name: "other Postgres", instance: recoveryInstance("primary"), source: &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "primary"), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "other", BranchName: "primary"}}, want: "belongs to Postgres"},
		{name: "non UTC target", instance: func() *v1.PostgresBranch {
			instance := recoveryInstance("primary")
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
			_, _, err := (&PostgresBranchReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal"}}}).Prepare(context.Background(), reader, tt.instance)
			requireErrorContains(t, err, tt.want)
		})
	}
}

func TestPrepareCompletedRecoveryDoesNotRequireSource(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	instance := recoveryInstance("deleted-source")
	cluster := ownedRecoveryCluster(t, scheme, instance, true)
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team", Labels: map[string]string{ProjectIDLabel: "team-gcp-project"}}},
		cluster,
	).Build()
	_, _, err := (&PostgresBranchReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal"}}}).Prepare(context.Background(), reader, instance)
	if err != nil {
		t.Fatalf("preparing completed recovery without source: %v", err)
	}
}

func TestRecoverySourceChangesEnqueueRecoveries(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	recovery := recoveryInstance("primary")
	other := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("other", "restore"), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "other", BranchName: "restore", Bootstrap: &v1.PostgresBranchBootstrap{Recovery: &v1.PostgresBranchRecovery{SourceBranch: "primary"}}}}
	reader := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&v1.PostgresBranch{}, postgresBranchRecoverySourceIndex, recoverySourceBranchIndex).
		WithObjects(recovery, other).Build()
	reconciler := &PostgresBranchReconciler{}

	requests, err := reconciler.branchesForRecoverySource(context.Background(), reader, &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "primary"), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "primary"}})
	if err != nil {
		t.Fatalf("mapping source branch: %v", err)
	}
	if len(requests) != 1 || requests[0].Name != recovery.Name {
		t.Fatalf("source branch requests = %#v, want recovery %q", requests, recovery.Name)
	}
}

func TestRecoverySourceBucketChangesEnqueueRecoveries(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	recovery := recoveryInstance("primary")
	bucket := &storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: "orders-primary-archive", Namespace: "team"}}
	reader := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&v1.PostgresBranch{}, postgresBranchRecoverySourceIndex, recoverySourceBranchIndex).
		WithObjects(recovery, bucket, &barmanv1.ObjectStore{ObjectMeta: metav1.ObjectMeta{Name: bucket.Name, Namespace: bucket.Namespace, Labels: map[string]string{rcstorage.OwnerNameLabel: v1.PostgresBranchObjectName("orders", "primary")}}}).Build()
	reconciler := &PostgresBranchReconciler{}

	requests, err := reconciler.branchesForRecoverySourceBucket(context.Background(), reader, bucket)
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
	reconciler := &PostgresBranchReconciler{Config: &config.Config{
		GoogleProjectID: "cluster-gcp-project",
		Google:          config.Google{Location: "europe-north1"},
		CNPG:            config.CNPG{WalBucketPrefix: "wal-bucket-prefix"},
	}, Scheme: scheme}
	instance := recoveryInstance("primary")
	prepared := PostgresBranchPreparedData{
		PostgresUID:         "feedab1e-beef-cafe-babe-700d1e100d1e",
		TeamGoogleProjectID: "team-gcp-project",
		PostgresSpec:        v1.PostgresSpec{MajorVersion: "18"},
		RecoverySource:      &rccnpg.RecoverySource{BucketName: "orders-primary-archive", ServerName: rccnpg.ClusterNameFor(v1.PostgresBranchObjectName("orders", "primary")), TargetTime: instance.Spec.Bootstrap.Recovery.TargetTime},
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

	completedCluster := ownedRecoveryCluster(t, scheme, instance, true)
	related.Insert(completedCluster)
	prepared.RecoveryClusterUID = completedCluster.UID
	instance.GetStatus().SetCondition(metav1.Condition{Type: recoveryCompletedCondition, Status: metav1.ConditionTrue, Reason: "Completed", Message: string(completedCluster.UID)})
	actions, _, err = reconciler.Update(instance, prepared, related)
	if err != nil {
		t.Fatalf("updating completed recovery: %v", err)
	}
	assertSourcePolicyAction(t, actions, false)
}

func TestRecoveredBranchSurvivesSourceDeletionAndTemporaryOutage(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	instance := recoveryInstance("deleted-source")
	instance.Generation = 1
	cluster := ownedRecoveryCluster(t, scheme, instance, false)
	cluster.Generation = 3
	cluster.Status.Conditions = append(cluster.Status.Conditions, metav1.Condition{
		Type: "LastBackupSucceeded", Status: metav1.ConditionTrue, Reason: "BackupSucceeded",
		LastTransitionTime: metav1.NewTime(time.Date(2026, time.September, 9, 14, 0, 0, 0, time.UTC)),
	})
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresSpec{MajorVersion: "18"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team", Labels: map[string]string{ProjectIDLabel: "team-gcp-project"}}},
		cluster,
	).Build()
	reconciler := &PostgresBranchReconciler{Config: &config.Config{
		GoogleProjectID: "cluster-gcp-project", Google: config.Google{Location: "europe-north1"},
		CNPG: config.CNPG{WalBucketPrefix: "wal-bucket-prefix"},
	}, Scheme: scheme}

	prepared, _, err := reconciler.Prepare(context.Background(), reader, instance)
	if err != nil {
		t.Fatalf("preparing previously recovered branch without source: %v", err)
	}
	if got := completedRecoveryClusterUID(instance); got != string(cluster.UID) {
		t.Fatalf("recovery milestone = %q, want cluster UID %q", got, cluster.UID)
	}
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme)
	related.Insert(cluster)
	actions, _, err := reconciler.Update(instance, prepared, related)
	if err != nil {
		t.Fatalf("updating previously recovered branch: %v", err)
	}
	assertSourcePolicyAction(t, actions, false)
	for _, planned := range actions {
		if updated, ok := planned.GetObject().(*cnpgv1.Cluster); ok {
			if updated.Spec.ExternalClusters != nil || updated.Spec.Bootstrap == nil || updated.Spec.Bootstrap.Recovery != nil {
				t.Fatalf("cluster still depends on deleted source: %#v", updated)
			}
			return
		}
	}
	t.Fatal("no CNPG Cluster update planned")
}

func TestRecoveryClusterCannotChangeBetweenPreparationAndUpdate(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	for _, tc := range []struct {
		name                 string
		presentAtPreparation bool
		clusterAtUpdate      string
		wantError            bool
	}{
		{name: "same cluster", presentAtPreparation: true, clusterAtUpdate: "original"},
		{name: "replaced cluster", presentAtPreparation: true, clusterAtUpdate: "replacement", wantError: true},
		{name: "removed cluster", presentAtPreparation: true, wantError: true},
		{name: "new cluster after preparation", clusterAtUpdate: "replacement", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instance := recoveryInstance("primary")
			original := ownedRecoveryCluster(t, scheme, instance, true)
			source := &v1.PostgresBranch{
				ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "primary"), Namespace: "team"},
				Spec:       v1.PostgresBranchSpec{Postgres: "orders", BranchName: "primary"},
			}
			objects := []client.Object{
				&v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}, Spec: v1.PostgresSpec{MajorVersion: "18"}},
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team", Labels: map[string]string{ProjectIDLabel: "team-gcp-project"}}},
				source,
				&barmanv1.ObjectStore{ObjectMeta: metav1.ObjectMeta{Name: "source-archive", Namespace: "team", Labels: map[string]string{rcstorage.OwnerNameLabel: source.Name}}},
				&storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: "source-archive", Namespace: "team"}},
			}
			if tc.presentAtPreparation {
				objects = append(objects, original)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			reconciler := &PostgresBranchReconciler{Config: &config.Config{
				GoogleProjectID: "cluster-gcp-project", Google: config.Google{Location: "europe-north1"},
				CNPG: config.CNPG{WalBucketPrefix: "wal-bucket-prefix"},
			}, Scheme: scheme, Recorder: recorder}
			prepared, _, err := reconciler.Prepare(context.Background(), reader, instance)
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			related := relatedobjectsmap.NewRelatedObjectsMap(scheme)
			switch tc.clusterAtUpdate {
			case "original":
				related.Insert(original)
			case "replacement":
				replacement := original.DeepCopy()
				replacement.UID = "replacement-uid"
				related.Insert(replacement)
			}
			actions, _, err := reconciler.Update(instance, prepared, related)
			if tc.wantError {
				requireErrorContains(t, err, "changed since preparation")
				if len(actions) != 0 {
					t.Fatalf("planned %d actions for a changed recovery cluster", len(actions))
				}
				return
			}
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			assertClusterAction(t, actions)
		})
	}
}

func TestUnfinishedRecoveryStillRequiresSource(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	instance := recoveryInstance("deleted-source")
	cluster := ownedRecoveryCluster(t, scheme, instance, false)
	reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team", Labels: map[string]string{ProjectIDLabel: "team-gcp-project"}}},
		cluster,
	).Build()
	reconciler := &PostgresBranchReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal-bucket-prefix"}}}
	_, _, err := reconciler.Prepare(context.Background(), reader, instance)
	requireErrorContains(t, err, "getting recovery source branch")
}

func TestCompletedRecoveryDoesNotRebootstrapMissingOrReplacedCluster(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	for _, tc := range []struct {
		name    string
		cluster bool
	}{
		{name: "missing cluster"},
		{name: "replaced cluster", cluster: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instance := recoveryInstance("primary")
			instance.UID = "branch-uid"
			instance.GetStatus().SetCondition(metav1.Condition{
				Type: recoveryCompletedCondition, Status: metav1.ConditionTrue,
				Reason: "Completed", Message: "original-cluster-uid",
			})
			objects := []client.Object{
				&v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}},
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "team", Labels: map[string]string{ProjectIDLabel: "team-gcp-project"}}},
			}
			if tc.cluster {
				objects = append(objects, ownedRecoveryCluster(t, scheme, instance, true))
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			_, _, err := (&PostgresBranchReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal-bucket-prefix"}}}).Prepare(context.Background(), reader, instance)
			requireErrorContains(t, err, "refusing to bootstrap")
		})
	}
}

func TestDeletingRecoveredBranchDoesNotRequireSource(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	instance := recoveryInstance("deleted-source")
	now := metav1.Now()
	instance.DeletionTimestamp = &now
	reader := fake.NewClientBuilder().WithScheme(scheme).
		WithIndex(&v1.PostgresBranch{}, postgresBranchRecoverySourceIndex, recoverySourceBranchIndex).
		WithObjects(&v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}}).Build()
	_, _, err := (&PostgresBranchReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal-bucket-prefix"}}}).Prepare(context.Background(), reader, instance)
	if err != nil {
		t.Fatalf("preparing branch deletion without source or Cluster: %v", err)
	}
}

func TestSourceDeletionWaitsForDependentRecoveryToDetach(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	for _, tc := range []struct {
		name      string
		cluster   bool
		detached  bool
		wantBlock bool
	}{
		{name: "recovery not started", wantBlock: true},
		{name: "recovery still references source", cluster: true, wantBlock: true},
		{name: "recovery detached from source", cluster: true, detached: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "primary"), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "primary"}}
			now := metav1.Now()
			source.DeletionTimestamp = &now
			source.Finalizers = []string{"postgresbranch.nais.io"}
			dependent := recoveryInstance("primary")
			objects := []client.Object{
				&v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "team"}},
				source, dependent,
			}
			if tc.cluster {
				cluster := ownedRecoveryCluster(t, scheme, dependent, tc.detached)
				if tc.detached {
					cluster.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{InitDB: &cnpgv1.BootstrapInitDB{}}
					dependent.GetStatus().SetCondition(metav1.Condition{Type: recoveryCompletedCondition, Status: metav1.ConditionTrue, Reason: "Completed", Message: string(cluster.UID)})
				}
				objects = append(objects, cluster)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).
				WithIndex(&v1.PostgresBranch{}, postgresBranchRecoverySourceIndex, recoverySourceBranchIndex).
				WithObjects(objects...).Build()
			reconciler := &PostgresBranchReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal-bucket-prefix"}}, Recorder: recorder}
			prepared, _, err := reconciler.Prepare(context.Background(), reader, source)
			if err != nil {
				t.Fatal(err)
			}
			_, result, err := reconciler.Delete(source, prepared, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := !result.IsZero(); got != tc.wantBlock {
				t.Errorf("deletion blocked = %t, want %t", got, tc.wantBlock)
			}
		})
	}
}

func ownedRecoveryCluster(t *testing.T, scheme *runtime.Scheme, instance *v1.PostgresBranch, ready bool) *cnpgv1.Cluster {
	t.Helper()
	if instance.UID == "" {
		instance.UID = "branch-uid"
	}
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: rccnpg.ClusterNameFor(instance.Name), Namespace: instance.Namespace, UID: "cluster-uid", CreationTimestamp: metav1.NewTime(time.Date(2026, time.September, 9, 12, 0, 0, 0, time.UTC))},
		Spec:       cnpgv1.ClusterSpec{Bootstrap: &cnpgv1.BootstrapConfiguration{Recovery: &cnpgv1.BootstrapRecovery{Source: "recovery-source"}}},
		Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{
			{Type: string(cnpgv1.ConditionInitialized), Status: metav1.ConditionTrue},
			{Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionFalse},
		}},
	}
	if ready {
		cluster.Status.Conditions[1].Status = metav1.ConditionTrue
		cluster.Status.Conditions = append(cluster.Status.Conditions, metav1.Condition{
			Type: "LastBackupSucceeded", Status: metav1.ConditionTrue, Reason: "BackupSucceeded",
			LastTransitionTime: metav1.NewTime(time.Date(2026, time.September, 9, 14, 0, 0, 0, time.UTC)),
		})
	}
	if err := controllerutil.SetControllerReference(instance, cluster, scheme); err != nil {
		t.Fatal(err)
	}
	return cluster
}

func recoveryInstance(source string) *v1.PostgresBranch {
	return &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "restore"), Namespace: "team"}, Spec: v1.PostgresBranchSpec{Postgres: "orders", BranchName: "restore", Bootstrap: &v1.PostgresBranchBootstrap{Recovery: &v1.PostgresBranchRecovery{SourceBranch: source, TargetTime: metav1.NewTime(time.Date(2026, time.September, 9, 13, 10, 0, 0, time.UTC))}}}}
}

func readyRecoveryInfrastructure(t *testing.T, scheme *runtime.Scheme, reconciler *PostgresBranchReconciler, instance *v1.PostgresBranch, prepared PostgresBranchPreparedData) *relatedobjectsmap.RelatedObjectsMap {
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

func reconcilerBucketName(instance *v1.PostgresBranch, prepared PostgresBranchPreparedData) string {
	return (&PostgresBranchReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal-bucket-prefix"}}}).bucketName(instance, prepared)
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
		if plannedAction.GetObject().GetName() == recoverySourcePolicyNameFor(v1.PostgresBranchObjectName("orders", "restore")) {
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
