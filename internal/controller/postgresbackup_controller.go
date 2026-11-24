package controller

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/backupcopy"
	"github.com/nais/pgrator/internal/config"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/resourcecreator/drbackup"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	fqdnv1alpha3 "github.com/nais/pgrator/internal/thirdparty/google/networking/v1alpha3"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const postgresBackupPostgresIndex = "spec.postgres"

// PostgresBackupReconciler orchestrates a standalone disaster-recovery backup
// by triggering a dedicated CloudNativePG Backup against the source database's
// cluster, copying and verifying the backup in the team's DR bucket via a Job,
// and cleaning up the DR bucket prefix on expiry or deletion.
type PostgresBackupReconciler struct {
	Config   *config.Config
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

type PostgresBackupPreparedData struct {
	CNPGBackup    *cnpgv1.Backup                 `yaml:"cnpgBackup,omitempty"`
	CopyJob       *batchv1.Job                   `yaml:"copyJob,omitempty"`
	CleanupJob    *batchv1.Job                   `yaml:"cleanupJob,omitempty"`
	CopyResult    *backupcopy.TerminationPayload `yaml:"copyResult,omitempty"`
	TeamProjectID string                         `yaml:"teamProjectID,omitempty"`
	SourceBucket  string                         `yaml:"sourceBucket,omitempty"`
}

var _ reconciler.Reconciler[*v1.PostgresBackup, PostgresBackupPreparedData] = &PostgresBackupReconciler{}

func (r *PostgresBackupReconciler) Name() string { return "postgresbackup.nais.io" }

func (r *PostgresBackupReconciler) New() *v1.PostgresBackup { return &v1.PostgresBackup{} }

func (r *PostgresBackupReconciler) OwnedTypes() []reconciler.OwnedType {
	return []reconciler.OwnedType{
		{
			Type: &cnpgv1.Backup{},
			AdditionalPredicate: predicate.Funcs{
				CreateFunc: func(event.CreateEvent) bool { return true },
				DeleteFunc: func(event.DeleteEvent) bool { return true },
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldBackup, oldOK := e.ObjectOld.(*cnpgv1.Backup)
					newBackup, newOK := e.ObjectNew.(*cnpgv1.Backup)
					return oldOK && newOK && (!reflect.DeepEqual(oldBackup.Status, newBackup.Status) ||
						oldBackup.DeletionTimestamp.IsZero() != newBackup.DeletionTimestamp.IsZero())
				},
			},
		},
		{
			Type: &batchv1.Job{},
			AdditionalPredicate: predicate.Funcs{
				CreateFunc: func(event.CreateEvent) bool { return true },
				DeleteFunc: func(event.DeleteEvent) bool { return true },
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldJob, oldOK := e.ObjectOld.(*batchv1.Job)
					newJob, newOK := e.ObjectNew.(*batchv1.Job)
					return oldOK && newOK && (!reflect.DeepEqual(oldJob.Status, newJob.Status) ||
						oldJob.DeletionTimestamp.IsZero() != newJob.DeletionTimestamp.IsZero())
				},
			},
		},
		{
			Type: &iamcnrm.IAMPolicyMember{},
			AdditionalPredicate: predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
				oldResource, oldOK := e.ObjectOld.(*iamcnrm.IAMPolicyMember)
				newResource, newOK := e.ObjectNew.(*iamcnrm.IAMPolicyMember)
				return oldOK && newOK && configConnectorReady(oldResource) != configConnectorReady(newResource)
			}},
		},
	}
}

func (r *PostgresBackupReconciler) AdditionalTypes() []client.Object {
	return []client.Object{
		&iamcnrm.IAMServiceAccount{},
		&storagecnrm.StorageBucket{},
		&corev1.ServiceAccount{},
		&networkingv1.NetworkPolicy{},
		&fqdnv1alpha3.FQDNNetworkPolicy{},
	}
}

