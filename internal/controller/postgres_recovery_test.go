package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/nais/pgrator/internal/config"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func directRecoveryFixture(t *testing.T) (*PostgresClusterReconciler, client.Client, *v1.Postgres, *v1.Postgres) {
	t.Helper()
	r, c, source, pg := unacceptedDirectRecoveryFixture(t)
	owners := ownership.NewOwnerManager(r.Name() + "/owner")
	requireNoError(t, (&protectPostgres{postgres: pg}).Do(t.Context(), c, r.Scheme, owners))
	requireNoError(t, newAcceptPostgresRecovery(pg, source, source.UID, "postgres.nais.io").Do(t.Context(), c, r.Scheme, owners))
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(source), source))
	return r, c, source, pg
}

func unacceptedDirectRecoveryFixture(t *testing.T) (*PostgresClusterReconciler, client.Client, *v1.Postgres, *v1.Postgres) {
	t.Helper()
	r, c, source, _, _ := directOwnershipFixture(t, true)
	sourceCluster := &cnpgv1.Cluster{}
	requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: source.Namespace, Name: source.Status.ClusterName}, sourceCluster))
	bucket := ""
	for _, plugin := range sourceCluster.Spec.Plugins {
		if plugin.Name == rccnpg.BarmanPluginName {
			bucket = plugin.Parameters["barmanObjectName"]
		}
	}
	if bucket == "" {
		t.Fatal("fixture has no WAL archive")
	}
	pg := &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "restored",
			Namespace:   source.Namespace,
			UID:         "restored-uid",
			Annotations: map[string]string{v1.PostgresRecoveryAnnotation: "restored"},
		},
		Spec: *source.Spec.DeepCopy(),
	}
	requireNoError(t, c.Create(t.Context(), pg))
	rec := &v1.PostgresRecovery{
		ObjectMeta: metav1.ObjectMeta{Name: pg.Name, Namespace: pg.Namespace, UID: "restored-recovery-uid"},
		Spec: v1.PostgresRecoverySpec{
			Postgres: pg.Name,
			PointInTime: &v1.PostgresPointInTimeRecovery{
				SourcePostgres: source.Name,
				SourceUID:      string(source.UID),
				BucketName:     bucket,
				ServerName:     source.Status.ClusterName,
				TargetTime:     metav1.NewTime(time.Now().UTC().Truncate(time.Second).Add(-time.Hour)),
			},
		},
	}
	requireNoError(t, controllerutil.SetOwnerReference(pg, rec, r.Scheme))
	requireNoError(t, c.Create(t.Context(), rec))
	_, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	requireNoError(t, c.Status().Update(t.Context(), pg))
	return r, c, source, pg
}

func mustGetCompanionRecovery(t *testing.T, c client.Client, pg *v1.Postgres) *v1.PostgresRecovery {
	t.Helper()
	rec := &v1.PostgresRecovery{}
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(pg), rec))
	return rec
}

func TestDirectRecoveryPinsSource(t *testing.T) {
	r, c, _, pg := directRecoveryFixture(t)
	rec := mustGetCompanionRecovery(t, c, pg)
	pitr := rec.Spec.PointInTime
	p, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	if p.RecoverySource == nil || p.RecoverySource.BucketName != pitr.BucketName || p.RecoverySource.ServerName != pitr.ServerName || !p.RecoverySource.TargetTime.Equal(&pitr.TargetTime) {
		t.Fatalf("lost recovery provenance: %#v", p.RecoverySource)
	}
	related := relatedobjectsmap.NewRelatedObjectsMap(r.Scheme)
	actions, result, err := r.Update(pg, p, related)
	requireNoError(t, err)
	if result.RequeueAfter == 0 {
		t.Fatal("recovery must keep observing CNPG jobs")
	}
	for _, a := range actions {
		if _, ok := a.GetObject().(*cnpgv1.Cluster); ok {
			t.Fatal("started recovery before IAM and archive infrastructure were ready")
		}
	}
}

