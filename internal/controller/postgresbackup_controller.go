package controller

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	v1 "github.com/nais/pgrator/pkg/api/v1"
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
// cluster and mirroring its progress into PostgresBackup.status.
type PostgresBackupReconciler struct {
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

type PostgresBackupPreparedData struct {
	Postgres   *v1.Postgres    `yaml:"postgres,omitempty"`
	Cluster    *cnpgv1.Cluster `yaml:"cluster,omitempty"`
	CNPGBackup *cnpgv1.Backup  `yaml:"cnpgBackup,omitempty"`
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
	}
}

func (r *PostgresBackupReconciler) AdditionalTypes() []client.Object { return nil }

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
	var origUID string
	if original.Status != nil {
		origUID = original.Status.SourceUID
	}
	var currUID string
	if current.Status != nil {
		currUID = current.Status.SourceUID
	}
	return currUID != "" && currUID != origUID
}

func (r *PostgresBackupReconciler) Prepare(ctx context.Context, reader client.Reader, backup *v1.PostgresBackup) (PostgresBackupPreparedData, ctrl.Result, error) {
	var prep PostgresBackupPreparedData
	backup.GetStatus()
	if !backup.DeletionTimestamp.IsZero() {
		return prep, ctrl.Result{}, nil
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

	// Once a backup has reached a terminal phase or its CNPG Backup is already
	// created and source metadata has been checkpointed, source Postgres lookup
	// is optional so that deleting the source Postgres does not break completed
	// or finalizing backups.
	if isTerminalPostgresBackup(backup) || (prep.CNPGBackup != nil && backup.Status.SourceUID != "" && backup.Status.ServerName != "") {
		return prep, ctrl.Result{}, nil
	}

	postgres, cluster, err := resolveBackupTarget(ctx, reader, backup)
	if err != nil {
		return prep, ctrl.Result{}, err
	}
	if backup.Status.SourceUID != "" && backup.Status.SourceUID != string(postgres.UID) {
		return prep, ctrl.Result{}, fmt.Errorf("source Postgres %q was replaced (expected UID %s, got %s)", postgres.Name, backup.Status.SourceUID, postgres.UID)
	}
	prep.Postgres = postgres
	prep.Cluster = cluster
	recordSourceSnapshot(backup.Status, postgres, cluster)
	return prep, ctrl.Result{}, nil
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
	if postgres != nil && status.SourceUID == "" {
		status.SourceUID = string(postgres.UID)
		status.MajorVersion = postgres.Spec.MajorVersion
		if len(postgres.Spec.Extensions) > 0 {
			status.Extensions = slices.Clone(postgres.Spec.Extensions)
		}
		if !postgres.Spec.Resources.DiskSize.IsZero() {
			disk := postgres.Spec.Resources.DiskSize.DeepCopy()
			status.DiskSize = &disk
		}
	}
	if cluster != nil && status.ServerName == "" {
		status.ServerName = cluster.Name
	}
}

func (r *PostgresBackupReconciler) Update(backup *v1.PostgresBackup, prepared PostgresBackupPreparedData, related reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	status := backup.GetStatus().(*v1.PostgresBackupStatus)
	recordSourceSnapshot(status, prepared.Postgres, prepared.Cluster)

	cnpgBackup := prepared.CNPGBackup
	if cnpgBackup == nil && related != nil {
		if matched := related.GetMatching(&cnpgv1.Backup{ObjectMeta: metav1.ObjectMeta{Name: backup.Name, Namespace: backup.Namespace}}); matched != nil {
			cnpgBackup = matched.(*cnpgv1.Backup)
		}
	}

	if isTerminalPostgresBackup(backup) {
		if cnpgBackup != nil {
			desired, err := rccnpg.CreateBackup(r.Scheme, backup, cnpgBackup.Spec.Cluster.Name)
			if err != nil {
				return nil, ctrl.Result{}, err
			}
			return []action.Action{action.Claim(desired, backup, existsConditionGetter, r.Recorder)}, ctrl.Result{}, nil
		}
		return nil, ctrl.Result{}, nil
	}

	clusterName := status.ServerName
	if clusterName == "" && prepared.Cluster != nil {
		clusterName = prepared.Cluster.Name
	}
	if clusterName == "" && cnpgBackup != nil {
		clusterName = cnpgBackup.Spec.Cluster.Name
	}
	if clusterName == "" {
		clusterName = rccnpg.ClusterNameFor(v1.PostgresPhysicalName(backup.Spec.Postgres))
	}

	desired, err := rccnpg.CreateBackup(r.Scheme, backup, clusterName)
	if err != nil {
		return nil, ctrl.Result{}, err
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
		return []action.Action{action.Create(desired, backup, existsConditionGetter, r.Recorder)}, ctrl.Result{}, nil
	}

	if !metav1.IsControlledBy(cnpgBackup, backup) || cnpgBackup.Spec.Cluster.Name != desired.Spec.Cluster.Name {
		return nil, ctrl.Result{}, fmt.Errorf("CNPG Backup %q has unexpected owner or target cluster", cnpgBackup.Name)
	}

	mirrorCNPGBackupStatus(backup, status, cnpgBackup)
	return []action.Action{action.Claim(desired, backup, existsConditionGetter, r.Recorder)}, ctrl.Result{}, nil
}

func mirrorCNPGBackupStatus(backup *v1.PostgresBackup, status *v1.PostgresBackupStatus, cnpgBackup *cnpgv1.Backup) {
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
		status.Phase = v1.PostgresBackupPhaseCompleted
		if cnpgBackup.Status.StoppedAt != nil {
			completed := cnpgBackup.Status.StoppedAt.DeepCopy()
			status.CompletedAt = completed
		} else if status.CompletedAt == nil {
			now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
			status.CompletedAt = &now
		}
		if status.ExpiresAt == nil && status.CompletedAt != nil {
			expires := metav1.NewTime(status.CompletedAt.Add(time.Duration(backup.Spec.EffectiveRetentionDays()) * 24 * time.Hour))
			status.ExpiresAt = &expires
		}
		status.ErrorMessage = ""
		status.SetCondition(metav1.Condition{
			Type:               readyCondition,
			Status:             metav1.ConditionTrue,
			Reason:             string(v1.PostgresBackupPhaseCompleted),
			Message:            "backup completed",
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

func (r *PostgresBackupReconciler) Delete(backup *v1.PostgresBackup, _ PostgresBackupPreparedData, related reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	var actions []action.Action
	if related != nil {
		for _, obj := range related.GetMatchingType(&cnpgv1.Backup{}) {
			actions = append(actions, action.DeleteIfExists(obj, backup, existsConditionGetter, r.Recorder))
		}
	}
	return actions, ctrl.Result{}, nil
}