func (r *PostgresBackupReconciler) Indexes() []reconciler.Index {
	return []reconciler.Index{{
		Object: &v1.PostgresBackup{},
		Field:  postgresBackupPostgresIndex,
		ExtractValue: func(object client.Object) []string {
			backup, ok := object.(*v1.PostgresBackup)
			if !ok || backup.Spec.Postgres == "" {
				return nil
			}
			return []string{backup.Spec.Postgres}
		},
	}}
}

func (r *PostgresBackupReconciler) RelationshipWatches() []reconciler.RelationshipWatch {
	return []reconciler.RelationshipWatch{
		{
			Type: &v1.Postgres{},
			Map:  r.backupsForPostgres,
			Predicate: predicate.Or(predicate.GenerationChangedPredicate{}, predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldPG, oldOK := e.ObjectOld.(*v1.Postgres)
					newPG, newOK := e.ObjectNew.(*v1.Postgres)
					return oldOK && newOK && (directClusterName(oldPG) != directClusterName(newPG) ||
						oldPG.DeletionTimestamp.IsZero() != newPG.DeletionTimestamp.IsZero())
				},
			}),
		},
		{
			Type: &cnpgv1.Cluster{},
			Map:  r.backupsForCluster,
			Predicate: predicate.Funcs{
				CreateFunc: func(event.CreateEvent) bool { return true },
				DeleteFunc: func(event.DeleteEvent) bool { return true },
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldCluster, oldOK := e.ObjectOld.(*cnpgv1.Cluster)
					newCluster, newOK := e.ObjectNew.(*cnpgv1.Cluster)
					return oldOK && newOK && (recoveryComplete(oldCluster) != recoveryComplete(newCluster) ||
						oldCluster.DeletionTimestamp.IsZero() != newCluster.DeletionTimestamp.IsZero())
				},
			},
		},
	}
}

func (r *PostgresBackupReconciler) backupsForPostgres(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
	postgres, ok := object.(*v1.Postgres)
	if !ok {
		return nil, nil
	}
	backups := &v1.PostgresBackupList{}
	if err := reader.List(ctx, backups, client.InNamespace(postgres.Namespace), client.MatchingFields{postgresBackupPostgresIndex: postgres.Name}); err != nil {
		return nil, fmt.Errorf("listing PostgresBackup resources for Postgres %q: %w", postgres.Name, err)
	}
	requests := make([]reconcile.Request, 0, len(backups.Items))
	for i := range backups.Items {
		if isTerminalPostgresBackup(&backups.Items[i]) {
			continue
		}
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&backups.Items[i])})
	}
	return requests, nil
}

func (r *PostgresBackupReconciler) backupsForCluster(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
	cluster, ok := object.(*cnpgv1.Cluster)
	if !ok {
		return nil, nil
	}
	backups := &v1.PostgresBackupList{}
	if err := reader.List(ctx, backups, client.InNamespace(cluster.Namespace)); err != nil {
		return nil, fmt.Errorf("listing PostgresBackup resources for Cluster %q: %w", cluster.Name, err)
	}
	requests := make([]reconcile.Request, 0)
	for i := range backups.Items {
		backup := &backups.Items[i]
		if isTerminalPostgresBackup(backup) {
			continue
		}
		if rccnpg.ClusterNameFor(v1.PostgresPhysicalName(backup.Spec.Postgres)) == cluster.Name {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(backup)})
		}
	}
	return requests, nil
}

func isTerminalPostgresBackup(backup *v1.PostgresBackup) bool {
	return backup.Status != nil && (backup.Status.Phase == v1.PostgresBackupPhaseCompleted || backup.Status.Phase == v1.PostgresBackupPhaseFailed)
}

// StatusCheckpointRequired persists the source database snapshot in status
// before the CNPG Backup child is created, so a later deletion of the source
// Postgres never leaves an in-flight backup without its provenance metadata.
func (r *PostgresBackupReconciler) StatusCheckpointRequired(current, original *v1.PostgresBackup) bool {
	if current.Status == nil || current.Status.SourceUID == "" {
		return false
	}
	before, after := original.Status, current.Status
	return before == nil || before.SourceUID != after.SourceUID || before.MajorVersion != after.MajorVersion ||
		!slices.Equal(before.Extensions, after.Extensions) || !reflect.DeepEqual(before.DiskSize, after.DiskSize)
}

