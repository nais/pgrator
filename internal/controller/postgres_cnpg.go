package controller

import (
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	rcmonitoring "github.com/nais/pgrator/internal/resourcecreator/monitoring"
	rcnetpol "github.com/nais/pgrator/internal/resourcecreator/netpol"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	"github.com/nais/pgrator/pkg/api"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *PostgresReconciler) updateCNPG(obj *data_nais_io_v1.Postgres, preparedData PreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	pgClusterName, pgNamespace, err := getClusterNameAndNamespace(obj, api.EngineCNPG)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	var actions []action.Action

	cluster, err := rccnpg.CreateClusterSpec(obj, r.Config, pgClusterName, pgNamespace)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	existingCluster := relatedObjects.GetMatching(cluster)
	if existingCluster != nil {
		actions = append(actions, action.Update(cluster, obj, cnpgClusterConditionGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(cluster, obj, cnpgClusterConditionGetter, r.Recorder))
	}

	if r.Config.CNPG.BackupBucket != "" {
		backup := rccnpg.CreateScheduledBackup(obj, r.Config, pgClusterName, pgNamespace)
		actions = append(actions, action.CreateOrUpdate(backup, obj, existsConditionGetter, r.Recorder))
	}

	pooler := rccnpg.CreatePooler(obj, pgClusterName, pgNamespace)
	existingPooler := relatedObjects.GetMatching(pooler)
	if existingPooler != nil {
		actions = append(actions, action.Update(pooler, obj, existsConditionGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(pooler, obj, existsConditionGetter, r.Recorder))
	}

	cnpgNetpol := rcnetpol.CreateCNPG(obj, pgClusterName, pgNamespace)
	actions = append(actions, action.CreateOrUpdate(cnpgNetpol, obj, existsConditionGetter, r.Recorder))

	// TODO: Set up IAM
	iamActions, err := r.cnpgIAMActions(obj, preparedData, pgNamespace, r.Config.CNPG.BackupBucket, relatedObjects)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	actions = append(actions, iamActions...)

	if !r.Config.PrometheusRulesDisabled {
		prometheusRule := rcmonitoring.CreateCNPGPrometheusRule(obj, pgClusterName, pgNamespace)
		actions = append(actions, action.CreateOrUpdate(prometheusRule, obj, existsConditionGetter, r.Recorder))

		podMonitor := rcmonitoring.CreateCNPGPodMonitor(obj, pgClusterName, pgNamespace)
		actions = append(actions, action.CreateOrUpdate(podMonitor, obj, existsConditionGetter, r.Recorder))
	}

	return actions, ctrl.Result{}, nil
}

func (r *PostgresReconciler) deleteCNPG(obj *data_nais_io_v1.Postgres, preparedData PreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	actionFunc := action.DeleteIfExists
	sharedActionFunc := action.Unclaim
	if !obj.Spec.Cluster.AllowDeletion {
		actionFunc = action.NoOp
		sharedActionFunc = action.NoOp
	}

	pgClusterName, pgNamespace, err := getClusterNameAndNamespace(obj, api.EngineCNPG)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	actions := make([]action.Action, 0, 4)

	cluster := rccnpg.MinimalCluster(obj, pgClusterName, pgNamespace)
	actions = append(actions, actionFunc(cluster, obj, cnpgClusterConditionGetter, r.Recorder))

	backup := rccnpg.MinimalScheduledBackup(obj, pgClusterName, pgNamespace)
	actions = append(actions, actionFunc(backup, obj, existsConditionGetter, r.Recorder))

	pooler := rccnpg.MinimalPooler(obj, pgClusterName, pgNamespace)
	actions = append(actions, actionFunc(pooler, obj, existsConditionGetter, r.Recorder))

	cnpgNetpol := rcnetpol.Minimal(obj, pgClusterName, pgNamespace)
	actions = append(actions, actionFunc(cnpgNetpol, obj, existsConditionGetter, r.Recorder))

	iamActions := r.deleteIAMActions(obj, preparedData, pgNamespace, r.Config.CNPG.BackupBucket, sharedActionFunc, relatedObjects)
	actions = append(actions, iamActions...)

	if !r.Config.PrometheusRulesDisabled {
		prometheusRule := rcmonitoring.MinimalPrometheusRule(obj, pgClusterName)
		actions = append(actions, actionFunc(prometheusRule, obj, existsConditionGetter, r.Recorder))

		podMonitor := rcmonitoring.MinimalCNPGPodMonitor(obj, pgClusterName, pgNamespace)
		actions = append(actions, actionFunc(podMonitor, obj, existsConditionGetter, r.Recorder))
	}

	return actions, ctrl.Result{}, nil
}

// cnpgIAMActions returns the IAM actions used by CNPG
func (r *PostgresReconciler) cnpgIAMActions(obj *data_nais_io_v1.Postgres, preparedData PreparedData, pgNamespace, backupBucket string, relatedObjects reconciler.RelatedObjects) ([]action.Action, error) {
	// TODO: Implement IAM
	return nil, nil
}

func cnpgClusterConditionGetter(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition {
	cluster := obj.(*cnpgv1.Cluster)

	isReady := false
	for _, cond := range cluster.Status.Conditions {
		if cond.Type == "Ready" && cond.Status == meta_v1.ConditionTrue {
			isReady = true
			break
		}
	}

	conditions := []conditionConfig{
		{
			Type:   "Available",
			Status: isReady,
		},
		{
			Type: "Progressing",
			Status: cluster.Status.Phase == cnpgv1.PhaseFirstPrimary ||
				cluster.Status.Phase == cnpgv1.PhaseCreatingReplica ||
				cluster.Status.Phase == cnpgv1.PhaseUpgrade ||
				cluster.Status.Phase == cnpgv1.PhaseOnlineUpgrading ||
				cluster.Status.Phase == cnpgv1.PhaseApplyingConfiguration ||
				cluster.Status.Phase == cnpgv1.PhaseSwitchover,
		},
		{
			Type: "Degraded",
			Status: cluster.Status.Phase == cnpgv1.PhaseUnrecoverable ||
				cluster.Status.Phase == cnpgv1.PhaseImageCatalogError ||
				cluster.Status.ReadyInstances < cluster.Status.Instances,
		},
	}

	message := cluster.Status.Phase
	if message == "" {
		message = "Unknown"
	}

	reason := reasonable(message)

	result := make([]meta_v1.Condition, 0, len(conditions))
	for _, condition := range conditions {
		result = append(result, meta_v1.Condition{
			Type:               fmt.Sprintf("%s/%s", typePrefix(obj, scheme), condition.Type),
			Status:             makeCondition(condition.Status),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             reason,
			Message:            message,
		})
	}

	return result
}