func TestDirectRecoveryCreatesOwnedClusterAfterInfrastructureIsReady(t *testing.T) {
	r, c, _, pg := directRecoveryFixture(t)
	rec := mustGetCompanionRecovery(t, c, pg)
	pitr := rec.Spec.PointInTime
	p, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	// The source archive readiness is independent of the destination's IAM.
	p.RecoverySourceReady = true
	related := relatedobjectsmap.NewRelatedObjectsMap(r.Scheme)
	initial, _, err := r.Update(pg, p, related)
	requireNoError(t, err)
	foundGroup := false
	for _, a := range initial {
		obj := a.GetObject()
		switch object := obj.(type) {
		case *v1.Postgres:
			continue
		case *iamcnrm.IAMServiceAccount:
			object.Status.ObservedGeneration = object.Generation
			object.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
		case *iamcnrm.IAMPolicyMember:
			object.Status.ObservedGeneration = object.Generation
			object.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
		case *storagecnrm.StorageBucket:
			object.Status.ObservedGeneration = new(object.Generation)
			object.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
		case *cnpgv1.DatabaseRole:
			if object.Name == rccnpg.ClusterNameFor(v1.PostgresPhysicalName(pg.Name))+"-personal-access" {
				foundGroup = true
				if object.Spec.Name != rccnpg.PersonalAccessRoleForPostgres(v1.PostgresPhysicalName(pg.Name), pg) {
					t.Fatal("personal access group role does not match the recovery cluster")
				}
			}
		}
		related.Insert(obj)
	}
	if !foundGroup {
		t.Fatal("no isolated authentication group planned")
	}
	actions, _, err := r.Update(pg, p, related)
	requireNoError(t, err)
	for _, a := range actions {
		cluster, ok := a.GetObject().(*cnpgv1.Cluster)
		if !ok {
			continue
		}
		if !physicalControlledBy(cluster, pg) || cluster.Spec.Bootstrap.Recovery == nil || cluster.Spec.Bootstrap.InitDB != nil {
			t.Fatal("recovery did not create a directly owned recovery cluster")
		}
		group := rccnpg.PersonalAccessRoleForPostgres(v1.PostgresPhysicalName(pg.Name), pg)
		if cluster.Annotations[rccnpg.PersonalAccessGroupAnnotation] != group {
			t.Fatal("recovery cluster did not publish its isolated access group")
		}
		if !slices.Contains(cluster.Spec.PostgresConfiguration.PgHBA, "hostssl all +"+group+" all scram-sha-256") {
			t.Fatal("recovery HBA does not require the isolated access group")
		}
		if got := cluster.Spec.Bootstrap.Recovery.RecoveryTarget.TargetTime; got != pitr.TargetTime.UTC().Format(time.RFC3339Nano) {
			t.Fatalf("changed explicit target time: %s", got)
		}
		if cluster.Spec.ExternalClusters[0].PluginConfiguration.Parameters["barmanObjectName"] != pitr.BucketName {
			t.Fatal("cluster lost the pinned recovery archive")
		}
		for _, plugin := range cluster.Spec.Plugins {
			if plugin.Name == rccnpg.BarmanPluginName && plugin.Parameters["barmanObjectName"] == pitr.BucketName {
				t.Fatal("recovery reused the source as its own writable WAL archive")
			}
		}
		return
	}
	t.Fatal("no recovery cluster planned after infrastructure became ready")
}

func TestDirectRecoveryRejectsChangedSource(t *testing.T) {
	for _, field := range []string{"UID", "archive", "server", "version", "extension", "future", "missing target", "self", "WAL disabled"} {
		t.Run(field, func(t *testing.T) {
			r, c, _, pg := directRecoveryFixture(t)
			rec := mustGetCompanionRecovery(t, c, pg)
			switch field {
			case "UID":
				rec.Spec.PointInTime.SourceUID = "replacement"
			case "archive":
				rec.Spec.PointInTime.BucketName = "another-archive"
			case "server":
				rec.Spec.PointInTime.ServerName = "another-server"
			case "version":
				pg.Spec.MajorVersion = "17"
			case "extension":
				source := &v1.Postgres{}
				requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: rec.Spec.PointInTime.SourcePostgres}, source))
				source.Spec.Extensions = []v1.PostgresExtension{{Name: "vector"}}
				requireNoError(t, c.Update(t.Context(), source))
			case "future":
				rec.Spec.PointInTime.TargetTime = metav1.NewTime(time.Now().Truncate(time.Second).Add(time.Hour))
			case "missing target":
				rec.Spec.PointInTime.TargetTime = metav1.Time{}
			case "self":
				rec.Spec.PointInTime.SourcePostgres = pg.Name
			case "WAL disabled":
				r.Config.CNPG.WalBucketPrefix = ""
			}
			requireNoError(t, c.Update(t.Context(), rec))
			if _, _, err := r.Prepare(t.Context(), c, pg); err == nil {
				t.Fatal("accepted invalid or changed recovery source")
			}
		})
	}
}

func recoveryClusterForPostgres(t *testing.T, r *PostgresClusterReconciler, c client.Client, pg *v1.Postgres) *cnpgv1.Cluster {
	t.Helper()
	rec := mustGetCompanionRecovery(t, c, pg)
	pitr := rec.Spec.PointInTime
	physical := pg.DeepCopy()
	physical.Name = v1.PostgresPhysicalName(pg.Name)
	cluster, err := rccnpg.CreateCluster(r.Scheme, physical, r.Config, rccnpg.WALArchive{BucketName: "target-archive"}, &rccnpg.RecoverySource{BucketName: pitr.BucketName, ServerName: pitr.ServerName, TargetTime: pitr.TargetTime}, nil)
	requireNoError(t, err)
	cluster.OwnerReferences = nil
	requireNoError(t, controllerutil.SetControllerReference(pg, cluster, r.Scheme))
	cluster.UID = "recovered-cluster-uid"
	cluster.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Minute))
	return cluster
}

