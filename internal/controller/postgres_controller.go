package controller

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	storage_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	// ProjectIDLabel and ProjectIDAnnotationFallback is where a team namespace
	// records which Google project it maps to.
	ProjectIDLabel              = "google-cloud-project"
	ProjectIDAnnotationFallback = "cnrm.cloud.google.com/project-id"
)

type conditionConfig struct {
	Type   string
	Status bool
}

// PostgresReconciler reconciles a nais.io/v1 Postgres object into logical
// resources. Physical CNPG resources are owned by PostgresInstance.
type PostgresReconciler struct {
	Config   *config.Config
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

var _ reconciler.Reconciler[*v1.Postgres, PostgresPreparedData] = &PostgresReconciler{}

// PostgresPreparedData contains data prepared during the Prepare phase.
type PostgresPreparedData struct{}

func (r *PostgresReconciler) Name() string {
	return "postgres.nais.io"
}

func (r *PostgresReconciler) New() *v1.Postgres {
	return &v1.Postgres{}
}

func (r *PostgresReconciler) Prepare(_ context.Context, _ client.Reader, _ *v1.Postgres) (PostgresPreparedData, ctrl.Result, error) {
	return PostgresPreparedData{}, ctrl.Result{}, nil
}

func (r *PostgresReconciler) OwnedTypes() []reconciler.OwnedType {
	return []reconciler.OwnedType{
		{Type: &v1.PostgresInstance{}},
	}
}

func (r *PostgresReconciler) AdditionalTypes() []client.Object {
	return nil
}

func (r *PostgresReconciler) MetricsLabels(obj *v1.Postgres) map[string]string {
	ha := "false"
	if obj.Spec.HighAvailability {
		ha = "true"
	}
	return map[string]string{
		"major_version":     obj.Spec.MajorVersion,
		"high_availability": ha,
	}
}

func (r *PostgresReconciler) Update(obj *v1.Postgres, _ PostgresPreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	activeInstance := effectiveActiveInstance(obj)
	obj.GetStatus().(*v1.PostgresStatus).ActiveInstance = activeInstance

	instance := &v1.PostgresInstance{
		TypeMeta:   meta_v1.TypeMeta{APIVersion: v1.GroupVersion.String(), Kind: "PostgresInstance"},
		ObjectMeta: meta_v1.ObjectMeta{Name: obj.GetName(), Namespace: obj.GetNamespace()},
		Spec:       v1.PostgresInstanceSpec{Postgres: obj.GetName()},
	}
	if err := controllerutil.SetControllerReference(obj, instance, r.Scheme); err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("setting controller reference on PostgresInstance: %w", err)
	}
	if obj.Spec.ActiveInstance != "" {
		if relatedObjects.GetMatching(instance) == nil {
			return nil, ctrl.Result{}, nil
		}
		return []action.Action{action.Claim(instance, obj, existsConditionGetter, r.Recorder)}, ctrl.Result{}, nil
	}

	return []action.Action{action.CreateOrUpdate(instance, obj, existsConditionGetter, r.Recorder)}, ctrl.Result{}, nil
}

func effectiveActiveInstance(postgres *v1.Postgres) string {
	if postgres.Spec.ActiveInstance != "" {
		return postgres.Spec.ActiveInstance
	}
	if postgres.Status != nil && postgres.Status.ActiveInstance != "" {
		return postgres.Status.ActiveInstance
	}
	return postgres.GetName()
}

func (r *PostgresReconciler) Delete(_ *v1.Postgres, _ PostgresPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	return nil, ctrl.Result{}, nil
}

func clusterConditionGetter(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition {
	cluster, ok := obj.(*cnpgv1.Cluster)
	if !ok {
		return nil
	}
	phase := cluster.Status.Phase
	return []meta_v1.Condition{{
		Type:               fmt.Sprintf("%s/%s", typePrefix(obj, scheme), "ObservedState"),
		Status:             makeCondition(phase != ""),
		ObservedGeneration: obj.GetGeneration(),
		Reason:             "Reconciled",
		Message:            fmt.Sprintf("Cluster is in phase: %s", phase),
	}}
}

// cnrmConditionsGetter maps Config Connector's status conditions onto the
// Available/Progressing/Degraded triple used across pgrator.
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
		return nil
	}

	statusCondition := meta_v1.Condition{Status: meta_v1.ConditionUnknown, Reason: "Unknown", Message: "No status available on source resource"}
	if len(cnrmConditions) > 0 {
		statusCondition = cnrmConditions[0]
	}

	conditions := []conditionConfig{
		{Type: "Available", Status: statusCondition.Status == meta_v1.ConditionTrue && slices.Contains([]string{"UpToDate", "Updating"}, statusCondition.Reason)},
		{Type: "Progressing", Status: slices.Contains([]string{"Creating", "Updating", "Deleting"}, statusCondition.Reason)},
		{Type: "Degraded", Status: strings.Contains(statusCondition.Reason, "Failed")},
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

func iamServiceAccountHasChanges(desired, existing *iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount) bool {
	return desired.Spec.DisplayName != existing.Spec.DisplayName
}

func iamPolicyHasChanges(desired, existing *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember) bool {
	return desired.Spec.Member != existing.Spec.Member ||
		desired.Spec.Role != existing.Spec.Role ||
		!reflect.DeepEqual(desired.Spec.ResourceRef, existing.Spec.ResourceRef)
}

func copyCnrmAnnotations(existing client.Object, desired *storage_cnrm_cloud_google_com_v1beta1.StorageBucket) {
	for key, value := range existing.GetAnnotations() {
		if strings.HasPrefix(key, "cnrm.cloud.google.com/") {
			meta_v1.SetMetaDataAnnotation(&desired.ObjectMeta, key, value)
		}
	}
}