func (r *PostgresBackupReconciler) Prepare(ctx context.Context, reader client.Reader, backup *v1.PostgresBackup) (PostgresBackupPreparedData, ctrl.Result, error) {
	var prep PostgresBackupPreparedData
	backup.GetStatus()

	if r.drEnabled() {
		ns := &corev1.Namespace{}
		if err := reader.Get(ctx, client.ObjectKey{Name: backup.Namespace}, ns); err != nil {
			return prep, ctrl.Result{}, err
		}
		prep.TeamProjectID = ns.Labels[ProjectIDLabel]
		if prep.TeamProjectID == "" {
			prep.TeamProjectID = ns.Annotations[ProjectIDAnnotationFallback]
		}
		if prep.TeamProjectID == "" {
			return prep, ctrl.Result{}, fmt.Errorf("namespace %q has no Google project identity", backup.Namespace)
		}
	}

	if !backup.DeletionTimestamp.IsZero() {
		var err error
		prep.CopyJob, err = getBackupJob(ctx, reader, backup, drbackup.CopyJobName(backup.Name))
		if err != nil {
			return prep, ctrl.Result{}, err
		}
		prep.CleanupJob, err = getBackupJob(ctx, reader, backup, drbackup.CleanupJobName(backup.Name))
		return prep, ctrl.Result{}, err
	}

	existing := &cnpgv1.Backup{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: backup.Namespace, Name: backup.Name}, existing); err == nil {
		if !metav1.IsControlledBy(existing, backup) {
			return prep, ctrl.Result{}, fmt.Errorf("CNPG Backup %q is not controlled by PostgresBackup", existing.Name)
		}
		prep.CNPGBackup = existing
	} else if !apierrors.IsNotFound(err) {
		return prep, ctrl.Result{}, fmt.Errorf("getting CNPG Backup %q: %w", backup.Name, err)
	}

	var err error
	prep.CopyJob, err = getBackupJob(ctx, reader, backup, drbackup.CopyJobName(backup.Name))
	if err != nil {
		return prep, ctrl.Result{}, err
	}

	if isTerminalPostgresBackup(backup) {
		return prep, ctrl.Result{}, nil
	}
	if prep.CopyJob != nil {
		prep.CopyResult = readJobTerminationPayload(ctx, reader, prep.CopyJob)
		prep.SourceBucket = prep.CopyJob.Annotations[drbackup.SourceBucketAnnotation]
		failed, _ := jobFailed(prep.CopyJob)
		if prep.SourceBucket != "" || jobSucceeded(prep.CopyJob) || failed {
			return prep, ctrl.Result{}, nil
		}
	}

	if prep.CNPGBackup != nil && backup.Status.SourceUID != "" && backup.Status.ServerName != "" {
		prep.SourceBucket = resolveSourceBucketName(prep.CNPGBackup, nil)
		if prep.SourceBucket == "" {
			if pg, cluster, err := resolveBackupTarget(ctx, reader, backup); err == nil && string(pg.UID) == backup.Status.SourceUID {
				prep.SourceBucket = resolveSourceBucketName(prep.CNPGBackup, cluster)
			}
		}
		return prep, ctrl.Result{}, nil
	}

	postgres, cluster, err := resolveBackupTarget(ctx, reader, backup)
	if err != nil {
		return prep, ctrl.Result{}, err
	}
	if backup.Status.SourceUID != "" && backup.Status.SourceUID != string(postgres.UID) {
		return prep, ctrl.Result{}, fmt.Errorf("source Postgres %q was replaced (expected UID %s, got %s)", postgres.Name, backup.Status.SourceUID, postgres.UID)
	}
	if postgres.Status.Ownership == nil {
		return prep, ctrl.Result{}, fmt.Errorf("source Postgres %q has no physical ownership checkpoint", postgres.Name)
	}
	if err := validateSourceExtensionState(ctx, reader, postgres, cluster); err != nil {
		return prep, ctrl.Result{}, fmt.Errorf("validating backup source extensions: %w", err)
	}
	prep.SourceBucket = resolveSourceBucketName(prep.CNPGBackup, cluster)
	recordSourceSnapshot(backup.Status, postgres, cluster)
	return prep, ctrl.Result{}, nil
}

