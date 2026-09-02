package controller

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/namegen"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	rcfqdnpolicy "github.com/nais/pgrator/internal/resourcecreator/fqdnpolicy"
	rciam "github.com/nais/pgrator/internal/resourcecreator/iam"
	rcnetpol "github.com/nais/pgrator/internal/resourcecreator/netpol"
	rcstorage "github.com/nais/pgrator/internal/resourcecreator/storage"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	networking_gke_io_v1alpha3 "github.com/nais/pgrator/internal/thirdparty/google/networking/v1alpha3"
	storage_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	core_v1 "k8s.io/api/core/v1"
	networking_v1 "k8s.io/api/networking/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
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

// PostgresReconciler reconciles a nais.io/v1 Postgres object into a CloudNativePG
// Cluster, an app-owner DatabaseRole (cert auth), a PgBouncer Pooler, and a
// NetworkPolicy.
type PostgresReconciler struct {
	Config   *config.Config
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

var _ reconciler.Reconciler[*v1.Postgres, PostgresPreparedData] = &PostgresReconciler{}

// PostgresPreparedData contains data prepared during the Prepare phase.
type PostgresPreparedData struct {
	// TeamGoogleProjectID is the Google project the team's namespace maps to.
	// Only resolved when WAL archiving is enabled.
	TeamGoogleProjectID string `yaml:"teamGoogleProjectID"`
}

// walArchivingEnabled reports whether this installation provisions WAL buckets.
// Local development (kind/Tilt) runs without Config Connector, so everything
// Google-specific is skipped there.
func (r *PostgresReconciler) walArchivingEnabled() bool {
	return r.Config.CNPG.WalBucketPrefix != ""
}

// bucketName is derived from the Postgres UID so that a recreated resource never
// reuses another resource's WAL archive.
func (r *PostgresReconciler) bucketName(obj *v1.Postgres) string {
	if !r.walArchivingEnabled() {
		return ""
	}
	return fmt.Sprintf("%s-%s", r.Config.CNPG.WalBucketPrefix, obj.GetUID())
}

func gsaName(obj *v1.Postgres) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-%s", obj.GetName()), validation.DNS1035LabelMaxLength)
}

func workloadIdentityPolicyName(obj *v1.Postgres) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-wi-user-%s", obj.GetName()), validation.DNS1123SubdomainMaxLength)
}

func storageBucketPolicyName(obj *v1.Postgres) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-wal-%s", obj.GetName()), validation.DNS1123SubdomainMaxLength)
}

func (r *PostgresReconciler) walArchive(obj *v1.Postgres, preparedData PostgresPreparedData) rccnpg.WALArchive {
	if !r.walArchivingEnabled() {
		return rccnpg.WALArchive{}
	}
	return rccnpg.WALArchive{
		GSAName:       gsaName(obj),
		TeamProjectID: preparedData.TeamGoogleProjectID,
		BucketName:    r.bucketName(obj),
	}
}

func (r *PostgresReconciler) Name() string {
	return "postgres.nais.io"
}

func (r *PostgresReconciler) New() *v1.Postgres {
	return &v1.Postgres{}
}

// Prepare resolves the team's Google project, which is needed to construct the
// service account e-mail used for Workload Identity and bucket access.
func (r *PostgresReconciler) Prepare(ctx context.Context, reader client.Reader, obj *v1.Postgres) (PostgresPreparedData, ctrl.Result, error) {
	if !r.walArchivingEnabled() {
		return PostgresPreparedData{}, ctrl.Result{}, nil
	}

	teamNamespace := &core_v1.Namespace{}
	if err := reader.Get(ctx, client.ObjectKey{Name: obj.GetNamespace()}, teamNamespace); err != nil {
		return PostgresPreparedData{}, ctrl.Result{}, fmt.Errorf("getting namespace %q: %w", obj.GetNamespace(), err)
	}

	projectID, ok := teamNamespace.Labels[ProjectIDLabel]
	if !ok || projectID == "" {
		projectID, ok = teamNamespace.Annotations[ProjectIDAnnotationFallback]
	}
	if !ok || projectID == "" {
		return PostgresPreparedData{}, ctrl.Result{}, fmt.Errorf(
			"namespace %q has neither the %q label nor the %q annotation",
			obj.GetNamespace(), ProjectIDLabel, ProjectIDAnnotationFallback)
	}

	return PostgresPreparedData{TeamGoogleProjectID: projectID}, ctrl.Result{}, nil
}

