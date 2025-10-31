package controller

import (
	"context"
	"fmt"
	"strings"

	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/liberator/pkg/apis/iam.cnrm.cloud.google.com/v1beta1"
	"github.com/nais/liberator/pkg/namegen"
	liberator_strings "github.com/nais/liberator/pkg/strings"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/controller/resourcecreator"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	monitoring_v1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	networking_v1 "k8s.io/api/networking/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Max length is 63, but we need to save space for suffixes added by Zalando operator or StatefulSets
	maxClusterNameLength = 50
)

// PostgresReconciler reconciles a Postgres object
type PostgresReconciler struct {
	Config *config.Config
}

var _ reconciler.Reconciler[*data_nais_io_v1.Postgres, PreparedData] = &PostgresReconciler{}

type PreparedData struct {
}

func (r *PostgresReconciler) Name() string {
	return "postgres.data.nais.io"
}

func (r *PostgresReconciler) New() *data_nais_io_v1.Postgres {
	return &data_nais_io_v1.Postgres{}
}

func (r *PostgresReconciler) Prepare(_ctx context.Context, _reader client.Reader, _obj *data_nais_io_v1.Postgres) (PreparedData, ctrl.Result, error) {
	return PreparedData{}, ctrl.Result{}, nil
}

func (r *PostgresReconciler) OwnedTypes() []client.Object {
	return nil
}

func (r *PostgresReconciler) AdditionalTypes() []client.Object {
	return []client.Object{
		&acid_zalan_do_v1.Postgresql{},
		&networking_v1.NetworkPolicy{},
		&iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{},
		&monitoring_v1.PrometheusRule{},
	}
}

func (r *PostgresReconciler) Update(obj *data_nais_io_v1.Postgres, _preparedData PreparedData) ([]action.Action, ctrl.Result, error) {
	var err error
	pgClusterName, pgNamespace, err := getClusterNameAndNamespace(obj)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	ownerAnnotationKey := fmt.Sprintf("%s/owner", r.Name())
	ownerAnnotationValue := fmt.Sprintf("%s:%s", obj.GetNamespace(), obj.GetName())

	var actions []action.Action
	cluster := resourcecreator.CreateClusterSpec(obj, r.Config, pgClusterName, pgNamespace)
	v1.SetMetaDataAnnotation(&cluster.ObjectMeta, ownerAnnotationKey, ownerAnnotationValue)
	actions = append(actions, action.CreateOrUpdate(cluster, obj, postgresqlConditionGetter))

	netpol := resourcecreator.CreatePostgresNetworkPolicySpec(obj, pgClusterName, pgNamespace)
	v1.SetMetaDataAnnotation(&netpol.ObjectMeta, ownerAnnotationKey, ownerAnnotationValue)
	actions = append(actions, action.CreateOrUpdate(netpol, obj, existsConditionGetter))

	iam, err := resourcecreator.CreateIAMPolicyMemberSpec(obj, r.Config, pgNamespace)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	if !r.Config.PrometheusRulesDisabled {
		prometheusRule := resourcecreator.CreatePrometheusRuleSpec(obj, pgClusterName, pgNamespace)
		v1.SetMetaDataAnnotation(&prometheusRule.ObjectMeta, ownerAnnotationKey, ownerAnnotationValue)
		actions = append(actions, action.CreateOrUpdate(prometheusRule, obj, existsConditionGetter))
	}

	v1.SetMetaDataAnnotation(&iam.ObjectMeta, ownerAnnotationKey, ownerAnnotationValue)
	actions = append(actions, action.CreateIfNotExists(iam, obj, iamPolicyMemberConditionGetter))

	return actions, ctrl.Result{}, nil
}

func iamPolicyMemberConditionGetter(obj client.Object) []v1.Condition {
	typePrefix := strings.ToLower(obj.GetObjectKind().GroupVersionKind().GroupKind().String())
	iamPolicyMember := obj.(*iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember)

	statusCondition := v1.Condition{}
	if len(iamPolicyMember.Status.Conditions) > 0 {
		statusCondition = iamPolicyMember.Status.Conditions[0]
	}

	type conditionConfig struct {
		Type   string
		Status bool
	}
	conditions := []conditionConfig{
		{
			Type:   "Available",
			Status: statusCondition.Status == v1.ConditionTrue && liberator_strings.ContainsString([]string{"UpToDate", "Updating"}, statusCondition.Reason),
		},
		{
			Type:   "Progressing",
			Status: liberator_strings.ContainsString([]string{"Creating", "Updating", "Deleting"}, statusCondition.Reason),
		},
		{
			Type:   "Degraded",
			Status: strings.Contains(statusCondition.Reason, "Failed"),
		},
	}

	result := make([]v1.Condition, 0, len(conditions))
	for _, condition := range conditions {
		t := fmt.Sprintf("%s/%s", typePrefix, condition.Type)
		result = append(result, v1.Condition{
			Type:               t,
			Status:             makeCondition(condition.Status),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             statusCondition.Reason,
			Message:            statusCondition.Message,
		})
	}

	return result
}

func makeCondition(value bool) v1.ConditionStatus {
	if value {
		return v1.ConditionTrue
	} else {
		return v1.ConditionFalse
	}
}

func existsConditionGetter(obj client.Object) []v1.Condition {
	typePrefix := strings.ToLower(obj.GetObjectKind().GroupVersionKind().GroupKind().String())
	return []v1.Condition{
		{
			Type:               fmt.Sprintf("%s/Available", typePrefix),
			Status:             makeCondition(obj != nil),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             "Exists",
		},
	}
}

func postgresqlConditionGetter(obj client.Object) []v1.Condition {
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

	result := make([]v1.Condition, 0, len(conditions))
	for _, condition := range conditions {
		t := fmt.Sprintf("%s/%s", typePrefix, condition.Type)
		result = append(result, v1.Condition{
			Type:               t,
			Status:             makeCondition(condition.Status),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             pg.Status.String(),
		})
	}

	return result
}

func (r *PostgresReconciler) Delete(obj *data_nais_io_v1.Postgres) ([]action.Action, ctrl.Result, error) {
	if !obj.Spec.Cluster.AllowDeletion {
		return nil, ctrl.Result{}, nil
	}
	var err error
	pgClusterName, pgNamespace, err := getClusterNameAndNamespace(obj)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	var actions []action.Action

	cluster := resourcecreator.MinimalCluster(obj, pgClusterName, pgNamespace)
	actions = append(actions, action.DeleteIfExists(cluster, obj, postgresqlConditionGetter))

	netpol := resourcecreator.MinimalNetpol(obj, pgClusterName, pgNamespace)
	actions = append(actions, action.DeleteIfExists(netpol, obj, existsConditionGetter))

	if !r.Config.PrometheusRulesDisabled {
		prometheusRule := resourcecreator.MinimalPrometheusRule(obj, pgClusterName)
		actions = append(actions, action.DeleteIfExists(prometheusRule, obj, existsConditionGetter))
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