func getBackupJob(ctx context.Context, reader client.Reader, backup *v1.PostgresBackup, name string) (*batchv1.Job, error) {
	job := &batchv1.Job{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: backup.Namespace, Name: name}, job); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting backup Job %q: %w", name, err)
	}
	if !metav1.IsControlledBy(job, backup) {
		return nil, fmt.Errorf("backup Job %q is not controlled by PostgresBackup", job.Name)
	}
	return job, nil
}

func resolveBackupTarget(ctx context.Context, reader client.Reader, backup *v1.PostgresBackup) (*v1.Postgres, *cnpgv1.Cluster, error) {
	postgres := &v1.Postgres{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: backup.Namespace, Name: backup.Spec.Postgres}, postgres); err != nil {
		return nil, nil, fmt.Errorf("getting source Postgres %q: %w", backup.Spec.Postgres, err)
	}
	if !postgres.DeletionTimestamp.IsZero() {
		return nil, nil, fmt.Errorf("source Postgres %q is deleting", postgres.Name)
	}
	clusterName := rccnpg.ClusterNameFor(v1.PostgresPhysicalName(postgres.Name))
	if directClusterName(postgres) != clusterName {
		return nil, nil, fmt.Errorf("source Postgres %q has no observed physical cluster yet", postgres.Name)
	}
	cluster := &cnpgv1.Cluster{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: backup.Namespace, Name: clusterName}, cluster); err != nil {
		return nil, nil, fmt.Errorf("getting source CNPG Cluster %q: %w", clusterName, err)
	}
	if !cluster.DeletionTimestamp.IsZero() {
		return nil, nil, fmt.Errorf("source CNPG Cluster %q is deleting", clusterName)
	}
	if !physicalControlledBy(cluster, postgres) {
		return nil, nil, fmt.Errorf("source CNPG Cluster %q is not controlled by Postgres %q", clusterName, postgres.Name)
	}
	if postgres.Status.Ownership != nil && postgres.Status.Ownership.ClusterUID != "" && postgres.Status.Ownership.ClusterUID != string(cluster.UID) {
		return nil, nil, fmt.Errorf("source CNPG Cluster %q was replaced after ownership was checkpointed", clusterName)
	}
	if !recoveryComplete(cluster) {
		return nil, nil, fmt.Errorf("source CNPG Cluster %q is not ready", clusterName)
	}
	hasBarman := slices.ContainsFunc(cluster.Spec.Plugins, func(p cnpgv1.PluginConfiguration) bool {
		return p.Name == rccnpg.BarmanPluginName && p.Enabled != nil && *p.Enabled
	})
	if !hasBarman {
		return nil, nil, fmt.Errorf("source CNPG Cluster %q has no enabled barman-cloud plugin", clusterName)
	}
	return postgres, cluster, nil
}

func recordSourceSnapshot(status *v1.PostgresBackupStatus, postgres *v1.Postgres, cluster *cnpgv1.Cluster) {
	status.SourceUID = string(postgres.UID)
	status.MajorVersion = postgres.Spec.MajorVersion
	status.Extensions = slices.Clone(postgres.Spec.Extensions)
	status.DiskSize = nil
	if !postgres.Spec.Resources.DiskSize.IsZero() {
		disk := postgres.Spec.Resources.DiskSize.DeepCopy()
		status.DiskSize = &disk
	}
	if status.ServerName == "" {
		status.ServerName = cluster.Name
	}
}

