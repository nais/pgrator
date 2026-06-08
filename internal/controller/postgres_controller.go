package controller

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/namegen"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	rciam "github.com/nais/pgrator/internal/resourcecreator/iam"
	rcmonitoring "github.com/nais/pgrator/internal/resourcecreator/monitoring"
	rcnetpol "github.com/nais/pgrator/internal/resourcecreator/netpol"
	rczalando "github.com/nais/pgrator/internal/resourcecreator/zalando"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/v1beta1"
	"github.com/nais/pgrator/pkg/api"
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

type conditionConfig struct {
	Type   string
	Status bool
}

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
	TeamGoogleProjectID string `yaml:"teamGoogleProjectID"`
	Engine              string `yaml:"engine"`
}

func (r *PostgresReconciler) Name() string {
	return "postgres.data.nais.io"
}

func (r *PostgresReconciler) MetricsLabels(obj *data_nais_io_v1.Postgres) map[string]string {
	ha := "false"
	if obj.Spec.Cluster.HighAvailability {
		ha = "true"
	}
	engine, err := getEngine(obj)
	if err != nil {
		engine = "unknown"
	}
	return map[string]string{
		"major_version":     obj.Spec.Cluster.MajorVersion,
		"high_availability": ha,
		"engine":            engine,
	}
}

func (r *PostgresReconciler) New() *data_nais_io_v1.Postgres {
	return &data_nais_io_v1.Postgres{}
}

func (r *PostgresReconciler) Prepare(ctx context.Context, reader client.Reader, obj *data_nais_io_v1.Postgres) (PreparedData, ctrl.Result, error) {
	engine, err := getEngine(obj)
	if err != nil {
		return PreparedData{}, ctrl.Result{}, err
	}

	if err := validateEngineImmutability(obj, engine); err != nil {
		return PreparedData{}, ctrl.Result{}, err
	}

	if err := validateVersionForEngine(obj.Spec.Cluster.MajorVersion, engine); err != nil {
		return PreparedData{}, ctrl.Result{}, err
	}

	teamNamespace := &core_v1.Namespace{}
	err = reader.Get(ctx, client.ObjectKey{Name: obj.Namespace}, teamNamespace)
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
		TeamGoogleProjectID: projectID,
		Engine:              engine,
	}

	return p, ctrl.Result{}, nil
}

func (r *PostgresReconciler) OwnedTypes() []reconciler.OwnedType {
	return nil
}

func (r *PostgresReconciler) AdditionalTypes() []client.Object {
	objects := []client.Object{
		&acid_zalan_do_v1.Postgresql{},
		&cnpgv1.Cluster{},
		&cnpgv1.ScheduledBackup{},
		&cnpgv1.Pooler{},
		&networking_v1.NetworkPolicy{},
		&iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{},
		&iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount{},
		&core_v1.ServiceAccount{},
		&rbacv1.RoleBinding{},
	}
	if !r.Config.PrometheusRulesDisabled {
		objects = append(objects, &monitoring_v1.PrometheusRule{}, &monitoring_v1.PodMonitor{})
	}
	return objects
}

func (r *PostgresReconciler) Update(obj *data_nais_io_v1.Postgres, preparedData PreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	// Persist the engine choice in status so it becomes immutable.
	// This is saved to the API server on the first reconcile when the finalizer is added.
	obj.GetStatus().(*data_nais_io_v1.PostgresStatus).Engine = preparedData.Engine

	switch preparedData.Engine {
	case api.EngineCNPG:
		return r.updateCNPG(obj, preparedData, relatedObjects)
	default:
		return r.updateZalando(obj, preparedData, relatedObjects)
	}
}

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

	iamActions, err := r.iamActions(obj, preparedData, pgNamespace, r.Config.CNPG.BackupBucket, relatedObjects)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	actions = append(actions, iamActions...)

	if !r.Config.PrometheusRulesDisabled {
		prometheusRule := rcmonitoring.CreateCNPGPrometheusRule(obj, pgClusterName, pgNamespace)
		actions = append(actions, action.CreateOrUpdate(prometheusRule, obj, existsConditionGetter, r.Recorder))

		podMonitor := rcmonitoring.CreatePodMonitor(obj, pgClusterName, pgNamespace)
		actions = append(actions, action.CreateOrUpdate(podMonitor, obj, existsConditionGetter, r.Recorder))
	}

	return actions, ctrl.Result{}, nil
}

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

	iamActions, err := r.iamActions(obj, preparedData, pgNamespace, r.Config.WalGsBucket, relatedObjects)
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
	switch preparedData.Engine {
	case api.EngineCNPG:
		return r.deleteCNPG(obj, preparedData, relatedObjects)
	default:
		return r.deleteZalando(obj, preparedData, relatedObjects)
	}
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

		podMonitor := rcmonitoring.MinimalPodMonitor(obj, pgClusterName, pgNamespace)
		actions = append(actions, actionFunc(podMonitor, obj, existsConditionGetter, r.Recorder))
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

	iamActions := r.deleteIAMActions(obj, preparedData, pgNamespace, r.Config.WalGsBucket, sharedActionFunc, relatedObjects)
	actions = append(actions, iamActions...)

	if !r.Config.PrometheusRulesDisabled {
		prometheusRule := rcmonitoring.MinimalPrometheusRule(obj, pgClusterName)
		actions = append(actions, actionFunc(prometheusRule, obj, existsConditionGetter, r.Recorder))
	}

	return actions, ctrl.Result{}, nil
}