func (r *PostgresReconciler) OwnedTypes() []reconciler.OwnedType {
	types := []reconciler.OwnedType{
		{
			Type: &cnpgv1.Cluster{},
			AdditionalPredicate: predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldObj, ok1 := e.ObjectOld.(*cnpgv1.Cluster)
					newObj, ok2 := e.ObjectNew.(*cnpgv1.Cluster)
					if !ok1 || !ok2 {
						return false
					}
					return oldObj.Status.Phase != newObj.Status.Phase
				},
			},
		},
		{Type: &cnpgv1.DatabaseRole{}},
		{Type: &cnpgv1.Pooler{}},
		{Type: &cnpgv1.ScheduledBackup{}},
		{Type: &barmanv1.ObjectStore{}},
		{Type: &networking_v1.NetworkPolicy{}},
	}
	if r.walArchivingEnabled() {
		types = append(types,
			reconciler.OwnedType{Type: &networking_gke_io_v1alpha3.FQDNNetworkPolicy{}},
			reconciler.OwnedType{Type: &iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount{}},
			reconciler.OwnedType{Type: &iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember{}},
			reconciler.OwnedType{Type: &storage_cnrm_cloud_google_com_v1beta1.StorageBucket{}},
		)
	}
	return types
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

func (r *PostgresReconciler) Update(obj *v1.Postgres, preparedData PostgresPreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	var actions []action.Action

	wal := r.walArchive(obj, preparedData)

	cluster, err := rccnpg.CreateCluster(r.Scheme, obj, r.Config, wal)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating CNPG Cluster spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(cluster, obj, clusterConditionGetter, r.Recorder))

	pooler, err := rccnpg.CreatePooler(r.Scheme, obj)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating Pooler spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(pooler, obj, existsConditionGetter, r.Recorder))

	netpol, err := rcnetpol.Create(r.Scheme, obj, rccnpg.ClusterName(obj), r.Config.APIServerIP)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating NetworkPolicy spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(netpol, obj, existsConditionGetter, r.Recorder))

	if wal.Enabled() {
		walActions, err := r.walActions(obj, preparedData, wal, relatedObjects)
		if err != nil {
			return nil, ctrl.Result{}, err
		}
		actions = append(actions, walActions...)
	}

	return actions, ctrl.Result{}, nil
}