func TestDirectRecoveryCheckpointsOwnBackupBeforeDetaching(t *testing.T) {
	r, c, source, pg := directRecoveryFixture(t)
	cluster := recoveryClusterForPostgres(t, r, c, pg)
	cluster.Status.Conditions = []metav1.Condition{
		{Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionTrue},
		{Type: "Initialized", Status: metav1.ConditionTrue},
		{Type: "LastBackupSucceeded", Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now())},
	}
	requireNoError(t, c.Create(t.Context(), cluster))
	p, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	if !recoveryFinished(pg, p.Cluster) {
		t.Fatalf("successful own backup was not checkpointed: %#v", pg.Status)
	}
	related := relatedobjectsmap.NewRelatedObjectsMap(r.Scheme)
	for _, child := range p.Children {
		related.Insert(child)
	}
	actions, _, err := r.Update(pg, p, related)
	requireNoError(t, err)
	var detached *cnpgv1.Cluster
	for _, a := range actions {
		if desired, ok := a.GetObject().(*cnpgv1.Cluster); ok {
			detached = desired
		}
	}
	if detached == nil || detached.Spec.Bootstrap.Recovery != nil || len(detached.Spec.ExternalClusters) != 0 {
		t.Fatalf("recovery inputs were not detached: %#v", detached)
	}
	// A completed restore no longer needs a source lookup, even during an outage.
	requireNoError(t, c.Delete(t.Context(), source))
	requireNoError(t, r.prepareDirectRecovery(t.Context(), c, pg, &p))
}

func TestSourceDeletionWaitsForRecoveryDetach(t *testing.T) {
	r, c, source, pg := directRecoveryFixture(t)
	blocking, err := blockingPostgresRecoveries(t.Context(), c, source)
	requireNoError(t, err)
	if fmt.Sprint(blocking) != "[restored]" {
		t.Fatalf("pending recovery did not block deletion: %v", blocking)
	}
	cluster := recoveryClusterForPostgres(t, r, c, pg)
	requireNoError(t, c.Create(t.Context(), cluster))
	pg.GetStatus().SetCondition(metav1.Condition{Type: recoveryCompletedCondition, Status: metav1.ConditionTrue, Reason: "Completed", Message: string(cluster.UID)})
	requireNoError(t, c.Status().Update(t.Context(), pg))
	blocking, err = blockingPostgresRecoveries(t.Context(), c, source)
	requireNoError(t, err)
	if len(blocking) == 0 {
		t.Fatal("checkpoint alone released archive while CNPG still references it")
	}
	cluster.Spec.Bootstrap.Recovery = nil
	cluster.Spec.Bootstrap.InitDB = &cnpgv1.BootstrapInitDB{}
	cluster.Spec.ExternalClusters = nil
	requireNoError(t, c.Update(t.Context(), cluster))
	blocking, err = blockingPostgresRecoveries(t.Context(), c, source)
	requireNoError(t, err)
	if len(blocking) != 0 {
		t.Fatalf("independent recovery still blocks source deletion: %v", blocking)
	}
}

func TestDirectRecoveryFailureIsTerminalAndDeletionNeedsNoSource(t *testing.T) {
	r, c, _, pg := directRecoveryFixture(t)
	cluster := recoveryClusterForPostgres(t, r, c, pg)
	cluster.Status.Phase = cnpgv1.PhaseUnrecoverable
	cluster.Status.PhaseReason = "failed PITR recovery"
	requireNoError(t, c.Create(t.Context(), cluster))
	requireNoError(t, c.Create(t.Context(), failedRecoveryJob(t, r.Scheme, cluster)))
	p, _, err := r.Prepare(t.Context(), c, pg)
	if err == nil || !strings.Contains(err.Error(), "failed PITR recovery") {
		t.Fatalf("failed job did not become a terminal recovery failure: %v", err)
	}
	if _, _, err := r.Update(pg, p, relatedobjectsmap.NewRelatedObjectsMap(r.Scheme)); err == nil {
		t.Fatal("planned changes after terminal failure")
	}
	// Deletion takes the inventory-only path and must not validate recovery inputs.
	pg.DeletionTimestamp = new(metav1.Now())
	rec := mustGetCompanionRecovery(t, c, pg)
	rec.Spec.PointInTime.SourcePostgres = "deleted-source"
	requireNoError(t, c.Update(t.Context(), rec))
	if _, _, err := r.Prepare(t.Context(), c, pg); err != nil {
		t.Fatalf("failed recovery could not be deleted: %v", err)
	}
}

func TestPostgresRecoverySpecIsImmutableAndRequiresExactlyOneSource(t *testing.T) {
	pitr := &v1.PostgresPointInTimeRecovery{
		SourcePostgres: "source",
		SourceUID:      "source-uid",
		BucketName:     "source-archive",
		ServerName:     "source-cluster",
		TargetTime:     metav1.NewTime(time.Now().Truncate(time.Second).Add(-time.Hour)),
	}
	backup := &v1.PostgresBackupRecovery{
		BackupName: "backup-1",
		BackupUID:  "backup-uid",
		BucketName: "dr-bucket",
		Prefix:     "backup-1",
		ServerName: "source-cluster",
		BackupID:   "20260417T020000",
	}
	if err := k8sClient.Create(t.Context(), &v1.PostgresRecovery{
		ObjectMeta: metav1.ObjectMeta{Name: "recovery-neither", Namespace: "default"},
		Spec:       v1.PostgresRecoverySpec{Postgres: "recovery-neither"},
	}); err == nil {
		t.Fatal("accepted PostgresRecovery without pointInTime or backup")
	}
	if err := k8sClient.Create(t.Context(), &v1.PostgresRecovery{
		ObjectMeta: metav1.ObjectMeta{Name: "recovery-both", Namespace: "default"},
		Spec:       v1.PostgresRecoverySpec{Postgres: "recovery-both", PointInTime: pitr, Backup: backup},
	}); err == nil {
		t.Fatal("accepted PostgresRecovery with both pointInTime and backup")
	}
	rec := &v1.PostgresRecovery{
		ObjectMeta: metav1.ObjectMeta{Name: "recovery-immutable", Namespace: "default"},
		Spec:       v1.PostgresRecoverySpec{Postgres: "recovery-immutable", PointInTime: pitr},
	}
	requireNoError(t, k8sClient.Create(t.Context(), rec))
	rec.Spec.PointInTime.BucketName = "another-archive"
	if err := k8sClient.Update(t.Context(), rec); err == nil {
		t.Fatal("accepted mutation of immutable PostgresRecovery.spec")
	}
}