type actionFunc func(client.Object, api.NaisObject, action.ConditionGetter, events.Recorder) action.Action

// deleteIAMActions returns actions to clean up shared IAM resources during deletion.
func (r *PostgresReconciler) deleteIAMActions(obj *data_nais_io_v1.Postgres, preparedData PreparedData, pgNamespace, bucket string, sharedActionFunc actionFunc, relatedObjects reconciler.RelatedObjects) []action.Action {
	var actions []action.Action

	workloadIdentityPolicyName, storageBucketPolicyName, logsWriterPolicyName := IAMPolicyMemberNames(obj.GetNamespace())

	workloadIdentityPolicy := rciam.CreateWorkloadIdentityPolicyMember(workloadIdentityPolicyName, obj.GetNamespace(), pgNamespace, r.Config.GoogleProjectID, GSAName, KSAName)
	if existing := relatedObjects.GetMatching(workloadIdentityPolicy); existing != nil {
		actions = append(actions, sharedActionFunc(existing, obj, iamConditionGetter, r.Recorder))
	}

	if bucket != "" {
		storageBucketPolicy := rciam.CreateStorageBucketPolicyMember(storageBucketPolicyName, ServiceAccountsNamespace, preparedData.TeamGoogleProjectID, GSAName, bucket)
		if existing := relatedObjects.GetMatching(storageBucketPolicy); existing != nil {
			actions = append(actions, sharedActionFunc(existing, obj, iamConditionGetter, r.Recorder))
		}
	}

	logsWriterPolicy := rciam.CreateLogsWriterPolicyMember(logsWriterPolicyName, obj.GetNamespace(), preparedData.TeamGoogleProjectID, GSAName)
	if existing := relatedObjects.GetMatching(logsWriterPolicy); existing != nil {
		actions = append(actions, sharedActionFunc(existing, obj, iamConditionGetter, r.Recorder))
	}

	kubernetesSA := rciam.CreateKubernetesServiceAccount(KSAName, pgNamespace, preparedData.TeamGoogleProjectID, GSAName)
	if existing := relatedObjects.GetMatching(kubernetesSA); existing != nil {
		actions = append(actions, sharedActionFunc(existing, obj, existsConditionGetter, r.Recorder))
	}

	return actions
}

