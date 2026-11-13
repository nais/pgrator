package controller

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func directRecoveryFixture(t *testing.T) (*PostgresClusterReconciler, client.Client, *v1.Postgres, *v1.Postgres) {
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
	pg := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "restored", Namespace: source.Namespace, UID: "restored-uid"}, Spec: *source.Spec.DeepCopy()}
	pg.Spec.Recovery = &v1.PostgresRecovery{SourcePostgres: source.Name, SourceUID: string(source.UID), BucketName: bucket, ServerName: source.Status.ClusterName, TargetTime: metav1.NewTime(time.Now().UTC().Truncate(time.Second).Add(-time.Hour))}
	requireNoError(t, c.Create(t.Context(), pg))
	_, _, err := r.Prepare(t.Context(), withoutBranchAPI(t, c), pg)
	requireNoError(t, err)
	requireNoError(t, c.Status().Update(t.Context(), pg))
	return r, withoutBranchAPI(t, c), source, pg
}

func TestDirectRecoveryPinsSourceWithoutBranchAPI(t *testing.T) {
	r, c, _, pg := directRecoveryFixture(t)
	p, _, err := r.Prepare(t.Context(), c, pg)
	requireNoError(t, err)
	if p.RecoverySource == nil || p.RecoverySource.BucketName != pg.Spec.Recovery.BucketName || p.RecoverySource.ServerName != pg.Spec.Recovery.ServerName || !p.RecoverySource.TargetTime.Equal(&pg.Spec.Recovery.TargetTime) {
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
		if _, ok := a.GetObject().(*v1.PostgresBranch); ok {
			t.Fatal("created a retired branch object")
		}
	}
}

func TestDirectRecoveryCreatesOwnedClusterAfterInfrastructureIsReady(t *testing.T) {
	r, c, _, pg := directRecoveryFixture(t)
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
		if got := cluster.Spec.Bootstrap.Recovery.RecoveryTarget.TargetTime; got != pg.Spec.Recovery.TargetTime.UTC().Format(time.RFC3339Nano) {
			t.Fatalf("changed explicit target time: %s", got)
		}
		if cluster.Spec.ExternalClusters[0].PluginConfiguration.Parameters["barmanObjectName"] != pg.Spec.Recovery.BucketName {
			t.Fatal("cluster lost the pinned recovery archive")
		}
		for _, plugin := range cluster.Spec.Plugins {
			if plugin.Name == rccnpg.BarmanPluginName && plugin.Parameters["barmanObjectName"] == pg.Spec.Recovery.BucketName {
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
			switch field {
			case "UID":
				pg.Spec.Recovery.SourceUID = "replacement"
			case "archive":
				pg.Spec.Recovery.BucketName = "another-archive"
			case "server":
				pg.Spec.Recovery.ServerName = "another-server"
			case "version":
				pg.Spec.MajorVersion = "17"
			case "extension":
				source := &v1.Postgres{}
				requireNoError(t, c.Get(t.Context(), client.ObjectKey{Namespace: pg.Namespace, Name: pg.Spec.Recovery.SourcePostgres}, source))
				source.Spec.Extensions = []v1.PostgresExtension{{Name: "vector"}}
				requireNoError(t, c.Update(t.Context(), source))
			case "future":
				pg.Spec.Recovery.TargetTime = metav1.NewTime(time.Now().Add(time.Hour))
			case "missing target":
				pg.Spec.Recovery.TargetTime = metav1.Time{}
			case "self":
				pg.Spec.Recovery.SourcePostgres = pg.Name
			case "WAL disabled":
				r.Config.CNPG.WalBucketPrefix = ""
			}
			if _, _, err := r.Prepare(t.Context(), c, pg); err == nil {
				t.Fatal("accepted invalid or changed recovery source")
			}
		})
	}
}

func recoveryClusterForPostgres(t *testing.T, r *PostgresClusterReconciler, pg *v1.Postgres) *cnpgv1.Cluster {
	t.Helper()
	recovery := pg.Spec.Recovery
	physical := pg.DeepCopy()
	physical.Name = v1.PostgresPhysicalName(pg.Name)
	cluster, err := rccnpg.CreateCluster(r.Scheme, physical, r.Config, rccnpg.WALArchive{BucketName: "target-archive"}, &rccnpg.RecoverySource{BucketName: recovery.BucketName, ServerName: recovery.ServerName, TargetTime: recovery.TargetTime}, nil)
	requireNoError(t, err)
	cluster.OwnerReferences = nil
	requireNoError(t, controllerutil.SetControllerReference(pg, cluster, r.Scheme))
	cluster.UID = "recovered-cluster-uid"
	cluster.CreationTimestamp = metav1.NewTime(time.Now().Add(-time.Minute))
	return cluster
}

