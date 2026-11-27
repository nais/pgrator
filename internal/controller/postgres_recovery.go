package controller

import (
	"context"
	"fmt"
	"slices"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func postgresRecoveryWatch() reconciler.RelationshipWatch {
	return reconciler.RelationshipWatch{
		Type:      &v1.PostgresRecovery{},
		Predicate: predicate.ResourceVersionChangedPredicate{},
		Map: func(_ context.Context, _ client.Reader, object client.Object) ([]reconcile.Request, error) {
			return []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(object)}}, nil
		},
	}
}

func getCompanionRecovery(ctx context.Context, reader client.Reader, pg *v1.Postgres) (*v1.PostgresRecovery, error) {
	name := pg.Annotations[v1.PostgresRecoveryAnnotation]
	if name == "" {
		name = pg.Name
	}
	if name != pg.Name {
		return nil, fmt.Errorf("recovery annotation %q must match Postgres name %q", name, pg.Name)
	}
	rec := &v1.PostgresRecovery{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: pg.Namespace, Name: name}, rec); err != nil {
		return nil, err
	}
	if rec.Spec.Postgres != pg.Name {
		return nil, fmt.Errorf("PostgresRecovery %q targets %q, not %q", rec.Name, rec.Spec.Postgres, pg.Name)
	}
	for _, ref := range rec.OwnerReferences {
		if ref.APIVersion == v1.GroupVersion.String() && ref.Kind == postgresKind && ref.Name == pg.Name && ref.UID != "" && pg.UID != "" && ref.UID != pg.UID {
			return nil, fmt.Errorf("PostgresRecovery %q belongs to a previous incarnation of %q", rec.Name, pg.Name)
		}
	}
	return rec, nil
}

func resolveRecoverySpecs(pg *v1.Postgres, companion *v1.PostgresRecovery, companionErr error) (*v1.PostgresPointInTimeRecovery, *v1.PostgresBackupRecovery, error) {
	switch {
	case companion != nil:
		if (companion.Spec.PointInTime != nil) == (companion.Spec.Backup != nil) {
			return nil, nil, fmt.Errorf("PostgresRecovery %q must specify exactly one of pointInTime or backup", companion.Name)
		}
		return companion.Spec.PointInTime, companion.Spec.Backup, nil
	case pg.Spec.Recovery != nil:
		return pg.Spec.Recovery, nil, nil
	case pg.Annotations[v1.PostgresRecoveryAnnotation] != "":
		return nil, nil, fmt.Errorf("getting companion PostgresRecovery %q: %w", pg.Name, companionErr)
	default:
		return nil, nil, nil
	}
}

func buildRecoverySource(pg *v1.Postgres, pitr *v1.PostgresPointInTimeRecovery, backupRec *v1.PostgresBackupRecovery) (*rccnpg.RecoverySource, string, error) {
	if pitr != nil {
		if pitr.SourcePostgres == pg.Name || pitr.SourceUID == "" || pitr.BucketName == "" || pitr.ServerName == "" || pitr.TargetTime.IsZero() || pitr.TargetTime.After(time.Now()) {
			return nil, "", fmt.Errorf("recovery requires a different source database, pinned archive identity and a past target time")
		}
		return &rccnpg.RecoverySource{
			BucketName: pitr.BucketName,
			ServerName: pitr.ServerName,
			TargetTime: pitr.TargetTime,
		}, pitr.TargetTime.UTC().Format(time.RFC3339Nano), nil
	}
	if backupRec.BackupName == "" || backupRec.BackupUID == "" || backupRec.BucketName == "" || backupRec.Prefix == "" || backupRec.ServerName == "" || backupRec.BackupID == "" {
		return nil, "", fmt.Errorf("backup recovery requires pinned backupName, backupUID, bucketName, prefix, serverName and backupID")
	}
	return &rccnpg.RecoverySource{
		BucketName:      backupRec.BucketName,
		ObjectStoreName: recoveryObjectStoreNameFor(v1.PostgresPhysicalName(pg.Name)),
		Prefix:          backupRec.Prefix,
		ServerName:      backupRec.ServerName,
		BackupID:        backupRec.BackupID,
	}, fmt.Sprintf("backup %s (%s)", backupRec.BackupName, backupRec.BackupID), nil
}