func TestDirectRecoveryRefusesRebootstrapAfterClusterLoss(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		t.Run(fmt.Sprintf("replaced=%t", replaced), func(t *testing.T) {
			r, c, _, pg := directRecoveryFixture(t)
			cluster := recoveryClusterForPostgres(t, r, c, pg)
			requireNoError(t, c.Create(t.Context(), cluster))
			_, _, err := r.Prepare(t.Context(), c, pg)
			requireNoError(t, err)
			requireNoError(t, c.Status().Update(t.Context(), pg))
			requireNoError(t, c.Delete(t.Context(), cluster))
			if replaced {
				cluster.UID = "replacement-cluster"
				cluster.ResourceVersion = ""
				requireNoError(t, c.Create(t.Context(), cluster))
			}
			if _, _, err := r.Prepare(t.Context(), c, pg); err == nil {
				t.Fatal("accepted lost or replaced recovery cluster")
			}
		})
	}
}

func TestRecoveryGrantAndCompanionRecoveryAreCleanedUpOnlyAfterActualDetach(t *testing.T) {
	r, c, _, pg := directRecoveryFixture(t)
	cluster := recoveryClusterForPostgres(t, r, c, pg)
	requireNoError(t, c.Create(t.Context(), cluster))
	pg.GetStatus().SetCondition(metav1.Condition{Type: recoveryCompletedCondition, Status: metav1.ConditionTrue, Reason: "Completed", Message: string(cluster.UID)})
	grant := &iamcnrm.IAMPolicyMember{ObjectMeta: metav1.ObjectMeta{Name: recoverySourcePolicyNameFor(v1.PostgresPhysicalName(pg.Name)), Namespace: pg.Namespace, UID: "temporary-grant"}}
	requireNoError(t, controllerutil.SetControllerReference(pg, grant, r.Scheme))
	requireNoError(t, c.Create(t.Context(), grant))
	for _, detached := range []bool{false, true} {
		t.Run(fmt.Sprintf("detached=%t", detached), func(t *testing.T) {
			if detached {
				cluster.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{InitDB: &cnpgv1.BootstrapInitDB{}}
				cluster.Spec.ExternalClusters = nil
				requireNoError(t, c.Update(t.Context(), cluster))
			}
			p, _, err := r.Prepare(t.Context(), c, pg)
			requireNoError(t, err)
			for _, ref := range pg.Status.Ownership.Resources {
				if ref.Name == grant.Name {
					t.Fatal("temporary grant was included in durable resource inventory")
				}
			}
			related := relatedobjectsmap.NewRelatedObjectsMap(r.Scheme)
			for _, child := range p.Children {
				related.Insert(child)
			}
			actions, _, err := r.Update(pg, p, related)
			requireNoError(t, err)
			foundGrantDelete := false
			foundRecoveryDelete := false
			for _, a := range actions {
				if a.GetObject().GetName() == grant.Name {
					foundGrantDelete = true
					if !detached {
						t.Fatal("revoked source grant before CNPG detached")
					}
				}
				if _, ok := a.GetObject().(*v1.PostgresRecovery); ok {
					foundRecoveryDelete = true
					if !detached {
						t.Fatal("deleted companion PostgresRecovery before CNPG detached")
					}
				}
			}
			if foundGrantDelete != detached {
				t.Fatal("missing cleanup of temporary source grant")
			}
			if foundRecoveryDelete != detached {
				t.Fatal("missing cleanup of companion PostgresRecovery")
			}
		})
	}
}

