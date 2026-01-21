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
	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/pkg/api/thirdparty/google/v1beta1"
	monitoring_v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	core_v1 "k8s.io/api/core/v1"
	networking_v1 "k8s.io/api/networking/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Max length is 63, but we need to save space for suffixes added by Zalando operator or StatefulSets
	maxClusterNameLength = 50
)

// PostgresReconciler reconciles a Postgres object
type PostgresReconciler struct {
	Config   *config.Config
	Recorder events.Recorder
}

var _ reconciler.Reconciler[*data_nais_io_v1.Postgres, PreparedData] = &PostgresReconciler{}

const ProjectIDLabel = "google-cloud-project"

type PreparedData struct {
	teamGoogleProjectID string
}

func (r *PostgresReconciler) Name() string {
	return "postgres.data.nais.io"
}

func (r *PostgresReconciler) New() *data_nais_io_v1.Postgres {
	return &data_nais_io_v1.Postgres{}
}

func (r *PostgresReconciler) Prepare(ctx context.Context, reader client.Reader, obj *data_nais_io_v1.Postgres) (PreparedData, ctrl.Result, error) {
	namespace := &core_v1.Namespace{}
	err := reader.Get(ctx, client.ObjectKey{Name: obj.Namespace}, namespace)
	if err != nil {
		return PreparedData{}, ctrl.Result{}, fmt.Errorf("failed to get namespace %q: %w", obj.Namespace, err)
	}

	if namespace.Labels == nil {
		return PreparedData{}, ctrl.Result{}, fmt.Errorf("namespace %q has no labels", obj.Namespace)
	}

	if projectID, ok := namespace.Labels[ProjectIDLabel]; !ok {
		return PreparedData{}, ctrl.Result{}, fmt.Errorf("namespace %q has no %q label", obj.Namespace, ProjectIDLabel)
	} else {
		return PreparedData{teamGoogleProjectID: projectID}, ctrl.Result{}, nil
	}
}

func (r *PostgresReconciler) OwnedTypes() []client.Object {
	return nil
}

func (r *PostgresReconciler) AdditionalTypes() []client.Object {
	objects := []client.Object{
		&acid_zalan_do_v1.Postgresql{},
		&networking_v1.NetworkPolicy{},
		&iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{},
		&iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount{},
		&core_v1.ServiceAccount{},
	}
	if !r.Config.PrometheusRulesDisabled {
		objects = append(objects, &monitoring_v1.PrometheusRule{})
	}
	return objects
}

