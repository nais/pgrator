package controller

import (
	"fmt"

	rciam "github.com/nais/pgrator/internal/resourcecreator/iam"
	rcmonitoring "github.com/nais/pgrator/internal/resourcecreator/monitoring"
	rcnetpol "github.com/nais/pgrator/internal/resourcecreator/netpol"
	rczalando "github.com/nais/pgrator/internal/resourcecreator/zalando"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	"github.com/nais/pgrator/pkg/api"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *PostgresReconciler) updateZalando(obj *data_nais_io_v1.Postgres, preparedData PreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	pgClusterName, pgNamespace, err := getClusterNameAndNamespace(obj, api.EngineZalando)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	var actions []action.Action
	cluster, err := rczalando.CreateClusterSpec(obj, r.Config, pgClusterName, pgNamespace)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	existingCluster := relatedObjects.GetMatching(cluster)
	if existingCluster != nil {
		actions = append(actions, action.Update(cluster, obj, postgresqlConditionGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(cluster, obj, postgresqlConditionGetter, r.Recorder))
	}

	zalandoNetpol := rcnetpol.CreateZalando(obj, pgClusterName, pgNamespace)
	actions = append(actions, action.CreateOrUpdate(zalandoNetpol, obj, existsConditionGetter, r.Recorder))

	iamActions, err := r.zalandoIAMActions(obj, preparedData, pgNamespace, r.Config.WalGsBucket, relatedObjects)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	actions = append(actions, iamActions...)

	if !r.Config.PrometheusRulesDisabled {
		prometheusRule := rcmonitoring.CreateZalandoPrometheusRule(obj, pgClusterName, pgNamespace)
		actions = append(actions, action.CreateOrUpdate(prometheusRule, obj, existsConditionGetter, r.Recorder))
	}

	return actions, ctrl.Result{}, nil
}

func (r *PostgresReconciler) deleteZalando(obj *data_nais_io_v1.Postgres, preparedData PreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	actionFunc := action.DeleteIfExists
	sharedActionFunc := action.Unclaim
	if !obj.Spec.Cluster.AllowDeletion {
		actionFunc = action.NoOp
		sharedActionFunc = action.NoOp
	}

	pgClusterName, pgNamespace, err := getClusterNameAndNamespace(obj, api.EngineZalando)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	var actions []action.Action

	cluster := rczalando.MinimalCluster(obj, pgClusterName, pgNamespace)
	actions = append(actions, actionFunc(cluster, obj, postgresqlConditionGetter, r.Recorder))

	zalandoNetpol := rcnetpol.Minimal(obj, pgClusterName, pgNamespace)
	actions = append(actions, actionFunc(zalandoNetpol, obj, existsConditionGetter, r.Recorder))

	iamActions := r.deleteZalandoIAMActions(obj, preparedData, pgNamespace, r.Config.WalGsBucket, sharedActionFunc, relatedObjects)
	actions = append(actions, iamActions...)

	if !r.Config.PrometheusRulesDisabled {
		prometheusRule := rcmonitoring.MinimalPrometheusRule(obj, pgClusterName)
		actions = append(actions, actionFunc(prometheusRule, obj, existsConditionGetter, r.Recorder))
	}

	return actions, ctrl.Result{}, nil
}

// zalandoIAMActions returns the IAM actions used by Zalando
func (r *PostgresReconciler) zalandoIAMActions(obj *data_nais_io_v1.Postgres, preparedData PreparedData, pgNamespace, backupBucket string, relatedObjects reconciler.RelatedObjects) ([]action.Action, error) {
	var actions []action.Action

	workloadIdentityPolicyName, storageBucketPolicyName, logsWriterPolicyName := IAMPolicyMemberNames(obj.GetNamespace())

	workloadIdentityPolicy := rciam.CreateWorkloadIdentityPolicyMember(workloadIdentityPolicyName, obj.GetNamespace(), pgNamespace, r.Config.GoogleProjectID, GSAName, KSAName)
	existingWorkloadIdentityPolicy := relatedObjects.GetMatching(workloadIdentityPolicy)
	if existingWorkloadIdentityPolicy == nil {
		actions = append(actions, action.Create(workloadIdentityPolicy, obj, cnrmConditionsGetter, r.Recorder))
	} else if iamPolicyHasChanges(workloadIdentityPolicy, existingWorkloadIdentityPolicy.(*iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember)) {
		if r.Config.ResyncIAMPermissions {
			actions = append(actions, action.Recreate(workloadIdentityPolicy, obj, cnrmConditionsGetter, r.Recorder))
		} else {
			return nil, fmt.Errorf("want to change IAMPolicyMember %s, but configuration does not allow recreate", client.ObjectKeyFromObject(workloadIdentityPolicy))
		}
	} else {
		actions = append(actions, action.Claim(workloadIdentityPolicy, obj, cnrmConditionsGetter, r.Recorder))
	}

	if backupBucket != "" {
		storageBucketPolicy := rciam.CreateStorageBucketPolicyMember(storageBucketPolicyName, ServiceAccountsNamespace, preparedData.TeamGoogleProjectID, GSAName, backupBucket, rciam.StorageBucketRole)
		existingStorageBucketPolicy := relatedObjects.GetMatching(storageBucketPolicy)
		if existingStorageBucketPolicy == nil {
			actions = append(actions, action.Create(storageBucketPolicy, obj, cnrmConditionsGetter, r.Recorder))
		} else if iamPolicyHasChanges(storageBucketPolicy, existingStorageBucketPolicy.(*iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember)) {
			if r.Config.ResyncIAMPermissions {
				actions = append(actions, action.Recreate(storageBucketPolicy, obj, cnrmConditionsGetter, r.Recorder))
			} else {
				return nil, fmt.Errorf("want to change IAMPolicyMember %s, but configuration does not allow recreate", client.ObjectKeyFromObject(storageBucketPolicy))
			}
		} else {
			actions = append(actions, action.Claim(storageBucketPolicy, obj, cnrmConditionsGetter, r.Recorder))
		}
	}

	logsWriterPolicy := rciam.CreateLogsWriterPolicyMember(logsWriterPolicyName, obj.GetNamespace(), preparedData.TeamGoogleProjectID, GSAName)
	existingLogsWriterPolicy := relatedObjects.GetMatching(logsWriterPolicy)
	if existingLogsWriterPolicy == nil {
		actions = append(actions, action.Create(logsWriterPolicy, obj, cnrmConditionsGetter, r.Recorder))
	} else if iamPolicyHasChanges(logsWriterPolicy, existingLogsWriterPolicy.(*iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember)) {
		if r.Config.ResyncIAMPermissions {
			actions = append(actions, action.Recreate(logsWriterPolicy, obj, cnrmConditionsGetter, r.Recorder))
		} else {
			return nil, fmt.Errorf("want to change IAMPolicyMember %s, but configuration does not allow recreate", client.ObjectKeyFromObject(logsWriterPolicy))
		}
	} else {
		actions = append(actions, action.Claim(logsWriterPolicy, obj, cnrmConditionsGetter, r.Recorder))
	}

	gsa := rciam.CreateIAMServiceAccount(GSAName, obj.GetNamespace())
	existingGsa := relatedObjects.GetMatching(gsa)
	if existingGsa != nil {
		actions = append(actions, action.Claim(gsa, obj, cnrmConditionsGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(gsa, obj, cnrmConditionsGetter, r.Recorder))
	}

	kubernetesSA := rciam.CreateKubernetesServiceAccount(KSAName, pgNamespace, preparedData.TeamGoogleProjectID, GSAName)
	existingKubernetesSA := relatedObjects.GetMatching(kubernetesSA)
	if existingKubernetesSA != nil {
		actions = append(actions, action.Update(kubernetesSA, obj, existsConditionGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(kubernetesSA, obj, existsConditionGetter, r.Recorder))
	}

	postgresPodRoleBinding := rciam.CreateRoleBinding(RoleBindingName, KSAName, ClusterRoleName, pgNamespace)
	existingPRB := relatedObjects.GetMatching(postgresPodRoleBinding)
	if existingPRB != nil {
		actions = append(actions, action.Update(postgresPodRoleBinding, obj, existsConditionGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(postgresPodRoleBinding, obj, existsConditionGetter, r.Recorder))
	}

	return actions, nil
}

// deleteZalandoIAMActions returns actions to clean up shared IAM resources during deletion.
func (r *PostgresReconciler) deleteZalandoIAMActions(obj *data_nais_io_v1.Postgres, preparedData PreparedData, pgNamespace, bucket string, sharedActionFunc actionFunc, relatedObjects reconciler.RelatedObjects) []action.Action {
	var actions []action.Action

	workloadIdentityPolicyName, storageBucketPolicyName, logsWriterPolicyName := IAMPolicyMemberNames(obj.GetNamespace())

	workloadIdentityPolicy := rciam.CreateWorkloadIdentityPolicyMember(workloadIdentityPolicyName, obj.GetNamespace(), pgNamespace, r.Config.GoogleProjectID, GSAName, KSAName)
	if existing := relatedObjects.GetMatching(workloadIdentityPolicy); existing != nil {
		actions = append(actions, sharedActionFunc(existing, obj, cnrmConditionsGetter, r.Recorder))
	}

	if bucket != "" {
		storageBucketPolicy := rciam.CreateStorageBucketPolicyMember(storageBucketPolicyName, ServiceAccountsNamespace, preparedData.TeamGoogleProjectID, GSAName, bucket, rciam.StorageBucketRole)
		if existing := relatedObjects.GetMatching(storageBucketPolicy); existing != nil {
			actions = append(actions, sharedActionFunc(existing, obj, cnrmConditionsGetter, r.Recorder))
		}
	}

	logsWriterPolicy := rciam.CreateLogsWriterPolicyMember(logsWriterPolicyName, obj.GetNamespace(), preparedData.TeamGoogleProjectID, GSAName)
	if existing := relatedObjects.GetMatching(logsWriterPolicy); existing != nil {
		actions = append(actions, sharedActionFunc(existing, obj, cnrmConditionsGetter, r.Recorder))
	}

	kubernetesSA := rciam.CreateKubernetesServiceAccount(KSAName, pgNamespace, preparedData.TeamGoogleProjectID, GSAName)
	if existing := relatedObjects.GetMatching(kubernetesSA); existing != nil {
		actions = append(actions, sharedActionFunc(existing, obj, existsConditionGetter, r.Recorder))
	}

	return actions
}

func postgresqlConditionGetter(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition {
	pg := obj.(*acid_zalan_do_v1.Postgresql)

	conditions := []conditionConfig{
		{
			Type:   "Available",
			Status: pg.Status.PostgresClusterStatus == acid_zalan_do_v1.ClusterStatusRunning || pg.Status.PostgresClusterStatus == acid_zalan_do_v1.ClusterStatusUpdating,
		},
		{
			Type:   "Progressing",
			Status: pg.Status.PostgresClusterStatus == acid_zalan_do_v1.ClusterStatusCreating || pg.Status.PostgresClusterStatus == acid_zalan_do_v1.ClusterStatusUpdating,
		},
		{
			Type:   "Degraded",
			Status: !pg.Status.Success(),
		},
	}

	statusReason := pg.Status.String()
	if statusReason == "" {
		statusReason = "Unknown"
	}

	result := make([]meta_v1.Condition, 0, len(conditions))
	for _, condition := range conditions {
		result = append(result, meta_v1.Condition{
			Type:               fmt.Sprintf("%s/%s", typePrefix(obj, scheme), condition.Type),
			Status:             makeCondition(condition.Status),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             statusReason,
		})
	}

	return result
}