func TestSourceDeletionAllowsAcceptedRecoveryToFinish(t *testing.T) {
	r, c, source, pg := directRecoveryFixture(t)
	pg.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Minute))
	requireNoError(t, c.Delete(t.Context(), source))
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(source), source))
	if source.DeletionTimestamp.IsZero() {
		t.Fatal("source deletion must be held by its finalizer")
	}
	p, _, err := r.Prepare(t.Context(), c, source)
	requireNoError(t, err)
	actions, result, err := r.Delete(source, p, nil)
	requireNoError(t, err)
	if len(actions) != 0 || result.RequeueAfter == 0 {
		t.Fatal("source archive cleanup must wait for the recovery")
	}
	// This dependent was admitted before source deletion. It can still prepare
	// its infrastructure instead of deadlocking against the source finalizer.
	_, _, err = r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	late := pg.DeepCopy()
	late.Name, late.UID, late.ResourceVersion, late.Status = "late-restored", "late-recovery-uid", "", nil
	late.Annotations = map[string]string{v1.PostgresRecoveryAnnotation: late.Name}
	late.CreationTimestamp = *source.DeletionTimestamp
	requireNoError(t, c.Create(t.Context(), late))
	rec := mustGetCompanionRecovery(t, c, pg).DeepCopy()
	rec.Name, rec.UID, rec.ResourceVersion, rec.OwnerReferences = late.Name, "late-rec-uid", "", nil
	rec.Spec.Postgres = late.Name
	requireNoError(t, c.Create(t.Context(), rec))
	if _, _, err := r.Prepare(t.Context(), c, late); err == nil {
		t.Fatal("accepted a new recovery after source deletion started")
	}
	blocking, err := blockingPostgresRecoveries(t.Context(), c, source)
	requireNoError(t, err)
	if !slices.Equal(blocking, []string{pg.Name}) {
		t.Fatalf("only the admitted recovery may block source deletion: %v", blocking)
	}
}

func TestOwnBackupProvesRecoveryCompletedDespiteLaterOutage(t *testing.T) {
	r, c, _, pg := directRecoveryFixture(t)
	cluster := recoveryClusterForPostgres(t, r, c, pg)
	cluster.Status.Phase = cnpgv1.PhaseUnrecoverable
	cluster.Status.Conditions = []metav1.Condition{
		{Type: "Initialized", Status: metav1.ConditionTrue},
		{Type: "LastBackupSucceeded", Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now())},
	}
	requireNoError(t, c.Create(t.Context(), cluster))
	requireNoError(t, c.Create(t.Context(), failedRecoveryJob(t, r.Scheme, cluster)))
	p, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	if !recoveryFinished(pg, p.Cluster) {
		t.Fatal("missed successful recovery was classified as a new bootstrap failure")
	}
}

func TestRecoveredDatabasePersonalAccessUsesNewAuthenticationGroup(t *testing.T) {
	access, pg, cluster := accessFixture()
	adoptAccessFixture(t, pg, cluster)
	access.Spec.Postgres, access.Spec.PostgresBranch = pg.Name, ""
	access.Spec.AccessLevel = v1.PostgresAccessLevelRead
	r := makePostgresAccessReconciler()
	access.Finalizers = []string{r.Name()}
	pg.GetStatus().SetCondition(metav1.Condition{Type: recoveryCompletedCondition, Status: metav1.ConditionTrue, Reason: "Completed", Message: string(cluster.UID)})
	group := rccnpg.PersonalAccessRoleForPostgres(v1.PostgresPhysicalName(pg.Name), pg)
	metav1.SetMetaDataAnnotation(&cluster.ObjectMeta, rccnpg.PersonalAccessGroupAnnotation, group)
	c := fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(pg, cluster).Build()
	prepared, _, err := r.Prepare(t.Context(), c, access)
	requireNoError(t, err)
	actions, _, err := r.Update(access, prepared, relatedobjectsmap.NewRelatedObjectsMap(r.Scheme))
	requireNoError(t, err)
	for _, a := range actions {
		if role, ok := a.GetObject().(*cnpgv1.DatabaseRole); ok {
			if !slices.Equal(role.Spec.InRoles, []string{rccnpg.ReadRole, group}) {
				t.Fatalf("personal access did not use the isolated group: %v", role.Spec.InRoles)
			}
			return
		}
	}
	t.Fatal("no personal DatabaseRole planned")
}

func failedRecoveryJob(t *testing.T, scheme *runtime.Scheme, cluster *cnpgv1.Cluster) *batchv1.Job {
	t.Helper()
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name: cluster.Name + "-1-full-recovery", Namespace: cluster.Namespace,
			Labels: map[string]string{
				recoveryJobRoleLabel: recoveryJobRole, recoveryJobClusterLabel: cluster.Name,
			},
		},
		Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{
			Type: batchv1.JobFailed, Status: corev1.ConditionTrue,
			Reason: "BackoffLimitExceeded",
		}}},
	}
	requireNoError(t, controllerutil.SetControllerReference(cluster, job, scheme))
	return job
}

