package controller

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/namegen"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	storage_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
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
		&barmanv1.ObjectStore{},
		&networking_v1.NetworkPolicy{},
		&iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{},
		&iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount{},
		&storage_cnrm_cloud_google_com_v1beta1.StorageBucket{},
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

func cnrmConditionsGetter(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition {
	var cnrmConditions []meta_v1.Condition
	switch o := obj.(type) {
	case *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember:
		cnrmConditions = o.Status.Conditions
	case *iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount:
		cnrmConditions = o.Status.Conditions
	case *storage_cnrm_cloud_google_com_v1beta1.StorageBucket:
		cnrmConditions = o.Status.Conditions
	default:
		panic(fmt.Sprintf("unsupported type for groupkind: %s (%T)", typePrefix(obj, scheme), o))
	}

	var statusCondition meta_v1.Condition
	if len(cnrmConditions) > 0 {
		statusCondition = cnrmConditions[0]
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

func (r *PostgresReconciler) Delete(obj *data_nais_io_v1.Postgres, preparedData PreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	switch preparedData.Engine {
	case api.EngineCNPG:
		return r.deleteCNPG(obj, preparedData, relatedObjects)
	default:
		return r.deleteZalando(obj, preparedData, relatedObjects)
	}
}

type actionFunc func(client.Object, api.NaisObject, action.ConditionGetter, events.Recorder) action.Action

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
