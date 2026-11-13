package controller

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/config"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	storage_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	core_v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

// PostgresReconciler reconciles a nais.io/v1 Postgres object into logical
// resources. Physical CNPG resources are owned by PostgresBranch.
type PostgresReconciler struct {
	Config   *config.Config
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

var _ reconciler.Reconciler[*v1.Postgres, PostgresPreparedData] = &PostgresReconciler{}

// PostgresPreparedData contains data prepared during the Prepare phase.
type PostgresPreparedData struct {
	RequestedBranch string `yaml:"requestedBranch,omitempty"`
	RequestedReady  bool   `yaml:"requestedReady,omitempty"`
}

func (r *PostgresReconciler) Name() string {
	return "postgres.nais.io"
}

func (r *PostgresReconciler) New() *v1.Postgres {
	return &v1.Postgres{}
}

func (r *PostgresReconciler) Prepare(ctx context.Context, reader client.Reader, obj *v1.Postgres) (PostgresPreparedData, ctrl.Result, error) {
	// Deletion must not depend on the identity or readiness of the active branch.
	if !obj.DeletionTimestamp.IsZero() {
		return PostgresPreparedData{}, ctrl.Result{}, nil
	}
	if obj.Spec.ActiveBranch == "" {
		branch := &v1.PostgresBranch{}
		key := client.ObjectKey{Namespace: obj.Namespace, Name: v1.PostgresBranchObjectName(obj.Name, v1.DefaultBranchName)}
		if err := reader.Get(ctx, key, branch); err != nil {
			if apierrors.IsNotFound(err) {
				return PostgresPreparedData{}, ctrl.Result{}, nil
			}
			return PostgresPreparedData{}, ctrl.Result{}, fmt.Errorf("getting default PostgresBranch: %w", err)
		}
		if !validBranchIdentity(branch) || branch.Spec.Postgres != obj.Name || branch.Spec.BranchName != v1.DefaultBranchName {
			return PostgresPreparedData{}, ctrl.Result{}, fmt.Errorf("default PostgresBranch %q has invalid identity", branch.Name)
		}
		return PostgresPreparedData{}, ctrl.Result{}, nil
	}

	requested := &v1.PostgresBranch{}
	key := client.ObjectKey{Namespace: obj.GetNamespace(), Name: v1.PostgresBranchObjectName(obj.Name, obj.Spec.ActiveBranch)}
	if err := reader.Get(ctx, key, requested); err != nil {
		if apierrors.IsNotFound(err) {
			return PostgresPreparedData{RequestedBranch: obj.Spec.ActiveBranch}, ctrl.Result{}, nil
		}
		return PostgresPreparedData{}, ctrl.Result{}, fmt.Errorf("getting requested PostgresBranch %q: %w", obj.Spec.ActiveBranch, err)
	}

	if !validBranchIdentity(requested) || requested.Spec.Postgres != obj.GetName() || requested.Spec.BranchName != obj.Spec.ActiveBranch || !requested.DeletionTimestamp.IsZero() ||
		meta.IsStatusConditionTrue(requested.GetStatus().GetConditions(), recoveryFailedCondition) {
		return PostgresPreparedData{RequestedBranch: obj.Spec.ActiveBranch}, ctrl.Result{}, nil
	}

	cluster := &cnpgv1.Cluster{}
	clusterKey := client.ObjectKey{Namespace: obj.GetNamespace(), Name: rccnpg.ClusterNameFor(requested.GetName())}
	if err := reader.Get(ctx, clusterKey, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return PostgresPreparedData{RequestedBranch: obj.Spec.ActiveBranch}, ctrl.Result{}, nil
		}
		return PostgresPreparedData{}, ctrl.Result{}, fmt.Errorf("getting CNPG Cluster for PostgresBranch %q: %w", requested.GetName(), err)
	}

	return PostgresPreparedData{
		RequestedBranch: obj.Spec.ActiveBranch,
		RequestedReady:  cluster.DeletionTimestamp.IsZero() && recoveryComplete(cluster),
	}, ctrl.Result{}, nil
}