func backupRecoveryFixture(t *testing.T) (*PostgresClusterReconciler, *PostgresBackupReconciler, client.Client, *v1.PostgresBackup, *v1.Postgres) {
	t.Helper()
	r, c, source, _, _ := directOwnershipFixture(t, true)
	r.Config.CNPG.DrBucketPrefix = "nais-dr-dev"
	backupReconciler := &PostgresBackupReconciler{Config: r.Config, Recorder: r.Recorder, Scheme: r.Scheme}

	drBucket := &storagecnrm.StorageBucket{
		ObjectMeta: metav1.ObjectMeta{Name: "nais-dr-dev-team", Namespace: source.Namespace, Generation: 1},
		Status: storagecnrm.StorageBucketStatus{
			ObservedGeneration: new(int64(1)),
			Conditions:         []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}},
		},
	}
	requireNoError(t, c.Create(t.Context(), drBucket))

	disk := resource.MustParse("10Gi")
	completedAt := metav1.NewTime(time.Now().UTC().Truncate(time.Second).Add(-2 * time.Hour))
	expiresAt := metav1.NewTime(completedAt.Add(30 * 24 * time.Hour))
	backup := &v1.PostgresBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "orders-pre-drop",
			Namespace:  source.Namespace,
			UID:        "backup-uid-1",
			Finalizers: []string{"postgresbackup.nais.io"},
		},
		Spec: v1.PostgresBackupSpec{Postgres: source.Name},
		Status: &v1.PostgresBackupStatus{
			Phase:        v1.PostgresBackupPhaseCompleted,
			SourceUID:    string(source.UID),
			MajorVersion: source.Spec.MajorVersion,
			Extensions:   []v1.PostgresExtension{{Name: "pg_trgm"}},
			DiskSize:     &disk,
			BackupID:     "20260417T020000",
			ServerName:   source.Status.ClusterName,
			CompletedAt:  &completedAt,
			ExpiresAt:    &expiresAt,
			Location: &v1.PostgresBackupLocation{
				BucketName: drBucket.Name,
				Prefix:     "orders-pre-drop",
			},
		},
	}
	requireNoError(t, c.Create(t.Context(), backup))

	// Delete the original source Postgres and its WAL bucket so restore proves
	// standalone backups work without the original database or WAL bucket.
	sourceCluster := &cnpgv1.Cluster{}
	requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: source.Namespace, Name: source.Status.ClusterName}, sourceCluster))
	for _, plugin := range sourceCluster.Spec.Plugins {
		if plugin.Name == rccnpg.BarmanPluginName {
			walBucket := &storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: plugin.Parameters["barmanObjectName"], Namespace: source.Namespace}}
			_ = c.Delete(t.Context(), walBucket)
		}
	}
	requireNoError(t, c.Delete(t.Context(), sourceCluster))
	source.Finalizers = nil
	requireNoError(t, c.Update(t.Context(), source))
	requireNoError(t, c.Delete(t.Context(), source))

	pg := &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "restored-from-backup",
			Namespace:   source.Namespace,
			UID:         "restored-backup-pg-uid",
			Annotations: map[string]string{v1.PostgresRecoveryAnnotation: "restored-from-backup"},
		},
		Spec: v1.PostgresSpec{
			MajorVersion: "18",
			Extensions:   []v1.PostgresExtension{{Name: "pg_trgm"}},
			Resources: v1.PostgresResources{
				DiskSize: resource.MustParse("10Gi"),
				Cpu:      resource.MustParse("100m"),
				Memory:   resource.MustParse("512Mi"),
			},
		},
	}
	requireNoError(t, c.Create(t.Context(), pg))

	rec := &v1.PostgresRecovery{
		ObjectMeta: metav1.ObjectMeta{Name: pg.Name, Namespace: pg.Namespace, UID: "backup-rec-uid"},
		Spec: v1.PostgresRecoverySpec{
			Postgres: pg.Name,
			Backup: &v1.PostgresBackupRecovery{
				SourcePostgres: source.Name,
				BackupName:     backup.Name,
				BackupUID:      string(backup.UID),
				BucketName:     drBucket.Name,
				Prefix:         "orders-pre-drop",
				ServerName:     backup.Status.ServerName,
				BackupID:       backup.Status.BackupID,
			},
		},
	}
	requireNoError(t, controllerutil.SetOwnerReference(pg, rec, r.Scheme))
	requireNoError(t, c.Create(t.Context(), rec))

	owners := ownership.NewOwnerManager(r.Name() + "/owner")
	requireNoError(t, (&protectPostgres{postgres: pg}).Do(t.Context(), c, r.Scheme, owners))
	requireNoError(t, newAcceptPostgresRecovery(pg, backup, backup.UID, "postgresbackup.nais.io").Do(t.Context(), c, r.Scheme, owners))
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(backup), backup))

	_, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	requireNoError(t, c.Status().Update(t.Context(), pg))
	return r, backupReconciler, c, backup, pg
}