func TestDirectRecoveryCheckpointsOwnBackupBeforeDetaching(t *testing.T) {
	r, c, source, pg := directRecoveryFixture(t)
	cluster := recoveryClusterForPostgres(t, r, pg)
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
	cluster := recoveryClusterForPostgres(t, r, pg)
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
	cluster := recoveryClusterForPostgres(t, r, pg)
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
	pg.Spec.Recovery.SourcePostgres = "deleted-source"
	if _, _, err := r.Prepare(t.Context(), c, pg); err != nil {
		t.Fatalf("failed recovery could not be deleted: %v", err)
	}
}

func TestPostgresRecoveryCannotBeAddedChangedOrRemoved(t *testing.T) {
	for _, operation := range []string{"add", "change", "remove"} {
		t.Run(operation, func(t *testing.T) {
			pg := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "immutable-recovery-" + operation, Namespace: "default"}, Spec: v1.PostgresSpec{MajorVersion: "18"}}
			recovery := &v1.PostgresRecovery{SourcePostgres: "source", SourceUID: "source-uid", BucketName: "source-archive", ServerName: "source-cluster", TargetTime: metav1.NewTime(time.Now().Truncate(time.Second).Add(-time.Hour))}
			if operation != "add" {
				pg.Spec.Recovery = recovery.DeepCopy()
			}
			requireNoError(t, k8sClient.Create(t.Context(), pg))
			switch operation {
			case "add":
				pg.Spec.Recovery = recovery
			case "change":
				pg.Spec.Recovery.BucketName = "another-archive"
			case "remove":
				pg.Spec.Recovery = nil
			}
			if err := k8sClient.Update(t.Context(), pg); err == nil {
				t.Fatal("accepted mutation of immutable recovery provenance")
			}
		})
	}
}

func TestDirectRecoveryRefusesRebootstrapAfterClusterLoss(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		t.Run(fmt.Sprintf("replaced=%t", replaced), func(t *testing.T) {
			r, c, _, pg := directRecoveryFixture(t)
			cluster := recoveryClusterForPostgres(t, r, pg)
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

func TestRecoveryGrantIsRevokedOnlyAfterActualDetach(t *testing.T) {
	r, c, _, pg := directRecoveryFixture(t)
	cluster := recoveryClusterForPostgres(t, r, pg)
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
			found := false
			for _, a := range actions {
				if a.GetObject().GetName() == grant.Name {
					found = true
					if !detached {
						t.Fatal("revoked source grant before CNPG detached")
					}
				}
			}
			if found != detached {
				t.Fatal("missing cleanup of temporary source grant")
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
	pg.CreationTimestamp = metav1.NewTime(source.DeletionTimestamp.Add(time.Second))
	if _, _, err := r.Prepare(t.Context(), c, pg); err == nil {
		t.Fatal("accepted a new recovery after source deletion started")
	}
	requireNoError(t, c.Update(t.Context(), pg))
	blocking, err := blockingPostgresRecoveries(t.Context(), c, source)
	requireNoError(t, err)
	if len(blocking) != 0 {
		t.Fatal("ineligible late recovery held source deletion hostage")
	}
}

func TestOwnBackupProvesRecoveryCompletedDespiteLaterOutage(t *testing.T) {
	r, c, _, pg := directRecoveryFixture(t)
	cluster := recoveryClusterForPostgres(t, r, pg)
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
	access, _, pg, cluster := accessFixture()
	adoptAccessFixture(t, pg, cluster)
	access.Spec.Postgres, access.Spec.PostgresBranch = pg.Name, ""
	access.Spec.AccessLevel = v1.PostgresAccessLevelRead
	r := makePostgresAccessReconciler()
	access.Finalizers = []string{r.Name()}
	pg.Spec.Recovery = &v1.PostgresRecovery{SourcePostgres: "source"}
	group := rccnpg.PersonalAccessRoleForPostgres(v1.PostgresPhysicalName(pg.Name), pg)
	metav1.SetMetaDataAnnotation(&cluster.ObjectMeta, rccnpg.PersonalAccessGroupAnnotation, group)
	c := withoutBranchAPI(t, fake.NewClientBuilder().WithScheme(r.Scheme).WithObjects(pg, cluster).Build())
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