// prepareDirectRecovery pins the recovery source from either the companion
// PostgresRecovery resource or the legacy inline Postgres.spec.recovery field.
func (r *PostgresClusterReconciler) prepareDirectRecovery(ctx context.Context, reader client.Reader, pg *v1.Postgres, p *PostgresClusterPreparedData) error {
	if err := recoveryFailureError(pg); err != nil {
		return err
	}
	if p.Cluster != nil && recoveryBackedUp(p.Cluster) {
		pg.GetStatus().SetCondition(metav1.Condition{
			Type: recoveryCompletedCondition, Status: metav1.ConditionTrue,
			Reason: "Completed", Message: string(p.Cluster.UID), ObservedGeneration: pg.Generation,
		})
	}

	companion, companionErr := getCompanionRecovery(ctx, reader, pg)
	if companionErr != nil && !apierrors.IsNotFound(companionErr) {
		return companionErr
	}
	if companion != nil && companion.DeletionTimestamp.IsZero() {
		p.CompanionRecovery = companion
	}

	if recoveryFinished(pg, p.Cluster) {
		if p.Cluster != nil && (p.Cluster.Spec.Bootstrap != nil && p.Cluster.Spec.Bootstrap.Recovery != nil || len(p.Cluster.Spec.ExternalClusters) > 0) {
			p.RecoverySource = &rccnpg.RecoverySource{}
		}
		return nil
	}

	pitr, backupRec, err := resolveRecoverySpecs(pg, companion, companionErr)
	if err != nil {
		return err
	}
	if pitr == nil && backupRec == nil {
		if p.Cluster != nil && p.Cluster.Spec.Bootstrap != nil && p.Cluster.Spec.Bootstrap.Recovery != nil {
			return fmt.Errorf("in-progress recovery cluster %q has no recovery source specification", p.Cluster.Name)
		}
		return nil
	}

	var targetSummary string
	p.RecoverySource, targetSummary, err = buildRecoverySource(pg, pitr, backupRec)
	if err != nil {
		return err
	}

	if !r.physical().walArchivingEnabled() {
		return fmt.Errorf("recovery requires WAL archiving")
	}
	if p.Cluster != nil {
		failed, err := recoveryBootstrapFailed(ctx, reader, p.Cluster)
		if err != nil {
			return err
		}
		if failed {
			pg.GetStatus().SetCondition(metav1.Condition{
				Type: recoveryFailedCondition, Status: metav1.ConditionTrue, Reason: "Unrecoverable", ObservedGeneration: pg.Generation,
				Message: fmt.Sprintf("Recovery of Cluster %q (UID %s) to %s failed: %s. %s", p.Cluster.Name, p.Cluster.UID, targetSummary, p.Cluster.Status.Phase, p.Cluster.Status.PhaseReason),
			})
			return recoveryFailureError(pg)
		}
	}

	if pitr != nil {
		ready, err := validatePointInTimeRecoverySource(ctx, reader, pg, pitr, p)
		p.RecoverySourceReady = ready
		return err
	}
	ready, err := validateBackupRecoverySource(ctx, reader, pg, backupRec, p)
	p.RecoverySourceReady = ready
	return err
}

// Temporary recovery resources (IAM grant and DR-prefix ObjectStore) are not
// durable database identities. Keep them out of the data-resource journal so
// they can be deleted after detach.
func isTemporaryRecoveryResource(pg *v1.Postgres, object client.Object) bool {
	instance := v1.PostgresPhysicalName(pg.Name)
	switch object.(type) {
	case *iamcnrm.IAMPolicyMember:
		return object.GetName() == recoverySourcePolicyNameFor(instance)
	case *barmanv1.ObjectStore:
		return object.GetName() == recoveryObjectStoreNameFor(instance)
	default:
		return false
	}
}