func TestRestoreFromStandaloneBackupAfterSourceAndWALBucketDeletion(t *testing.T) {
	r, backupReconciler, c, backup, pg := backupRecoveryFixture(t)
	p, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	if !p.RecoverySourceReady {
		t.Fatal("expected DR bucket recovery source to be ready")
	}

	related := relatedobjectsmap.NewRelatedObjectsMap(r.Scheme)
	initial, _, err := r.Update(pg, p, related)
	requireNoError(t, err)

	storeName := recoveryObjectStoreNameFor(v1.PostgresPhysicalName(pg.Name))
	var tempStore *barmanv1.ObjectStore
	for _, a := range initial {
		obj := a.GetObject()
		switch object := obj.(type) {
		case *v1.Postgres:
			continue
		case *iamcnrm.IAMServiceAccount:
			object.Status.ObservedGeneration = object.Generation
			object.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
		case *iamcnrm.IAMPolicyMember:
			object.Status.ObservedGeneration = object.Generation
			object.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
		case *storagecnrm.StorageBucket:
			object.Status.ObservedGeneration = new(object.Generation)
			object.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
		case *barmanv1.ObjectStore:
			if object.Name == storeName {
				tempStore = object
			}
		}
		related.Insert(obj)
	}
	if tempStore == nil {
		t.Fatalf("expected temporary recovery ObjectStore %q to be planned", storeName)
	}
	if got, want := tempStore.Spec.Configuration.DestinationPath, "gs://nais-dr-dev-team/orders-pre-drop"; got != want {
		t.Fatalf("temporary ObjectStore destinationPath = %q, want %q", got, want)
	}
	if tempStore.Spec.RetentionPolicy != "" {
		t.Fatalf("temporary recovery ObjectStore must not set retentionPolicy, got %q", tempStore.Spec.RetentionPolicy)
	}

	actions, _, err := r.Update(pg, p, related)
	requireNoError(t, err)
	var cluster *cnpgv1.Cluster
	for _, a := range actions {
		if cObj, ok := a.GetObject().(*cnpgv1.Cluster); ok {
			cluster = cObj
		}
	}
	if cluster == nil {
		t.Fatal("expected recovery Cluster to be planned once infrastructure is ready")
	}
	if cluster.Spec.Bootstrap == nil || cluster.Spec.Bootstrap.Recovery == nil {
		t.Fatal("expected bootstrap.recovery on Cluster")
	}
	rt := cluster.Spec.Bootstrap.Recovery.RecoveryTarget
	if rt == nil || rt.BackupID != "20260417T020000" || rt.TargetImmediate == nil || !*rt.TargetImmediate || rt.TargetTime != "" {
		t.Fatalf("unexpected recoveryTarget: %#v", rt)
	}
	if got := cluster.Spec.ExternalClusters[0].PluginConfiguration.Parameters["barmanObjectName"]; got != storeName {
		t.Fatalf("externalCluster barmanObjectName = %q, want %q", got, storeName)
	}

	assertBackupProtectedWhileRecoveryActive(t, backupReconciler, c, backup, pg.Name)

	// Once the recovered database has its own backup and detaches, temporary
	// ObjectStore, IAM grant, and PostgresRecovery are cleaned up, and the
	// backup admission is released.
	assertBackupRecoveryDetachCleansUp(t, r, backupReconciler, c, backup, pg, cluster, tempStore)
}

func assertBackupProtectedWhileRecoveryActive(t *testing.T, backupReconciler *PostgresBackupReconciler, c client.Client, backup *v1.PostgresBackup, targetName string) {
	t.Helper()
	pastCompleted := metav1.NewTime(time.Now().Add(-31 * 24 * time.Hour))
	backup.Status.CompletedAt = &pastCompleted
	expired := metav1.NewTime(time.Now().Add(-time.Minute))
	backup.Status.ExpiresAt = &expired
	requireNoError(t, c.Status().Update(t.Context(), backup))
	bPrep, _, err := backupReconciler.Prepare(t.Context(), c, backup)
	requireNoError(t, err)
	if !slices.Equal(bPrep.BlockingRecoveries, []string{targetName}) {
		t.Fatalf("expected BlockingRecoveries = [%s], got %v", targetName, bPrep.BlockingRecoveries)
	}
	bActions, bResult, err := backupReconciler.Update(backup, bPrep, nil)
	requireNoError(t, err)
	if len(bActions) != 0 || bResult.RequeueAfter == 0 {
		t.Fatalf("expired backup with active recovery must defer deletion: actions=%v result=%v", bActions, bResult)
	}

	requireNoError(t, c.Delete(t.Context(), backup))
	requireNoError(t, c.Get(t.Context(), client.ObjectKeyFromObject(backup), backup))
	bPrep, _, err = backupReconciler.Prepare(t.Context(), c, backup)
	requireNoError(t, err)
	bActions, bResult, err = backupReconciler.Delete(backup, bPrep, nil)
	requireNoError(t, err)
	if len(bActions) != 0 || bResult.RequeueAfter == 0 {
		t.Fatalf("deleting backup with active recovery must wait for detach: actions=%v result=%v", bActions, bResult)
	}
}

