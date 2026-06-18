package controller

import (
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/namegen"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	rciam "github.com/nais/pgrator/internal/resourcecreator/iam"
	rcmonitoring "github.com/nais/pgrator/internal/resourcecreator/monitoring"
	rcnetpol "github.com/nais/pgrator/internal/resourcecreator/netpol"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	"github.com/nais/pgrator/pkg/api"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *PostgresReconciler) updateCNPG(obj *data_nais_io_v1.Postgres, preparedData PreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	pgClusterName, pgNamespace, err := getClusterNameAndNamespace(obj, api.EngineCNPG)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	var actions []action.Action

	ksaName := makeKsaName(obj)
	gsaName := makeGsaName(obj)

	storageBucketName := r.makeStorageBucketName(obj, pgClusterName)

	cluster, err := rccnpg.CreateClusterSpec(obj, r.Config, pgClusterName, pgNamespace, ksaName, gsaName, preparedData.TeamGoogleProjectID, storageBucketName)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	existingCluster := relatedObjects.GetMatching(cluster)
	if existingCluster != nil {
		actions = append(actions, action.Update(cluster, obj, cnpgClusterConditionGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(cluster, obj, cnpgClusterConditionGetter, r.Recorder))
	}

	if r.walStorageEnabled() {
		backup := rccnpg.CreateScheduledBackup(obj, pgClusterName, pgNamespace)
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

	iamActions, err := r.cnpgIAMActions(obj, ksaName, gsaName, preparedData, pgNamespace, storageBucketName, relatedObjects)
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

func makeGsaName(obj *data_nais_io_v1.Postgres) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-%s", obj.GetName()), validation.DNS1035LabelMaxLength)
}

func makeKsaName(obj *data_nais_io_v1.Postgres) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-sa-%s", obj.GetName()), validation.DNS1035LabelMaxLength)
}

func (r *PostgresReconciler) makeStorageBucketName(obj *data_nais_io_v1.Postgres, pgClusterName string) string {
	storageBucketName := ""
	if r.Config.CNPG.WalBucketPrefix != "" {
		storageBucketName = namegen.MustShortenName(fmt.Sprintf("%s-%s-%s", r.Config.CNPG.WalBucketPrefix, obj.GetNamespace(), pgClusterName), validation.DNS1035LabelMaxLength)
	}
	return storageBucketName
}

func (r *PostgresReconciler) walStorageEnabled() bool {
	return r.Config.CNPG.WalBucketPrefix != "" && r.Config.CNPG.WalBucketNamespace != ""
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

	ksaName := makeKsaName(obj)
	gsaName := makeGsaName(obj)
	storageBucketName := r.makeStorageBucketName(obj, pgClusterName)

	actions := make([]action.Action, 0, 4)

	cluster := rccnpg.MinimalCluster(obj, pgClusterName, pgNamespace)
	actions = append(actions, actionFunc(cluster, obj, cnpgClusterConditionGetter, r.Recorder))

	backup := rccnpg.MinimalScheduledBackup(obj, pgClusterName, pgNamespace)
	actions = append(actions, actionFunc(backup, obj, existsConditionGetter, r.Recorder))

	pooler := rccnpg.MinimalPooler(obj, pgClusterName, pgNamespace)
	actions = append(actions, actionFunc(pooler, obj, existsConditionGetter, r.Recorder))

	cnpgNetpol := rcnetpol.Minimal(obj, pgClusterName, pgNamespace)
	actions = append(actions, actionFunc(cnpgNetpol, obj, existsConditionGetter, r.Recorder))

	iamActions := r.deleteCnpgIAMActions(obj, preparedData, pgNamespace, storageBucketName, sharedActionFunc, relatedObjects, gsaName, ksaName)
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
func (r *PostgresReconciler) cnpgIAMActions(obj *data_nais_io_v1.Postgres, ksaName, gsaName string, preparedData PreparedData, pgNamespace, storageBucketName string, relatedObjects reconciler.RelatedObjects) ([]action.Action, error) {
	var actions []action.Action

	gsa := rciam.CreateIAMServiceAccount(gsaName, pgNamespace)
	existingGsa := relatedObjects.GetMatching(gsa)
	if existingGsa != nil {
		actions = append(actions, action.Update(gsa, obj, iamConditionGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(gsa, obj, iamConditionGetter, r.Recorder))
	}

	workloadIdentityPolicyName := makeWorkloadIdentityPolicyName(obj)

	workloadIdentityPolicy := rciam.CreateWorkloadIdentityPolicyMember(workloadIdentityPolicyName, obj.GetNamespace(), pgNamespace, r.Config.GoogleProjectID, gsaName, ksaName)
	existingWorkloadIdentityPolicy := relatedObjects.GetMatching(workloadIdentityPolicy)
	if existingWorkloadIdentityPolicy == nil {
		actions = append(actions, action.Create(workloadIdentityPolicy, obj, iamConditionGetter, r.Recorder))
	} else if iamPolicyHasChanges(workloadIdentityPolicy, existingWorkloadIdentityPolicy.(*iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember)) {
		if r.Config.ResyncIAMPermissions {
			actions = append(actions, action.Recreate(workloadIdentityPolicy, obj, iamConditionGetter, r.Recorder))
		} else {
			return nil, fmt.Errorf("want to change IAMPolicyMember %s, but configuration does not allow recreate", client.ObjectKeyFromObject(workloadIdentityPolicy))
		}
	} else {
		actions = append(actions, action.Claim(workloadIdentityPolicy, obj, iamConditionGetter, r.Recorder))
	}

	if r.walStorageEnabled() {
		storageBucketPolicyName := makeStorageBucketPolicyName(obj)

		storageBucketPolicy := rciam.CreateStorageBucketPolicyMember(storageBucketPolicyName, r.Config.CNPG.WalBucketNamespace, preparedData.TeamGoogleProjectID, gsaName, storageBucketName)
		existingStorageBucketPolicy := relatedObjects.GetMatching(storageBucketPolicy)
		if existingStorageBucketPolicy == nil {
			actions = append(actions, action.Create(storageBucketPolicy, obj, iamConditionGetter, r.Recorder))
		} else if iamPolicyHasChanges(storageBucketPolicy, existingStorageBucketPolicy.(*iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember)) {
			if r.Config.ResyncIAMPermissions {
				actions = append(actions, action.Recreate(storageBucketPolicy, obj, iamConditionGetter, r.Recorder))
			} else {
				return nil, fmt.Errorf("want to change IAMPolicyMember %s, but configuration does not allow recreate", client.ObjectKeyFromObject(storageBucketPolicy))
			}
		} else {
			actions = append(actions, action.Claim(storageBucketPolicy, obj, iamConditionGetter, r.Recorder))
		}
	}

	return actions, nil
}

func makeStorageBucketPolicyName(obj *data_nais_io_v1.Postgres) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-wal-%s", obj.GetName()), validation.DNS1035LabelMaxLength)
}

func makeWorkloadIdentityPolicyName(obj *data_nais_io_v1.Postgres) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-wi-user-%s", obj.GetName()), validation.DNS1035LabelMaxLength)
}

// deleteCnpgIAMActions returns actions to clean up shared IAM resources during deletion.
func (r *PostgresReconciler) deleteCnpgIAMActions(obj *data_nais_io_v1.Postgres, preparedData PreparedData, pgNamespace, storageBucketName string, sharedActionFunc actionFunc, relatedObjects reconciler.RelatedObjects, gsaName, ksaName string) []action.Action {
	var actions []action.Action

	workloadIdentityPolicyName := makeWorkloadIdentityPolicyName(obj)
	storageBucketPolicyName := makeStorageBucketPolicyName(obj)

	gsa := rciam.CreateIAMServiceAccount(gsaName, pgNamespace)
	if existing := relatedObjects.GetMatching(gsa); existing != nil {
		actions = append(actions, sharedActionFunc(existing, obj, iamConditionGetter, r.Recorder))
	}

	workloadIdentityPolicy := rciam.CreateWorkloadIdentityPolicyMember(workloadIdentityPolicyName, obj.GetNamespace(), pgNamespace, r.Config.GoogleProjectID, gsaName, ksaName)
	if existing := relatedObjects.GetMatching(workloadIdentityPolicy); existing != nil {
		actions = append(actions, sharedActionFunc(existing, obj, iamConditionGetter, r.Recorder))
	}

	if r.walStorageEnabled() {
		storageBucketPolicy := rciam.CreateStorageBucketPolicyMember(storageBucketPolicyName, r.Config.CNPG.WalBucketNamespace, preparedData.TeamGoogleProjectID, gsaName, storageBucketName)
		if existing := relatedObjects.GetMatching(storageBucketPolicy); existing != nil {
			actions = append(actions, sharedActionFunc(existing, obj, iamConditionGetter, r.Recorder))
		}
	}

	return actions
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
