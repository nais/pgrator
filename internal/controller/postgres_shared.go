package controller

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	storage_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	postgresKind = "Postgres"

	// ProjectIDLabel and ProjectIDAnnotationFallback is where a team namespace
	// records which Google project it maps to.
	ProjectIDLabel              = "google-cloud-project"
	ProjectIDAnnotationFallback = "cnrm.cloud.google.com/project-id"
)

type conditionConfig struct {
	Type   string
	Status bool
}

func observedActiveBranch(postgres *v1.Postgres) string {
	if postgres.Status != nil && postgres.Status.ActiveBranch != "" {
		return postgres.Status.ActiveBranch
	}
	return v1.DefaultBranchName
}

func clusterConditionGetter(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition {
	cluster, ok := obj.(*cnpgv1.Cluster)
	if !ok {
		return nil
	}
	condition := meta_v1.Condition{
		Type:               fmt.Sprintf("%s/%s", typePrefix(obj, scheme), "ObservedState"),
		Status:             meta_v1.ConditionUnknown,
		ObservedGeneration: obj.GetGeneration(),
		Reason:             "Unknown",
		Message:            "No readiness status available on source resource",
	}
	if ready := meta.FindStatusCondition(cluster.Status.Conditions, string(cnpgv1.ConditionClusterReady)); ready != nil {
		condition.Status = ready.Status
		condition.Reason = ready.Reason
		if condition.Reason == "" {
			condition.Reason = "Reconciled"
		}
		if ready.Message != "" {
			condition.Message = ready.Message
		}
	}
	if cluster.Status.Phase != "" {
		condition.Message = fmt.Sprintf("Cluster is in phase: %s", cluster.Status.Phase)
	}
	if cluster.Status.PhaseReason != "" {
		condition.Message = fmt.Sprintf("%s: %s", condition.Message, cluster.Status.PhaseReason)
	}
	return []meta_v1.Condition{condition}
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