func assertBackupRecoveryDetachCleansUp(
	t *testing.T,
	r *PostgresClusterReconciler,
	backupReconciler *PostgresBackupReconciler,
	c client.Client,
	backup *v1.PostgresBackup,
	pg *v1.Postgres,
	cluster *cnpgv1.Cluster,
	tempStore *barmanv1.ObjectStore,
) {
	t.Helper()
	cluster.UID = "recovered-from-backup-cluster-uid"
	cluster.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Minute))
	cluster.Status.Conditions = []metav1.Condition{
		{Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionTrue},
		{Type: "Initialized", Status: metav1.ConditionTrue},
		{Type: "LastBackupSucceeded", Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now())},
	}
	requireNoError(t, c.Create(t.Context(), cluster))
	tempStore.UID = "temp-store-uid"
	requireNoError(t, c.Create(t.Context(), tempStore))
	grant := &iamcnrm.IAMPolicyMember{ObjectMeta: metav1.ObjectMeta{Name: recoverySourcePolicyNameFor(v1.PostgresPhysicalName(pg.Name)), Namespace: pg.Namespace, UID: "temp-grant-uid"}}
	requireNoError(t, controllerutil.SetControllerReference(pg, grant, r.Scheme))
	requireNoError(t, c.Create(t.Context(), grant))

	p, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	for _, ref := range pg.Status.Ownership.Resources {
		if ref.Name == tempStore.Name || ref.Name == grant.Name {
			t.Fatalf("temporary recovery resource leaked into durable inventory: %#v", ref)
		}
	}
	cluster.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{InitDB: &cnpgv1.BootstrapInitDB{}}
	cluster.Spec.ExternalClusters = nil
	requireNoError(t, c.Update(t.Context(), cluster))

	p, _, err = r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	relatedAfter := relatedobjectsmap.NewRelatedObjectsMap(r.Scheme)
	for _, child := range p.Children {
		relatedAfter.Insert(child)
	}
	cleanupActions, _, err := r.Update(pg, p, relatedAfter)
	requireNoError(t, err)
	var deletedStore, deletedGrant, deletedRecovery bool
	for _, a := range cleanupActions {
		switch a.GetObject().GetName() {
		case tempStore.Name:
			deletedStore = true
		case grant.Name:
			deletedGrant = true
		case pg.Name:
			if _, ok := a.GetObject().(*v1.PostgresRecovery); ok {
				deletedRecovery = true
			}
		}
	}
	if !deletedStore || !deletedGrant || !deletedRecovery {
		t.Fatalf("expected cleanup of tempStore=%t, grant=%t, companionRecovery=%t", deletedStore, deletedGrant, deletedRecovery)
	}
	if pg.Annotations[v1.PostgresRecoveryAnnotation] != "" {
		t.Fatal("expected PostgresRecoveryAnnotation to be cleared after detach")
	}

	requireNoError(t, c.Status().Update(t.Context(), pg))
	bPrep, err := backupReconcilerPrepare(t, backupReconciler, c, backup)
	requireNoError(t, err)
	if len(bPrep.BlockingRecoveries) != 0 {
		t.Fatalf("detached recovery must not block backup cleanup: %v", bPrep.BlockingRecoveries)
	}
}

func backupReconcilerPrepare(t *testing.T, backupReconciler *PostgresBackupReconciler, c client.Client, backup *v1.PostgresBackup) (PostgresBackupPreparedData, error) {
	t.Helper()
	bPrep, _, err := backupReconciler.Prepare(t.Context(), c, backup)
	return bPrep, err
}

func TestRemovingLegacyInlineSpecRecoveryDoesNotChangeUnderlyingResources(t *testing.T) {
	r, c, _, pg := directRecoveryFixture(t)
	cluster := recoveryClusterForPostgres(t, r, c, pg)
	cluster.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{InitDB: &cnpgv1.BootstrapInitDB{}}
	cluster.Spec.ExternalClusters = nil
	cluster.Status.Conditions = []metav1.Condition{
		{Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionTrue},
		{Type: "Initialized", Status: metav1.ConditionTrue},
		{Type: "LastBackupSucceeded", Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now())},
	}
	requireNoError(t, c.Create(t.Context(), cluster))
	pg.GetStatus().SetCondition(metav1.Condition{Type: recoveryCompletedCondition, Status: metav1.ConditionTrue, Reason: "Completed", Message: string(cluster.UID)})
	requireNoError(t, c.Status().Update(t.Context(), pg))

	// Remove both the companion PostgresRecovery, the annotation, and any inline Spec.Recovery.
	rec := mustGetCompanionRecovery(t, c, pg)
	requireNoError(t, c.Delete(t.Context(), rec))
	pg.Annotations = nil
	pg.Spec.Recovery = nil
	requireNoError(t, c.Update(t.Context(), pg))

	p, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	related := relatedobjectsmap.NewRelatedObjectsMap(r.Scheme)
	for _, child := range p.Children {
		related.Insert(child)
	}
	actions, _, err := r.Update(pg, p, related)
	requireNoError(t, err)

	wantGroup := rccnpg.PersonalAccessRole(v1.PostgresPhysicalName(pg.Name) + "/" + string(pg.UID))
	for _, a := range actions {
		switch obj := a.GetObject().(type) {
		case *cnpgv1.DatabaseRole:
			if obj.Name == rccnpg.ClusterNameFor(v1.PostgresPhysicalName(pg.Name))+"-personal-access" && obj.Spec.Name != wantGroup {
				t.Fatalf("DatabaseRole group changed to %q, want %q", obj.Spec.Name, wantGroup)
			}
		case *cnpgv1.Cluster:
			if obj.Annotations[rccnpg.PersonalAccessGroupAnnotation] != wantGroup {
				t.Fatalf("Cluster annotation changed to %q, want %q", obj.Annotations[rccnpg.PersonalAccessGroupAnnotation], wantGroup)
			}
			if !slices.Contains(obj.Spec.PostgresConfiguration.PgHBA, "hostssl all +"+wantGroup+" all scram-sha-256") {
				t.Fatalf("Cluster pg_hba lost isolated group %q: %v", wantGroup, obj.Spec.PostgresConfiguration.PgHBA)
			}
		}
	}
	_ = config.Config{}
}