func (r *PostgresBackupReconciler) Update(backup *v1.PostgresBackup, prepared PostgresBackupPreparedData, related reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	status := backup.GetStatus().(*v1.PostgresBackupStatus)
	if status.Phase == v1.PostgresBackupPhaseCompleted {
		setBackupExpiry(backup, status)
	}
	if status.ExpiresAt != nil && !time.Now().Before(status.ExpiresAt.Time) {
		status.SetCondition(metav1.Condition{
			Type:               readyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             "Expired",
			Message:            "backup retention has expired",
			ObservedGeneration: backup.Generation,
		})
		var options []client.DeleteOption
		if backup.ResourceVersion != "" {
			options = append(options, client.Preconditions{ResourceVersion: &backup.ResourceVersion})
		}
		return []action.Action{action.DeleteIfExists(backup, backup, existsConditionGetter, r.Recorder, options...)}, ctrl.Result{}, nil
	}

	cnpgBackup := prepared.CNPGBackup
	if cnpgBackup == nil && related != nil {
		if matched := related.GetMatching(&cnpgv1.Backup{ObjectMeta: metav1.ObjectMeta{Name: backup.Name, Namespace: backup.Namespace}}); matched != nil {
			cnpgBackup = matched.(*cnpgv1.Backup)
		}
	}
	copyJob := prepared.CopyJob
	if copyJob == nil && related != nil {
		if matched := related.GetMatching(&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: drbackup.CopyJobName(backup.Name), Namespace: backup.Namespace}}); matched != nil {
			copyJob = matched.(*batchv1.Job)
		}
	}

	var actions []action.Action
	if r.drEnabled() {
		actions = append(actions, r.sharedDRInfrastructureActions(backup, prepared.TeamProjectID, true)...)
	}

	if isTerminalPostgresBackup(backup) {
		if cnpgBackup != nil {
			desired, err := rccnpg.CreateBackup(r.Scheme, backup, cnpgBackup.Spec.Cluster.Name)
			if err != nil {
				return nil, ctrl.Result{}, err
			}
			actions = append(actions, action.Claim(desired, backup, existsConditionGetter, r.Recorder))
		}
		var requeue ctrl.Result
		if status.ExpiresAt != nil {
			requeue.RequeueAfter = max(time.Until(status.ExpiresAt.Time), time.Second)
		}
		return actions, requeue, nil
	}

	clusterName := rccnpg.ClusterNameFor(v1.PostgresPhysicalName(backup.Spec.Postgres))
	desired, err := rccnpg.CreateBackup(r.Scheme, backup, clusterName)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	if cnpgBackup != nil {
		if !metav1.IsControlledBy(cnpgBackup, backup) || cnpgBackup.Spec.Cluster.Name != desired.Spec.Cluster.Name {
			return nil, ctrl.Result{}, fmt.Errorf("CNPG Backup %q has unexpected owner or target cluster", cnpgBackup.Name)
		}
		actions = append(actions, action.Claim(desired, backup, existsConditionGetter, r.Recorder))
		mirrorCNPGBackupMetadata(status, cnpgBackup)
	}
	if copyJob != nil || (r.drEnabled() && cnpgBackup != nil && cnpgBackup.Status.Phase == cnpgv1.BackupPhaseCompleted) {
		return r.reconcileDRCopy(backup, status, cnpgBackup, copyJob, prepared, related, actions)
	}
	if cnpgBackup == nil {
		status.Phase = v1.PostgresBackupPhasePreparing
		status.SetCondition(metav1.Condition{
			Type:               readyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             string(v1.PostgresBackupPhasePreparing),
			Message:            "starting CloudNativePG backup",
			ObservedGeneration: backup.Generation,
		})
		actions = append(actions, action.Create(desired, backup, existsConditionGetter, r.Recorder))
		return actions, ctrl.Result{}, nil
	}

	mirrorCNPGBackupPhase(backup, status, cnpgBackup)
	return actions, ctrl.Result{}, nil
}