func (r *PostgresReconciler) OwnedTypes() []reconciler.OwnedType {
	return []reconciler.OwnedType{
		{Type: &v1.PostgresBranch{}},
	}
}

func (r *PostgresReconciler) AdditionalTypes() []client.Object {
	return nil
}

func (r *PostgresReconciler) RelationshipWatches() []reconciler.RelationshipWatch {
	return []reconciler.RelationshipWatch{{
		Type:      &v1.PostgresBranch{},
		Predicate: predicate.GenerationChangedPredicate{},
		Map: func(_ context.Context, _ client.Reader, object client.Object) ([]reconcile.Request, error) {
			branch, ok := object.(*v1.PostgresBranch)
			if !ok || !validBranchIdentity(branch) {
				return nil, nil
			}
			// A newly created branch has no owner reference yet, so Owns alone
			// cannot trigger the reconciliation that registers its ownership.
			return []reconcile.Request{{NamespacedName: client.ObjectKey{Namespace: branch.Namespace, Name: branch.Spec.Postgres}}}, nil
		},
	}}
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

func (r *PostgresReconciler) Update(obj *v1.Postgres, prepared PostgresPreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	instance := &v1.PostgresBranch{
		LegacyPostgresTypeMeta: v1.LegacyPostgresTypeMeta{APIVersion: v1.GroupVersion.String(), Kind: "PostgresBranch"},
		ObjectMeta:             meta_v1.ObjectMeta{Name: v1.PostgresBranchObjectName(obj.Name, v1.DefaultBranchName), Namespace: obj.GetNamespace(), Labels: map[string]string{"postgres.nais.io/name": obj.Name, "postgres.nais.io/branch": v1.DefaultBranchName}},
		Spec:                   v1.PostgresBranchSpec{Postgres: obj.GetName(), BranchName: v1.DefaultBranchName},
	}
	if err := controllerutil.SetControllerReference(obj, instance, r.Scheme); err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("setting controller reference on PostgresBranch: %w", err)
	}

	branches := relatedObjects.GetMatchingType(&v1.PostgresBranch{})
	actions := make([]action.Action, 0, len(branches)+1)
	status := obj.GetStatus().(*v1.PostgresStatus)
	ownerManager := ownership.NewOwnerManager(r.Name() + "/owner")
	ownershipPending := false
	// Bootstrap main even when explicitly requested, but never recreate it
	// after another branch has been selected.
	ensureMain := (obj.Spec.ActiveBranch == "" || obj.Spec.ActiveBranch == v1.DefaultBranchName) && observedActiveBranch(obj) == v1.DefaultBranchName
	if ensureMain {
		var mainAction action.Action
		if existing, ok := relatedObjects.GetMatching(instance).(*v1.PostgresBranch); ok {
			if !validBranchIdentity(existing) || existing.Spec.Postgres != obj.Name || existing.Spec.BranchName != v1.DefaultBranchName {
				return nil, ctrl.Result{}, fmt.Errorf("default PostgresBranch %q has invalid identity", existing.Name)
			}
			owned := postgresBranchOwnedBy(existing, obj)
			if err := setPostgresBranchOwner(existing, obj, r.Scheme); err != nil {
				return nil, ctrl.Result{}, err
			}
			if !owned {
				mainAction = action.Claim(existing, obj, existsConditionGetter, r.Recorder)
				ownershipPending = true
			}
		}
		status.ActiveBranch = v1.DefaultBranchName
		if mainAction == nil {
			mainAction = action.ExclusiveCreateOrUpdate(instance, obj, existsConditionGetter, r.Recorder)
		}
		actions = append(actions, mainAction)
	}
	// Branches have their own lifecycle. Removing the activation request must
	// not prune previously claimed branches, including workload-pinned ones.
	for _, candidate := range branches {
		branch, ok := candidate.(*v1.PostgresBranch)
		if !ok || branch.Namespace != obj.Namespace || !validBranchIdentity(branch) || branch.Spec.Postgres != obj.Name {
			continue
		}
		if ensureMain && branch.Name == instance.Name {
			continue // The default branch already has a create/update action.
		}
		if !postgresBranchOwnedBy(branch, obj) || !ownerManager.HasOwnerAnnotation(branch, obj) {
			ownershipPending = true
		}
		if err := setPostgresBranchOwner(branch, obj, r.Scheme); err != nil {
			return nil, ctrl.Result{}, err
		}
		actions = append(actions, action.Claim(branch, obj, existsConditionGetter, r.Recorder))
	}
	// Persist ownership in its own pass before activation validation can fail.
	if ownershipPending {
		return actions, ctrl.Result{RequeueAfter: time.Second}, nil
	}
	if obj.Spec.ActiveBranch != "" && !ensureMain {
		if !prepared.RequestedReady {
			r.Recorder.RecordEvent(obj, core_v1.EventTypeWarning, "ActivationFailed", "requested PostgresBranch %q is not ready", obj.Spec.ActiveBranch)
			return nil, ctrl.Result{}, fmt.Errorf("requested PostgresBranch %q is not ready", obj.Spec.ActiveBranch)
		}
		status.ActiveBranch = obj.Spec.ActiveBranch
	}
	return actions, ctrl.Result{}, nil
}