func validatePointInTimeRecoverySource(ctx context.Context, reader client.Reader, pg *v1.Postgres, recovery *v1.PostgresPointInTimeRecovery, prepared *PostgresClusterPreparedData) (bool, error) {
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
	prepared.RecoveryAdmissionSource = &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: recovery.SourcePostgres, Namespace: pg.Namespace}}
	prepared.RecoveryAdmissionSourceUID = types.UID(recovery.SourceUID)
	prepared.RecoveryAdmissionFinalizer = "postgres.nais.io"

	if prepared.Cluster == nil {
		if err := validateRecoveryConfiguration(pg, source.Spec.MajorVersion, source.Spec.Extensions, source.Spec.Resources.DiskSize); err != nil {
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

func validateBackupRecoverySource(ctx context.Context, reader client.Reader, pg *v1.Postgres, recovery *v1.PostgresBackupRecovery, prepared *PostgresClusterPreparedData) (bool, error) {
	backup := &v1.PostgresBackup{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: pg.Namespace, Name: recovery.BackupName}, backup); err != nil {
		return false, fmt.Errorf("getting recovery PostgresBackup: %w", err)
	}
	if string(backup.UID) != recovery.BackupUID {
		return false, fmt.Errorf("recovery PostgresBackup was replaced")
	}
	if backup.Status == nil || backup.Status.Phase != v1.PostgresBackupPhaseCompleted {
		return false, fmt.Errorf("recovery PostgresBackup %q is not Completed", backup.Name)
	}
	if backup.Status.Location == nil || backup.Status.Location.BucketName != recovery.BucketName || backup.Status.Location.Prefix != recovery.Prefix ||
		backup.Status.ServerName != recovery.ServerName || backup.Status.BackupID != recovery.BackupID {
		return false, fmt.Errorf("recovery PostgresBackup %q does not match pinned archive location or backupID", backup.Name)
	}
	accepted, err := recoveryWasAccepted(backup, pg)
	if err != nil {
		return false, err
	}
	if !backup.DeletionTimestamp.IsZero() && !accepted {
		return false, fmt.Errorf("recovery PostgresBackup is deleting and cannot accept a new recovery")
	}
	if backup.Status.ExpiresAt != nil && !time.Now().Before(backup.Status.ExpiresAt.Time) && !accepted {
		return false, fmt.Errorf("recovery PostgresBackup has expired and cannot accept a new recovery")
	}
	prepared.RecoveryAdmissionRequired = !accepted
	prepared.RecoveryAdmissionSource = &v1.PostgresBackup{ObjectMeta: metav1.ObjectMeta{Name: recovery.BackupName, Namespace: pg.Namespace}}
	prepared.RecoveryAdmissionSourceUID = types.UID(recovery.BackupUID)
	prepared.RecoveryAdmissionFinalizer = "postgresbackup.nais.io"

	if prepared.Cluster == nil {
		var disk resource.Quantity
		if backup.Status.DiskSize != nil {
			disk = *backup.Status.DiskSize
		}
		if err := validateRecoveryConfiguration(pg, backup.Status.MajorVersion, backup.Status.Extensions, disk); err != nil {
			return false, err
		}
	}
	bucket := &storagecnrm.StorageBucket{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: pg.Namespace, Name: recovery.BucketName}, bucket); err != nil {
		return false, fmt.Errorf("getting recovery DR bucket: %w", err)
	}
	if !bucket.DeletionTimestamp.IsZero() {
		return false, fmt.Errorf("recovery DR bucket is deleting")
	}
	return configConnectorReady(bucket), nil
}

func validateRecoveryConfiguration(pg *v1.Postgres, majorVersion string, extensions []v1.PostgresExtension, diskSize resource.Quantity) error {
	if majorVersion != "" && majorVersion != pg.Spec.MajorVersion {
		return fmt.Errorf("recovery must use the source PostgreSQL major version")
	}
	for _, extension := range extensions {
		if !slices.Contains(pg.Spec.Extensions, extension) {
			return fmt.Errorf("recovery must include source extension %q", extension.Name)
		}
	}
	if !diskSize.IsZero() && pg.Spec.Resources.DiskSize.Cmp(diskSize) < 0 {
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
