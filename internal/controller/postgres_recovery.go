package controller

import (
	"context"
	"fmt"
	"slices"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// prepareDirectRecovery preserves the branch-era bootstrap protocol, but the
// durable owner, source identity and checkpoints are all Postgres resources.
func (r *PostgresClusterReconciler) prepareDirectRecovery(ctx context.Context, reader client.Reader, pg *v1.Postgres, p *PostgresClusterPreparedData) error {
	recovery := pg.Spec.Recovery
	if recovery == nil {
		return nil
	}
	if err := recoveryFailureError(pg); err != nil {
		return err
	}
	if recovery.SourcePostgres == pg.Name || recovery.SourceUID == "" || recovery.BucketName == "" || recovery.ServerName == "" || recovery.TargetTime.IsZero() || recovery.TargetTime.After(time.Now()) {
		return fmt.Errorf("recovery requires a different source database, pinned archive identity and a past target time")
	}
	p.RecoverySource = &rccnpg.RecoverySource{BucketName: recovery.BucketName, ServerName: recovery.ServerName, TargetTime: recovery.TargetTime}
	if recoveryFinished(pg, p.Cluster) {
		return nil
	}
	if !r.physical().walArchivingEnabled() {
		return fmt.Errorf("recovery requires WAL archiving")
	}
	if p.Cluster != nil {
		// A successful own backup proves bootstrap already finished even if the
		// controller missed that transition and observes a later cluster outage.
		if recoveryBackedUp(p.Cluster) {
			pg.GetStatus().SetCondition(metav1.Condition{Type: recoveryCompletedCondition, Status: metav1.ConditionTrue, Reason: "Completed", Message: string(p.Cluster.UID), ObservedGeneration: pg.Generation})
			return nil
		}
		failed, err := recoveryBootstrapFailed(ctx, reader, p.Cluster)
		if err != nil {
			return err
		}
		if failed {
			pg.GetStatus().SetCondition(metav1.Condition{
				Type: recoveryFailedCondition, Status: metav1.ConditionTrue, Reason: "Unrecoverable", ObservedGeneration: pg.Generation,
				Message: fmt.Sprintf("Recovery of Cluster %q (UID %s) to %s failed: %s. %s", p.Cluster.Name, p.Cluster.UID, recovery.TargetTime.UTC().Format(time.RFC3339Nano), p.Cluster.Status.Phase, p.Cluster.Status.PhaseReason),
			})
			return recoveryFailureError(pg)
		}
	}
	ready, err := validateDirectRecoverySource(ctx, reader, pg, p)
	p.RecoverySourceReady = ready
	return err
}

// Recovery grants are temporary access, not durable database identities. Keep
// them out of the data-resource journal so they can be revoked after detach.
func isPostgresRecoveryGrant(pg *v1.Postgres, object client.Object) bool {
	_, policy := object.(*iamcnrm.IAMPolicyMember)
	return pg.Spec.Recovery != nil && policy && object.GetName() == recoverySourcePolicyNameFor(v1.PostgresPhysicalName(pg.Name))
}

func validateDirectRecoverySource(ctx context.Context, reader client.Reader, pg *v1.Postgres, prepared *PostgresClusterPreparedData) (bool, error) {
	recovery := pg.Spec.Recovery
	source := &v1.Postgres{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: pg.Namespace, Name: recovery.SourcePostgres}, source); err != nil {
		return false, fmt.Errorf("getting recovery source: %w", err)
	}
	if string(source.UID) != recovery.SourceUID {
		return false, fmt.Errorf("recovery source was replaced")
	}
	accepted, err := recoveryWasAccepted(source, pg)
	if err != nil {
		return false, err
	}
	if !source.DeletionTimestamp.IsZero() && !accepted {
		return false, fmt.Errorf("recovery source is deleting and cannot accept a new recovery")
	}
	prepared.RecoveryAdmissionRequired = !accepted
	if prepared.Cluster == nil {
		if err := validateRecoveryConfiguration(pg, source); err != nil {
			return false, err
		}
	}
	cluster := &cnpgv1.Cluster{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: pg.Namespace, Name: recovery.ServerName}, cluster); err != nil {
		return false, fmt.Errorf("getting recovery source cluster: %w", err)
	}
	if !physicalControlledBy(cluster, source) || source.Status == nil || source.Status.ClusterName != cluster.Name || !cluster.DeletionTimestamp.IsZero() {
		return false, fmt.Errorf("recovery archive does not belong to the selected source database")
	}
	if source.Status.Ownership == nil || source.Status.Ownership.Phase != ownershipManaged || source.Status.Ownership.ClusterUID != string(cluster.UID) {
		return false, fmt.Errorf("recovery source has no matching managed cluster checkpoint")
	}
	if prepared.Cluster == nil {
		if err := validateSourceExtensionState(ctx, reader, source, cluster); err != nil {
			return false, err
		}
	}
	archiveFound := false
	for _, plugin := range cluster.Spec.Plugins {
		if plugin.Name == rccnpg.BarmanPluginName && plugin.Parameters["barmanObjectName"] == recovery.BucketName {
			archiveFound = true
		}
	}
	if !archiveFound {
		return false, fmt.Errorf("recovery archive does not match the source cluster")
	}
	store := &barmanv1.ObjectStore{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: pg.Namespace, Name: recovery.BucketName}, store); err != nil {
		return false, fmt.Errorf("getting recovery ObjectStore: %w", err)
	}
	if !physicalControlledBy(store, source) || !store.DeletionTimestamp.IsZero() || !sourceResourceCheckpointMatches(source, store, "ObjectStore") {
		return false, fmt.Errorf("recovery ObjectStore does not match the source database ownership checkpoint")
	}
	bucket := &storagecnrm.StorageBucket{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: pg.Namespace, Name: recovery.BucketName}, bucket); err != nil {
		return false, fmt.Errorf("getting recovery source bucket: %w", err)
	}
	if !physicalControlledBy(bucket, source) || !bucket.DeletionTimestamp.IsZero() || !sourceResourceCheckpointMatches(source, bucket, "StorageBucket") {
		return false, fmt.Errorf("recovery bucket does not match the source database ownership checkpoint")
	}
	return configConnectorReady(bucket), nil
}