func observedActiveBranch(postgres *v1.Postgres) string {
	if postgres.Status != nil && postgres.Status.ActiveBranch != "" {
		return postgres.Status.ActiveBranch
	}
	return v1.DefaultBranchName
}

func validBranchIdentity(branch *v1.PostgresBranch) bool {
	return branch.Name == v1.PostgresBranchObjectName(branch.Spec.Postgres, branch.Spec.BranchName)
}

func setPostgresBranchOwner(branch *v1.PostgresBranch, postgres *v1.Postgres, scheme *runtime.Scheme) error {
	if owner := meta_v1.GetControllerOf(branch); owner != nil &&
		(owner.UID != postgres.UID || owner.Name != postgres.Name || owner.Kind != postgresKind || owner.APIVersion != v1.GroupVersion.String()) {
		return fmt.Errorf("PostgresBranch %q is controlled by another resource (UID %s); refusing to adopt it", branch.Name, owner.UID)
	}
	if !branch.CreationTimestamp.IsZero() && branch.CreationTimestamp.Before(&postgres.CreationTimestamp) {
		return fmt.Errorf("PostgresBranch %q predates this Postgres; refusing to adopt it", branch.Name)
	}
	if err := controllerutil.SetControllerReference(postgres, branch, scheme); err != nil {
		return fmt.Errorf("setting controller reference on PostgresBranch %q: %w", branch.Name, err)
	}
	return nil
}

func postgresBranchOwnedBy(branch *v1.PostgresBranch, postgres *v1.Postgres) bool {
	owner := meta_v1.GetControllerOf(branch)
	return owner != nil && owner.APIVersion == v1.GroupVersion.String() && owner.Kind == postgresKind &&
		owner.Name == postgres.Name && owner.UID == postgres.UID && postgres.UID != ""
}

func (r *PostgresReconciler) Delete(obj *v1.Postgres, _ PostgresPreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	var actions []action.Action
	pending := false
	ownerManager := ownership.NewOwnerManager(r.Name() + "/owner")
	for _, candidate := range relatedObjects.GetMatchingType(&v1.PostgresBranch{}) {
		branch := candidate.(*v1.PostgresBranch)
		if branch.Namespace != obj.Namespace {
			continue
		}
		if branch.Spec.Postgres != obj.Name && !meta_v1.IsControlledBy(branch, obj) && !ownerManager.HasOwnerAnnotation(branch, obj) {
			continue
		}
		// References identify candidates, but only a matching controller UID
		// authorizes deletion. Ambiguous ownership blocks finalization.
		if branch.Spec.Postgres != obj.Name || !validBranchIdentity(branch) || !postgresBranchOwnedBy(branch, obj) {
			return nil, ctrl.Result{}, fmt.Errorf("PostgresBranch %q has missing or inconsistent ownership; refusing to delete it", branch.Name)
		}
		pending = true
		if branch.DeletionTimestamp.IsZero() {
			actions = append(actions, action.DeleteIfExists(branch, obj, existsConditionGetter, r.Recorder))
		}
	}
	if pending {
		return actions, ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	return nil, ctrl.Result{}, nil
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