func getClusterNameAndNamespace(obj *data_nais_io_v1.Postgres, engine string) (string, string, error) {
	var err error
	pgClusterName := obj.GetName()
	if len(pgClusterName) > maxClusterNameLength {
		pgClusterName, err = namegen.ShortName(pgClusterName, maxClusterNameLength)
		if err != nil {
			return "", "", fmt.Errorf("failed to shorten PostgreSQL cluster name: %w", err)
		}
	}
	if engine == api.EngineCNPG {
		return pgClusterName, obj.GetNamespace(), nil
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

// getEngine returns the engine for this Postgres resource.
// Once an active-engine annotation is set (after first successful reconcile),
// it is the source of truth — the user-facing engine annotation is only used
// to detect requested changes (which are rejected by validateEngineImmutability).
// Returns an error for unknown engine values.
func getEngine(obj *data_nais_io_v1.Postgres) (string, error) {
	if obj.Status != nil {
		active := obj.Status.Engine
		if slices.Contains(api.AllEngines, active) {
			return active, nil
		}
		if active != "" {
			return "", fmt.Errorf("unsupported engine %q in status (valid: %s)", active, strings.Join(api.AllEngines, ", "))
		}
	}

	if obj.Annotations == nil {
		return api.EngineZalando, nil
	}

	// First reconcile: use the user-facing annotation to determine engine.
	engine, ok := obj.Annotations[api.EngineAnnotation]
	if !ok || engine == "" {
		return api.EngineZalando, nil
	}
	if slices.Contains(api.AllEngines, engine) {
		return engine, nil
	}

	return "", fmt.Errorf("unsupported engine %q in annotation %s (valid: %s, %s)", engine, api.EngineAnnotation, api.EngineZalando, api.EngineCNPG)
}

// validateEngineImmutability checks that the engine hasn't been changed after initial provisioning.
// Returns an error if engine change is detected.
func validateEngineImmutability(obj *data_nais_io_v1.Postgres, engine string) error {
	if obj.Status == nil {
		return nil
	}

	activeEngine := obj.Status.Engine
	if activeEngine == "" {
		// First reconcile — no active engine yet, allow any choice.
		return nil
	}

	if activeEngine != engine {
		return fmt.Errorf("engine change from %q to %q is not supported; annotation %s is immutable after provisioning", activeEngine, engine, api.EngineAnnotation)
	}
	return nil
}

// validateVersionForEngine checks that the major version is compatible with the selected engine.
// CNPG requires majorVersion >= 18, Zalando only supports 16 and 17.
func validateVersionForEngine(majorVersion string, engine string) error {
	switch engine {
	case api.EngineCNPG:
		version, err := strconv.Atoi(majorVersion)
		if err != nil {
			return fmt.Errorf("invalid major version %q: %w", majorVersion, err)
		}
		if version < 18 {
			return fmt.Errorf("cnpg engine requires majorVersion >= 18, got %q", majorVersion)
		}
	case api.EngineZalando:
		if majorVersion != "16" && majorVersion != "17" {
			return fmt.Errorf("zalando engine only supports majorVersion 16 or 17, got %q", majorVersion)
		}
	}
	return nil
}

// iamActions returns the shared IAM actions used by both Zalando and CNPG codepaths.
func (r *PostgresReconciler) iamActions(obj *data_nais_io_v1.Postgres, preparedData PreparedData, pgNamespace, backupBucket string, relatedObjects reconciler.RelatedObjects) ([]action.Action, error) {
	var actions []action.Action

	workloadIdentityPolicyName, storageBucketPolicyName, logsWriterPolicyName := IAMPolicyMemberNames(obj.GetNamespace())

	workloadIdentityPolicy := rciam.CreateWorkloadIdentityPolicyMember(workloadIdentityPolicyName, obj.GetNamespace(), pgNamespace, r.Config.GoogleProjectID, GSAName, KSAName)
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

	if backupBucket != "" {
		storageBucketPolicy := rciam.CreateStorageBucketPolicyMember(storageBucketPolicyName, ServiceAccountsNamespace, preparedData.TeamGoogleProjectID, GSAName, backupBucket)
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

	logsWriterPolicy := rciam.CreateLogsWriterPolicyMember(logsWriterPolicyName, obj.GetNamespace(), preparedData.TeamGoogleProjectID, GSAName)
	existingLogsWriterPolicy := relatedObjects.GetMatching(logsWriterPolicy)
	if existingLogsWriterPolicy == nil {
		actions = append(actions, action.Create(logsWriterPolicy, obj, iamConditionGetter, r.Recorder))
	} else if iamPolicyHasChanges(logsWriterPolicy, existingLogsWriterPolicy.(*iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember)) {
		if r.Config.ResyncIAMPermissions {
			actions = append(actions, action.Recreate(logsWriterPolicy, obj, iamConditionGetter, r.Recorder))
		} else {
			return nil, fmt.Errorf("want to change IAMPolicyMember %s, but configuration does not allow recreate", client.ObjectKeyFromObject(logsWriterPolicy))
		}
	} else {
		actions = append(actions, action.Claim(logsWriterPolicy, obj, iamConditionGetter, r.Recorder))
	}

	gsa := rciam.CreateServiceAccount(GSAName, obj.GetNamespace())
	existingGsa := relatedObjects.GetMatching(gsa)
	if existingGsa != nil {
		actions = append(actions, action.Claim(gsa, obj, iamConditionGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(gsa, obj, iamConditionGetter, r.Recorder))
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

func reasonable(message string) string {
	nextIsUpper := true
	buf := make([]rune, 0, len(message))
	for _, c := range message {
		thisIsUpper := nextIsUpper
		if c == ' ' {
			nextIsUpper = true
			continue
		} else {
			nextIsUpper = false
		}
		if thisIsUpper {
			buf = append(buf, unicode.ToUpper(c))
		} else {
			buf = append(buf, unicode.ToLower(c))
		}
	}
	return string(buf)
}
