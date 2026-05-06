package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/controller/resourcecreator"
	"github.com/nais/pgrator/internal/namegen"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/v1beta1"
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	monitoring_v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	core_v1 "k8s.io/api/core/v1"
	networking_v1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Max length is 63, but we need to save space for suffixes added by Zalando operator or StatefulSets
	maxClusterNameLength        = 50
	GSAName                     = "postgres-pod"
	KSAName                     = "postgres-pod"
	RoleBindingName             = "postgres-pod-additional"
	ClusterRoleName             = "postgres-pod-additional"
	ServiceAccountsNamespace    = "serviceaccounts"
	ProjectIDLabel              = "google-cloud-project"
	ProjectIDAnnotationFallback = "cnrm.cloud.google.com/project-id"
)

// PostgresReconciler reconciles a Postgres object
type PostgresReconciler struct {
	Config   *config.Config
	Recorder events.Recorder
}

func IAMPolicyMemberNames(teamNamespace string) (string, string, string) {
	workloadIdentityPolicyName := GSAName + "-wi-user"
	logsWriterPolicyName := GSAName + "-logs-writer"
	storageBucketPolicyName, err := namegen.ShortName("pg-gcs-"+teamNamespace, 63)
	if err != nil {
		panic(err)
	}

	return workloadIdentityPolicyName, storageBucketPolicyName, logsWriterPolicyName
}

var _ reconciler.Reconciler[*data_nais_io_v1.Postgres, PreparedData] = &PostgresReconciler{}
var _ reconciler.MetricsLabeler[*data_nais_io_v1.Postgres] = &PostgresReconciler{}

type PreparedData struct {
	teamGoogleProjectID string
}

func (r *PostgresReconciler) Name() string {
	return "postgres.data.nais.io"
}

func (r *PostgresReconciler) MetricsLabels(obj *data_nais_io_v1.Postgres) map[string]string {
	ha := "false"
	if obj.Spec.Cluster.HighAvailability {
		ha = "true"
	}
	return map[string]string{
		"major_version":     obj.Spec.Cluster.MajorVersion,
		"high_availability": ha,
	}
}

func (r *PostgresReconciler) New() *data_nais_io_v1.Postgres {
	return &data_nais_io_v1.Postgres{}
}

func (r *PostgresReconciler) Prepare(ctx context.Context, reader client.Reader, obj *data_nais_io_v1.Postgres) (PreparedData, ctrl.Result, error) {
	teamNamespace := &core_v1.Namespace{}
	err := reader.Get(ctx, client.ObjectKey{Name: obj.Namespace}, teamNamespace)
	if err != nil {
		return PreparedData{}, ctrl.Result{}, fmt.Errorf("failed to get namespace %q: %w", obj.Namespace, err)
	}

	var projectID string
	var ok bool

	// Try to get project ID from label first
	if teamNamespace.Labels != nil {
		projectID, ok = teamNamespace.Labels[ProjectIDLabel]
	}

	// If not found in labels, try annotation fallback
	if !ok && teamNamespace.Annotations != nil {
		projectID, ok = teamNamespace.Annotations[ProjectIDAnnotationFallback]
	}

	if !ok || projectID == "" {
		return PreparedData{}, ctrl.Result{}, fmt.Errorf("namespace %q has no %q label or %q annotation", obj.Namespace, ProjectIDLabel, ProjectIDAnnotationFallback)
	}

	p := PreparedData{
		teamGoogleProjectID: projectID,
	}

	return p, ctrl.Result{}, nil
}

func (r *PostgresReconciler) OwnedTypes() []reconciler.OwnedType {
	return nil
}

func (r *PostgresReconciler) AdditionalTypes() []client.Object {
	objects := []client.Object{
		&acid_zalan_do_v1.Postgresql{},
		&networking_v1.NetworkPolicy{},
		&iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{},
		&iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount{},
		&core_v1.ServiceAccount{},
		&rbacv1.RoleBinding{},
	}
	if !r.Config.PrometheusRulesDisabled {
		objects = append(objects, &monitoring_v1.PrometheusRule{})
	}
	return objects
}