func (r *PostgresBackupReconciler) reconcileDRCopy(
	backup *v1.PostgresBackup,
	status *v1.PostgresBackupStatus,
	cnpgBackup *cnpgv1.Backup,
	copyJob *batchv1.Job,
	prepared PostgresBackupPreparedData,
	related reconciler.RelatedObjects,
	actions []action.Action,
) ([]action.Action, ctrl.Result, error) {
	if copyJob != nil {
		if !metav1.IsControlledBy(copyJob, backup) {
			return nil, ctrl.Result{}, fmt.Errorf("copy Job %q is not controlled by PostgresBackup", copyJob.Name)
		}
		// Keep the observed Job until its outcome has been committed to status.
		actions = append(actions, action.Claim(copyJob, backup, existsConditionGetter, r.Recorder))
	}

	if jobSucceeded(copyJob) {
		status.Phase = v1.PostgresBackupPhaseCompleted
		r.recordCopyDestination(backup, status)
		if prepared.CopyResult != nil && prepared.CopyResult.SizeBytes > 0 {
			size := prepared.CopyResult.SizeBytes
			status.SizeBytes = &size
		}
		if status.CompletedAt == nil {
			if copyJob.Status.CompletionTime != nil {
				done := copyJob.Status.CompletionTime.DeepCopy()
				status.CompletedAt = done
			} else {
				now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
				status.CompletedAt = &now
			}
		}
		setBackupExpiry(backup, status)
		status.ErrorMessage = ""
		status.SetCondition(metav1.Condition{
			Type:               readyCondition,
			Status:             metav1.ConditionTrue,
			Reason:             string(v1.PostgresBackupPhaseCompleted),
			Message:            "backup copied and verified in disaster-recovery bucket",
			ObservedGeneration: backup.Generation,
		})
		return actions, ctrl.Result{RequeueAfter: time.Second}, nil
	}

	if failed, msg := jobFailed(copyJob); failed {
		r.recordCopyDestination(backup, status)
		if prepared.CopyResult != nil && prepared.CopyResult.Error != "" {
			msg = prepared.CopyResult.Error
		}
		status.Phase = v1.PostgresBackupPhaseFailed
		status.ErrorMessage = msg
		status.SetCondition(metav1.Condition{
			Type:               readyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             "CopyFailed",
			Message:            msg,
			ObservedGeneration: backup.Generation,
		})
		return actions, ctrl.Result{RequeueAfter: time.Second}, nil
	}

	sourceBucket := prepared.SourceBucket
	if sourceBucket == "" && copyJob != nil {
		sourceBucket = copyJob.Annotations[drbackup.SourceBucketAnnotation]
	}
	if sourceBucket == "" {
		sourceBucket = resolveSourceBucketName(cnpgBackup, nil)
	}
	if sourceBucket == "" {
		return nil, ctrl.Result{}, fmt.Errorf("unable to determine source WAL bucket for PostgresBackup %q", backup.Name)
	}
	policyAction, existingPolicy, err := r.sourceBucketPolicyAction(backup, prepared.TeamProjectID, sourceBucket, related)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	actions = append(actions, policyAction)

	status.Phase = v1.PostgresBackupPhaseRunning
	if copyJob == nil && !drInfrastructureReady(backup.Namespace, r.drBucketName(backup.Namespace), existingPolicy, related) {
		status.SetCondition(metav1.Condition{
			Type:               readyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             string(v1.PostgresBackupPhaseRunning),
			Message:            "waiting for disaster-recovery bucket and IAM readiness",
			ObservedGeneration: backup.Generation,
		})
		return actions, ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if copyJob == nil {
		// Checkpoint the destination in a separate reconcile, before a worker
		// can write anything there. Deletion can then always find partial data.
		if status.Location == nil {
			r.recordCopyDestination(backup, status)
			return actions, ctrl.Result{RequeueAfter: time.Second}, nil
		}
		desiredJob, err := drbackup.CreateCopyJob(r.Scheme, backup, r.Config.CNPG.BackupWorkerImage, sourceBucket, status.Location.BucketName, r.Config.CNPG.BackupCopyDeadline)
		if err != nil {
			return nil, ctrl.Result{}, err
		}
		actions = append(actions, action.Create(desiredJob, backup, existsConditionGetter, r.Recorder))
	}
	status.SetCondition(metav1.Condition{
		Type:               readyCondition,
		Status:             metav1.ConditionFalse,
		Reason:             string(v1.PostgresBackupPhaseRunning),
		Message:            "copying backup to disaster-recovery bucket",
		ObservedGeneration: backup.Generation,
	})
	return actions, ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func (r *PostgresBackupReconciler) recordCopyDestination(backup *v1.PostgresBackup, status *v1.PostgresBackupStatus) {
	if status.Location == nil && r.drEnabled() {
		status.Location = &v1.PostgresBackupLocation{BucketName: r.drBucketName(backup.Namespace), Prefix: backup.Name}
	}
}

func setBackupExpiry(backup *v1.PostgresBackup, status *v1.PostgresBackupStatus) {
	if status.CompletedAt != nil {
		expires := metav1.NewTime(status.CompletedAt.Add(time.Duration(backup.Spec.EffectiveRetentionDays()) * 24 * time.Hour))
		status.ExpiresAt = &expires
	}
}

func mirrorCNPGBackupMetadata(status *v1.PostgresBackupStatus, cnpgBackup *cnpgv1.Backup) {
	if cnpgBackup.Status.BackupID != "" {
		status.BackupID = cnpgBackup.Status.BackupID
	}
	if cnpgBackup.Status.ServerName != "" {
		status.ServerName = cnpgBackup.Status.ServerName
	}
	if cnpgBackup.Status.StartedAt != nil {
		started := cnpgBackup.Status.StartedAt.DeepCopy()
		status.StartedAt = started
	}
	if cnpgBackup.Status.BeginWal != "" {
		status.BeginWal = cnpgBackup.Status.BeginWal
	}
	if cnpgBackup.Status.EndWal != "" {
		status.EndWal = cnpgBackup.Status.EndWal
	}
}

func mirrorCNPGBackupPhase(backup *v1.PostgresBackup, status *v1.PostgresBackupStatus, cnpgBackup *cnpgv1.Backup) {
	switch cnpgBackup.Status.Phase {
	case "", cnpgv1.BackupPhasePending:
		status.Phase = v1.PostgresBackupPhasePreparing
		status.SetCondition(metav1.Condition{
			Type:               readyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             string(v1.PostgresBackupPhasePreparing),
			Message:            "waiting for CloudNativePG backup to start",
			ObservedGeneration: backup.Generation,
		})
	case cnpgv1.BackupPhaseStarted, cnpgv1.BackupPhaseRunning, cnpgv1.BackupPhaseFinalizing:
		status.Phase = v1.PostgresBackupPhaseRunning
		status.SetCondition(metav1.Condition{
			Type:               readyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             string(v1.PostgresBackupPhaseRunning),
			Message:            "CloudNativePG backup is running",
			ObservedGeneration: backup.Generation,
		})
	case cnpgv1.BackupPhaseCompleted:
		// A CNPG Backup alone depends on the source's WAL bucket and does not
		// satisfy the standalone PostgresBackup contract.
		status.Phase = v1.PostgresBackupPhaseFailed
		status.ErrorMessage = "disaster-recovery storage is not configured"
		status.SetCondition(metav1.Condition{
			Type:               readyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             "DRStorageNotConfigured",
			Message:            status.ErrorMessage,
			ObservedGeneration: backup.Generation,
		})
	case cnpgv1.BackupPhaseFailed, cnpgv1.BackupPhaseWalArchivingFailing:
		status.Phase = v1.PostgresBackupPhaseFailed
		msg := cnpgBackup.Status.Error
		if msg == "" {
			msg = fmt.Sprintf("CloudNativePG backup ended in phase %q", cnpgBackup.Status.Phase)
		}
		status.ErrorMessage = msg
		status.SetCondition(metav1.Condition{
			Type:               readyCondition,
			Status:             metav1.ConditionFalse,
			Reason:             "BackupFailed",
			Message:            msg,
			ObservedGeneration: backup.Generation,
		})
	}
}

func (r *PostgresBackupReconciler) Delete(backup *v1.PostgresBackup, prepared PostgresBackupPreparedData, related reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	var actions []action.Action
	status := backup.GetStatus().(*v1.PostgresBackupStatus)
	if copyJob := prepared.CopyJob; copyJob != nil {
		if !metav1.IsControlledBy(copyJob, backup) {
			return nil, ctrl.Result{}, fmt.Errorf("copy Job %q is not controlled by PostgresBackup", copyJob.Name)
		}
		if status.Location == nil {
			if !r.drEnabled() {
				return nil, ctrl.Result{}, fmt.Errorf("DR destination is unknown for copy Job %q", copyJob.Name)
			}
			r.recordCopyDestination(backup, status)
			return nil, ctrl.Result{RequeueAfter: time.Second}, nil
		}
		// Foreground deletion waits for the Job's Pods to stop writing, so
		// the cleanup worker cannot race a final upload to the same prefix.
		return stopBackupJob(backup, copyJob, r.Recorder), ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	cleanupJob := prepared.CleanupJob
	if cleanupJob == nil && related != nil {
		if matched := related.GetMatching(&batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: drbackup.CleanupJobName(backup.Name), Namespace: backup.Namespace}}); matched != nil {
			cleanupJob = matched.(*batchv1.Job)
		}
	}

	if cleanupJob != nil && !metav1.IsControlledBy(cleanupJob, backup) {
		return nil, ctrl.Result{}, fmt.Errorf("cleanup Job %q is not controlled by PostgresBackup", cleanupJob.Name)
	}
	needsBucketCleanup := status.Location != nil || cleanupJob != nil
	if needsBucketCleanup && !jobSucceeded(cleanupJob) {
		if !r.drEnabled() {
			return nil, ctrl.Result{}, fmt.Errorf("DR infrastructure is not configured for backup cleanup")
		}
		if failed, msg := jobFailed(cleanupJob); failed {
			r.Recorder.RecordEvent(backup, corev1.EventTypeWarning, "CleanupFailed", "DR bucket cleanup Job failed: %s", msg)
			for _, condition := range cleanupJob.Status.Conditions {
				if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
					if delay := time.Until(condition.LastTransitionTime.Add(time.Minute)); delay > 0 {
						return nil, ctrl.Result{RequeueAfter: delay}, nil
					}
					break
				}
			}
			return stopBackupJob(backup, cleanupJob, r.Recorder), ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		actions = append(actions, r.sharedDRInfrastructureActions(backup, prepared.TeamProjectID, true)...)
		drBucket := r.drBucketName(backup.Namespace)
		prefix := backup.Name
		if status.Location != nil {
			if status.Location.BucketName != "" {
				drBucket = status.Location.BucketName
			}
			if status.Location.Prefix != "" {
				prefix = status.Location.Prefix
			}
		}
		desiredCleanup, err := drbackup.CreateCleanupJob(r.Scheme, backup, r.Config.CNPG.BackupWorkerImage, drBucket, prefix)
		if err != nil {
			return nil, ctrl.Result{}, err
		}
		if cleanupJob == nil {
			actions = append(actions, action.Create(desiredCleanup, backup, existsConditionGetter, r.Recorder))
		} else {
			actions = append(actions, action.Claim(desiredCleanup, backup, existsConditionGetter, r.Recorder))
		}
		return actions, ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if r.drEnabled() {
		actions = append(actions, r.sharedDRInfrastructureActions(backup, prepared.TeamProjectID, false)...)
	}
	if related != nil {
		for _, typ := range []client.Object{&cnpgv1.Backup{}, &batchv1.Job{}, &iamcnrm.IAMPolicyMember{}} {
			for _, obj := range related.GetMatchingType(typ) {
				if metav1.IsControlledBy(obj, backup) {
					actions = append(actions, action.DeleteIfExists(obj, backup, existsConditionGetter, r.Recorder))
				}
			}
		}
	}
	return actions, ctrl.Result{}, nil
}

func stopBackupJob(backup *v1.PostgresBackup, job *batchv1.Job, recorder events.Recorder) []action.Action {
	if !job.DeletionTimestamp.IsZero() {
		return nil
	}
	return []action.Action{action.DeleteIfExists(job, backup, existsConditionGetter, recorder, client.PropagationPolicy(metav1.DeletePropagationForeground))}
}