func validateRecoveryConfiguration(pg, source *v1.Postgres) error {
	if source.Spec.MajorVersion != pg.Spec.MajorVersion {
		return fmt.Errorf("recovery must use the source PostgreSQL major version")
	}
	for _, extension := range source.Spec.Extensions {
		if !slices.Contains(pg.Spec.Extensions, extension) {
			return fmt.Errorf("recovery must include source extension %q", extension.Name)
		}
	}
	if pg.Spec.Resources.DiskSize.Cmp(source.Spec.Resources.DiskSize) < 0 {
		return fmt.Errorf("recovery disk size must not be smaller than the source")
	}
	return nil
}

func sourceResourceCheckpointMatches(source *v1.Postgres, object client.Object, kind string) bool {
	for _, ref := range source.Status.Ownership.Resources {
		if ref.Kind == kind && ref.Name == object.GetName() && ref.UID == string(object.GetUID()) {
			return true
		}
	}
	return false
}

func validateSourceExtensionState(ctx context.Context, reader client.Reader, source *v1.Postgres, cluster *cnpgv1.Cluster) error {
	database := &cnpgv1.Database{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: source.Namespace, Name: cluster.Name + "-" + rccnpg.DatabaseName}, database); err != nil {
		return fmt.Errorf("getting recovery source Database for extension validation: %w", err)
	}
	if !physicalControlledBy(database, source) || !database.DeletionTimestamp.IsZero() || !sourceResourceCheckpointMatches(source, database, "Database") || database.Spec.ClusterRef.Name != cluster.Name || database.Spec.Name != rccnpg.DatabaseName {
		return fmt.Errorf("recovery source Database does not match its ownership checkpoint")
	}
	return rccnpg.ValidateRecoverySourceExtensions(source.Spec.Extensions, database, cluster)
}

// Source deletion must wait until every live dependent has its own backup and
// has detached the recovery inputs from its actual CNPG Cluster.
func blockingPostgresRecoveries(ctx context.Context, reader client.Reader, source *v1.Postgres) ([]string, error) {
	pending, err := pendingRecoveryAdmissions(ctx, reader, source)
	if err != nil {
		return nil, err
	}
	var blocking []string
	for _, name := range pending {
		blocking = append(blocking, name)
	}
	slices.Sort(blocking)
	return blocking, nil
}