func (r *PostgresReconciler) Update(obj *data_nais_io_v1.Postgres, preparedData PreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	var err error
	pgClusterName, pgNamespace, err := getClusterNameAndNamespace(obj)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	var actions []action.Action
	cluster := resourcecreator.CreateClusterSpec(obj, r.Config, pgClusterName, pgNamespace)
	existingCluster := relatedObjects.GetMatching(cluster)
	if existingCluster != nil {
		actions = append(actions, action.Update(cluster, obj, postgresqlConditionGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(cluster, obj, postgresqlConditionGetter, r.Recorder))
	}

	netpol := resourcecreator.CreatePostgresNetworkPolicySpec(obj, pgClusterName, pgNamespace)
	actions = append(actions, action.CreateOrUpdate(netpol, obj, existsConditionGetter, r.Recorder))

	workloadIdentityPolicyName, storageBucketPolicyName, logsWriterPolicyName := IAMPolicyMemberNames(obj.GetNamespace())

	workloadIdentityPolicy := resourcecreator.CreateWorkloadIdentityIAMPolicyMember(workloadIdentityPolicyName, obj.GetNamespace(), pgNamespace, r.Config.GoogleProjectID, GSAName, KSAName)
	existingWorkloadIdentityPolicy := relatedObjects.GetMatching(workloadIdentityPolicy)
	if existingWorkloadIdentityPolicy == nil {
		actions = append(actions, action.Create(workloadIdentityPolicy, obj, iamConditionGetter, r.Recorder))
	} else if iamPolicyHasChanges(workloadIdentityPolicy, existingWorkloadIdentityPolicy.(*iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember)) {
		if r.Config.ResyncIAMPermissions {
			actions = append(actions, action.Recreate(workloadIdentityPolicy, obj, iamConditionGetter, r.Recorder))
		} else {
			return nil, ctrl.Result{}, fmt.Errorf("want to change IAMPolicyMember %s, but configuration does not allow recreate", client.ObjectKeyFromObject(workloadIdentityPolicy))
		}
	} else {
		actions = append(actions, action.Claim(workloadIdentityPolicy, obj, iamConditionGetter, r.Recorder))
	}

	if r.Config.WalGsBucket != "" {
		storageBucketPolicy := resourcecreator.CreateStorageBucketIAMPolicyMember(storageBucketPolicyName, ServiceAccountsNamespace, preparedData.teamGoogleProjectID, GSAName, r.Config.WalGsBucket)
		existingStorageBucketPolicy := relatedObjects.GetMatching(storageBucketPolicy)
		if existingStorageBucketPolicy == nil {
			actions = append(actions, action.Create(storageBucketPolicy, obj, iamConditionGetter, r.Recorder))
		} else if iamPolicyHasChanges(storageBucketPolicy, existingStorageBucketPolicy.(*iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember)) {
			if r.Config.ResyncIAMPermissions {
				actions = append(actions, action.Recreate(storageBucketPolicy, obj, iamConditionGetter, r.Recorder))
			} else {
				return nil, ctrl.Result{}, fmt.Errorf("want to change IAMPolicyMember %s, but configuration does not allow recreate", client.ObjectKeyFromObject(storageBucketPolicy))
			}
		} else {
			actions = append(actions, action.Claim(storageBucketPolicy, obj, iamConditionGetter, r.Recorder))
		}
	}

	logsWriterPolicy := resourcecreator.CreateLogsWriterIAMPolicyMember(logsWriterPolicyName, obj.GetNamespace(), preparedData.teamGoogleProjectID, GSAName)
	existingLogsWriterPolicy := relatedObjects.GetMatching(logsWriterPolicy)
	if existingLogsWriterPolicy == nil {
		actions = append(actions, action.Create(logsWriterPolicy, obj, iamConditionGetter, r.Recorder))
	} else if iamPolicyHasChanges(logsWriterPolicy, existingLogsWriterPolicy.(*iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember)) {
		if r.Config.ResyncIAMPermissions {
			actions = append(actions, action.Recreate(logsWriterPolicy, obj, iamConditionGetter, r.Recorder))
		} else {
			return nil, ctrl.Result{}, fmt.Errorf("want to change IAMPolicyMember %s, but configuration does not allow recreate", client.ObjectKeyFromObject(logsWriterPolicy))
		}
	} else {
		actions = append(actions, action.Claim(logsWriterPolicy, obj, iamConditionGetter, r.Recorder))
	}

	gsa := resourcecreator.CreateIAMServiceAccount(GSAName, obj.GetNamespace())
	existingGsa := relatedObjects.GetMatching(gsa)
	if existingGsa != nil {
		actions = append(actions, action.Claim(gsa, obj, iamConditionGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(gsa, obj, iamConditionGetter, r.Recorder))
	}

	kubernetesSA := resourcecreator.CreateKubernetesServiceAccount(KSAName, pgNamespace, preparedData.teamGoogleProjectID, GSAName)
	existingKubernetesSA := relatedObjects.GetMatching(kubernetesSA)
	if existingKubernetesSA != nil {
		actions = append(actions, action.Update(kubernetesSA, obj, existsConditionGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(kubernetesSA, obj, existsConditionGetter, r.Recorder))
	}

	postgresPodRoleBinding := resourcecreator.CreateRoleBinding(RoleBindingName, KSAName, ClusterRoleName, pgNamespace)
	existingPRB := relatedObjects.GetMatching(postgresPodRoleBinding)
	if existingPRB != nil {
		actions = append(actions, action.Update(postgresPodRoleBinding, obj, existsConditionGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(postgresPodRoleBinding, obj, existsConditionGetter, r.Recorder))
	}

	if !r.Config.PrometheusRulesDisabled {
		prometheusRule := resourcecreator.CreatePrometheusRuleSpec(obj, pgClusterName, pgNamespace)
		actions = append(actions, action.CreateOrUpdate(prometheusRule, obj, existsConditionGetter, r.Recorder))
	}

	return actions, ctrl.Result{}, nil
}

func iamConditionGetter(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition {
	var iamConditions []meta_v1.Condition
	switch o := obj.(type) {
	case *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember:
		iamConditions = o.Status.Conditions
	case *iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount:
		iamConditions = o.Status.Conditions
	default:
		panic(fmt.Sprintf("unsupported type for groupkind: %s (%T)", typePrefix(obj, scheme), o))
	}

	var statusCondition meta_v1.Condition
	if len(iamConditions) > 0 {
		statusCondition = iamConditions[0]
	} else {
		statusCondition = meta_v1.Condition{
			Status:  meta_v1.ConditionUnknown,
			Reason:  "Unknown",
			Message: "No status available on source resource",
		}
	}

	type conditionConfig struct {
		Type   string
		Status bool
	}
	conditions := []conditionConfig{
		{
			Type:   "Available",
			Status: statusCondition.Status == meta_v1.ConditionTrue && slices.Contains([]string{"UpToDate", "Updating"}, statusCondition.Reason),
		},
		{
			Type:   "Progressing",
			Status: slices.Contains([]string{"Creating", "Updating", "Deleting"}, statusCondition.Reason),
		},
		{
			Type:   "Degraded",
			Status: strings.Contains(statusCondition.Reason, "Failed"),
		},
	}

	result := make([]meta_v1.Condition, 0, len(conditions))
	for _, condition := range conditions {
		result = append(result, meta_v1.Condition{
			Type:               fmt.Sprintf("%s.%s/%s", typePrefix(obj, scheme), obj.GetName(), condition.Type),
			Status:             makeCondition(condition.Status),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             statusCondition.Reason,
			Message:            statusCondition.Message,
		})
	}

	return result
}

func postgresqlConditionGetter(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition {
	pg := obj.(*acid_zalan_do_v1.Postgresql)

	type conditionConfig struct {
		Type   string
		Status bool
	}
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

func (r *PostgresReconciler) Delete(obj *data_nais_io_v1.Postgres, preparedData PreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	actionFunc := action.DeleteIfExists
	sharedActionFunc := action.Unclaim
	if !obj.Spec.Cluster.AllowDeletion {
		actionFunc = action.NoOp
		sharedActionFunc = action.NoOp
	}

	var err error
	pgClusterName, pgNamespace, err := getClusterNameAndNamespace(obj)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	var actions []action.Action

	cluster := resourcecreator.MinimalCluster(obj, pgClusterName, pgNamespace)
	actions = append(actions, actionFunc(cluster, obj, postgresqlConditionGetter, r.Recorder))

	netpol := resourcecreator.MinimalNetpol(obj, pgClusterName, pgNamespace)
	actions = append(actions, actionFunc(netpol, obj, existsConditionGetter, r.Recorder))

	workloadIdentityPolicyName, storageBucketPolicyName, logsWriterPolicyName := IAMPolicyMemberNames(obj.GetNamespace())

	workloadIdentityPolicy := resourcecreator.CreateWorkloadIdentityIAMPolicyMember(workloadIdentityPolicyName, obj.GetNamespace(), pgNamespace, r.Config.GoogleProjectID, GSAName, KSAName)
	existingWorkloadIdentityPolicy := relatedObjects.GetMatching(workloadIdentityPolicy)
	if existingWorkloadIdentityPolicy != nil {
		actions = append(actions, sharedActionFunc(existingWorkloadIdentityPolicy, obj, iamConditionGetter, r.Recorder))
	}

	if r.Config.WalGsBucket != "" {
		storageBucketPolicy := resourcecreator.CreateStorageBucketIAMPolicyMember(storageBucketPolicyName, ServiceAccountsNamespace, preparedData.teamGoogleProjectID, GSAName, r.Config.WalGsBucket)
		existingStorageBucketPolicy := relatedObjects.GetMatching(storageBucketPolicy)
		if existingStorageBucketPolicy != nil {
			actions = append(actions, sharedActionFunc(existingStorageBucketPolicy, obj, iamConditionGetter, r.Recorder))
		}
	}

	logsWriterPolicy := resourcecreator.CreateLogsWriterIAMPolicyMember(logsWriterPolicyName, obj.GetNamespace(), preparedData.teamGoogleProjectID, GSAName)
	existingLogsWriterPolicy := relatedObjects.GetMatching(logsWriterPolicy)
	if existingLogsWriterPolicy != nil {
		actions = append(actions, sharedActionFunc(existingLogsWriterPolicy, obj, iamConditionGetter, r.Recorder))
	}

	kubernetesSA := resourcecreator.CreateKubernetesServiceAccount(KSAName, pgNamespace, preparedData.teamGoogleProjectID, GSAName)
	existingKubernetesSA := relatedObjects.GetMatching(kubernetesSA)
	if existingKubernetesSA != nil {
		actions = append(actions, sharedActionFunc(kubernetesSA, obj, existsConditionGetter, r.Recorder))
	}

	if !r.Config.PrometheusRulesDisabled {
		prometheusRule := resourcecreator.MinimalPrometheusRule(obj, pgClusterName)
		actions = append(actions, actionFunc(prometheusRule, obj, existsConditionGetter, r.Recorder))
	}

	return actions, ctrl.Result{}, nil
}

func getClusterNameAndNamespace(obj *data_nais_io_v1.Postgres) (string, string, error) {
	var err error
	pgClusterName := obj.GetName()
	if len(pgClusterName) > maxClusterNameLength {
		pgClusterName, err = namegen.ShortName(pgClusterName, maxClusterNameLength)
		if err != nil {
			return "", "", fmt.Errorf("failed to shorten PostgreSQL cluster name: %w", err)
		}
	}
	pgNamespace := fmt.Sprintf("pg-%s", obj.GetNamespace())
	return pgClusterName, pgNamespace, nil
}

func iamPolicyHasChanges(a, b *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember) bool {
	if a == b {
		return false
	}
	if a == nil || b == nil {
		return true
	}
	if a.Spec.Member != b.Spec.Member ||
		a.Spec.Role != b.Spec.Role {
		return true
	}
	if a.Spec.ResourceRef.Kind != b.Spec.ResourceRef.Kind ||
		a.Spec.ResourceRef.Name != b.Spec.ResourceRef.Name ||
		a.Spec.ResourceRef.Namespace != b.Spec.ResourceRef.Namespace {
		return true
	}
	if strPtrDiffers(a.Spec.ResourceRef.APIVersion, b.Spec.ResourceRef.APIVersion) {
		return true
	}

	return strPtrDiffers(a.Spec.ResourceRef.External, b.Spec.ResourceRef.External)
}

func strPtrDiffers(a *string, b *string) bool {
	if a == nil && b == nil {
		return false
	}
	if a == nil || b == nil {
		return true
	}
	return *a != *b
}