// walActions builds the WAL archive: the Google service account and its Workload
// Identity binding, the bucket and its IAM binding, the barman-cloud ObjectStore,
// nightly ScheduledBackup, and FQDN egress policy.
//
// Config Connector resources are created once and then claimed rather than
// updated, because changing an IAMPolicyMember's member or role in place is not
// supported; a genuine change requires a recreate, which is gated behind
// ResyncIAMPermissions so it is never done silently.
func (r *PostgresReconciler) walActions(obj *v1.Postgres, preparedData PostgresPreparedData, wal rccnpg.WALArchive, relatedObjects reconciler.RelatedObjects) ([]action.Action, error) {
	var actions []action.Action

	gsa := rciam.CreateIAMServiceAccount(wal.GSAName, obj.GetNamespace())
	if err := controllerutil.SetControllerReference(obj, gsa, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on IAMServiceAccount: %w", err)
	}
	switch existing := relatedObjects.GetMatching(gsa); {
	case existing == nil:
		actions = append(actions, action.Create(gsa, obj, cnrmConditionsGetter, r.Recorder))
	case iamServiceAccountHasChanges(gsa, existing.(*iam_cnrm_cloud_google_com_v1beta1.IAMServiceAccount)):
		recreate, err := r.recreateIAM(gsa, obj)
		if err != nil {
			return nil, err
		}
		actions = append(actions, recreate)
	default:
		actions = append(actions, action.Claim(gsa, obj, cnrmConditionsGetter, r.Recorder))
	}

	wiPolicy := rciam.CreateWorkloadIdentityPolicyMember(
		workloadIdentityPolicyName(obj),
		obj.GetNamespace(),
		obj.GetNamespace(),
		r.Config.GoogleProjectID,
		wal.GSAName,
		rccnpg.ClusterName(obj),
	)
	if err := controllerutil.SetControllerReference(obj, wiPolicy, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on Workload Identity IAMPolicyMember: %w", err)
	}
	wiActions, err := r.policyMemberActions(wiPolicy, obj, relatedObjects)
	if err != nil {
		return nil, err
	}
	actions = append(actions, wiActions)

	bucket := rcstorage.CreateStorageBucket(obj, wal.BucketName, r.Config.Google.Location)
	if err := controllerutil.SetControllerReference(obj, bucket, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on StorageBucket: %w", err)
	}
	if existing := relatedObjects.GetMatching(bucket); existing != nil {
		// Config Connector writes state back onto the object; preserve it so we do
		// not fight the CNRM controller on every reconcile.
		copyCnrmAnnotations(existing, bucket)
		bucket.Spec.ResourceID = existing.(*storage_cnrm_cloud_google_com_v1beta1.StorageBucket).Spec.ResourceID
		actions = append(actions, action.Update(bucket, obj, cnrmConditionsGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(bucket, obj, cnrmConditionsGetter, r.Recorder))
	}

	bucketPolicy := rciam.CreateStorageBucketPolicyMember(
		storageBucketPolicyName(obj),
		obj.GetNamespace(),
		preparedData.TeamGoogleProjectID,
		wal.GSAName,
		wal.BucketName,
	)
	if err := controllerutil.SetControllerReference(obj, bucketPolicy, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on bucket IAMPolicyMember: %w", err)
	}
	bucketPolicyAction, err := r.policyMemberActions(bucketPolicy, obj, relatedObjects)
	if err != nil {
		return nil, err
	}
	actions = append(actions, bucketPolicyAction)

	objectStore := rcstorage.CreateObjectStore(wal.BucketName, objectStoreMeta(obj))
	if err := controllerutil.SetControllerReference(obj, objectStore, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on ObjectStore: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(objectStore, obj, existsConditionGetter, r.Recorder))

	backup, err := rccnpg.CreateScheduledBackup(r.Scheme, obj)
	if err != nil {
		return nil, fmt.Errorf("creating ScheduledBackup spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(backup, obj, existsConditionGetter, r.Recorder))

	fqdnPolicy, err := rcfqdnpolicy.Create(r.Scheme, obj, rccnpg.ClusterName(obj))
	if err != nil {
		return nil, fmt.Errorf("creating WAL FQDNNetworkPolicy spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(fqdnPolicy, obj, existsConditionGetter, r.Recorder))

	return actions, nil
}

func (r *PostgresReconciler) policyMemberActions(desired *iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember, obj *v1.Postgres, relatedObjects reconciler.RelatedObjects) (action.Action, error) {
	existing := relatedObjects.GetMatching(desired)
	if existing == nil {
		return action.Create(desired, obj, cnrmConditionsGetter, r.Recorder), nil
	}
	if iamPolicyHasChanges(desired, existing.(*iam_cnrm_cloud_google_com_v1beta1.IAMPolicyMember)) {
		return r.recreateIAM(desired, obj)
	}
	return action.Claim(desired, obj, cnrmConditionsGetter, r.Recorder), nil
}

func (r *PostgresReconciler) recreateIAM(desired client.Object, obj *v1.Postgres) (action.Action, error) {
	if !r.Config.ResyncIAMPermissions {
		return nil, fmt.Errorf("want to change %T %s, but configuration does not allow recreate",
			desired, client.ObjectKeyFromObject(desired))
	}
	return action.Recreate(desired, obj, cnrmConditionsGetter, r.Recorder), nil
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

func objectStoreMeta(obj *v1.Postgres) meta_v1.ObjectMeta {
	return meta_v1.ObjectMeta{
		Namespace: obj.GetNamespace(),
		Labels: map[string]string{
			rcstorage.OwnerNameLabel: obj.GetName(),
		},
	}
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
	return []meta_v1.Condition{
		{
			Type:               fmt.Sprintf("%s/%s", typePrefix(obj, scheme), "ObservedState"),
			Status:             makeCondition(phase != ""),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             "Reconciled",
			Message:            fmt.Sprintf("Cluster is in phase: %s", phase),
		},
	}
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

	statusCondition := meta_v1.Condition{
		Status:  meta_v1.ConditionUnknown,
		Reason:  "Unknown",
		Message: "No status available on source resource",
	}
	if len(cnrmConditions) > 0 {
		statusCondition = cnrmConditions[0]
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