func (r *PostgresReconciler) Update(obj *data_nais_io_v1.Postgres, preparedData PreparedData) ([]action.Action, ctrl.Result, error) {
	var err error
	pgClusterName, pgNamespace, err := getClusterNameAndNamespace(obj)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	ownerAnnotationKey := fmt.Sprintf("%s/owner", r.Name())

	ns := obj.GetNamespace()
	// cluster-scoped resources cannot have an empty namespace in the owner annotation
	if ns == "" {
		ns = "_"
	}
	ownerAnnotationValue := fmt.Sprintf("%s/%s", ns, obj.GetName())

	var actions []action.Action
	cluster := resourcecreator.CreateClusterSpec(obj, r.Config, pgClusterName, pgNamespace)
	meta_v1.SetMetaDataAnnotation(&cluster.ObjectMeta, ownerAnnotationKey, ownerAnnotationValue)
	actions = append(actions, action.CreateOrUpdate(cluster, obj, postgresqlConditionGetter, r.Recorder))

	netpol := resourcecreator.CreatePostgresNetworkPolicySpec(obj, pgClusterName, pgNamespace)
	meta_v1.SetMetaDataAnnotation(&netpol.ObjectMeta, ownerAnnotationKey, ownerAnnotationValue)
	actions = append(actions, action.CreateOrUpdate(netpol, obj, existsConditionGetter, r.Recorder))

	iampm := resourcecreator.CreateWorkloadIdentityIAMPolicyMember(obj.GetNamespace(), preparedData.teamGoogleProjectID)
	actions = append(actions, action.CreateIfNotExists(iampm, obj, iamConditionGetter, r.Recorder))

	if r.Config.WalGsBucket != "" {
		storageBucketIAM := resourcecreator.CreateStorageBucketIAMPolicyMember(preparedData.teamGoogleProjectID, r.Config.WalGsBucket)
		actions = append(actions, action.CreateIfNotExists(storageBucketIAM, obj, iamConditionGetter, r.Recorder))
	}

	gsa := resourcecreator.CreateIAMServiceAccount(obj)
	actions = append(actions, action.CreateIfNotExists(gsa, obj, iamConditionGetter, r.Recorder))

	ksa := resourcecreator.CreateKubernetesServiceAccount(obj, pgNamespace, preparedData.teamGoogleProjectID)
	actions = append(actions, action.CreateIfNotExists(ksa, obj, existsConditionGetter, r.Recorder))

	if !r.Config.PrometheusRulesDisabled {
		prometheusRule := resourcecreator.CreatePrometheusRuleSpec(obj, pgClusterName, pgNamespace)
		meta_v1.SetMetaDataAnnotation(&prometheusRule.ObjectMeta, ownerAnnotationKey, ownerAnnotationValue)
		actions = append(actions, action.CreateOrUpdate(prometheusRule, obj, existsConditionGetter, r.Recorder))
	}

	return actions, ctrl.Result{}, nil
}

func iamConditionGetter(obj client.Object) []meta_v1.Condition {
	typePrefix := strings.ToLower(obj.GetObjectKind().GroupVersionKind().GroupKind().String())

	var iamConditions []meta_v1.Condition
	switch o := obj.(type) {
	case *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember:
		iamConditions = o.Status.Conditions
	case *iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount:
		iamConditions = o.Status.Conditions
	default:
		panic(fmt.Sprintf("unsupported type for groupkind: %s (%T)", typePrefix, o))
	}

	var statusCondition meta_v1.Condition
	if len(iamConditions) > 0 {
		statusCondition = iamConditions[0]
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
		t := fmt.Sprintf("%s.%s/%s", typePrefix, obj.GetName(), condition.Type)
		result = append(result, meta_v1.Condition{
			Type:               t,
			Status:             makeCondition(condition.Status),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             statusCondition.Reason,
			Message:            statusCondition.Message,
		})
	}

	return result
}

func makeCondition(value bool) meta_v1.ConditionStatus {
	if value {
		return meta_v1.ConditionTrue
	} else {
		return meta_v1.ConditionFalse
	}
}

func existsConditionGetter(obj client.Object) []meta_v1.Condition {
	typePrefix := strings.ToLower(obj.GetObjectKind().GroupVersionKind().GroupKind().String())
	return []meta_v1.Condition{
		{
			Type:               fmt.Sprintf("%s/Available", typePrefix),
			Status:             makeCondition(obj != nil),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             "Exists",
		},
	}
}

func postgresqlConditionGetter(obj client.Object) []meta_v1.Condition {
	typePrefix := strings.ToLower(obj.GetObjectKind().GroupVersionKind().GroupKind().String())
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

	result := make([]meta_v1.Condition, 0, len(conditions))
	for _, condition := range conditions {
		t := fmt.Sprintf("%s/%s", typePrefix, condition.Type)
		result = append(result, meta_v1.Condition{
			Type:               t,
			Status:             makeCondition(condition.Status),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             pg.Status.String(),
		})
	}

	return result
}

func (r *PostgresReconciler) Delete(obj *data_nais_io_v1.Postgres) ([]action.Action, ctrl.Result, error) {
	actionFunc := action.DeleteIfExists
	if !obj.Spec.Cluster.AllowDeletion {
		actionFunc = action.NoOp
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
