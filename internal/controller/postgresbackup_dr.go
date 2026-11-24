package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/backupcopy"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/resourcecreator/drbackup"
	rciam "github.com/nais/pgrator/internal/resourcecreator/iam"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	fqdnv1alpha3 "github.com/nais/pgrator/internal/thirdparty/google/networking/v1alpha3"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const postgresBackupKind = "PostgresBackup"

func (r *PostgresBackupReconciler) drEnabled() bool {
	return r.Config != nil && r.Config.CNPG.WalBucketPrefix != ""
}

func (r *PostgresBackupReconciler) drBucketName(namespace string) string {
	if !r.drEnabled() {
		return ""
	}
	return drbackup.DRBucketName(r.Config.CNPG.WalBucketPrefix, namespace)
}

func resolveSourceBucketName(cnpgBackup *cnpgv1.Backup, cluster *cnpgv1.Cluster) string {
	if cnpgBackup != nil && cnpgBackup.Status.DestinationPath != "" {
		trimmed := strings.TrimPrefix(cnpgBackup.Status.DestinationPath, "gs://")
		trimmed = strings.TrimPrefix(trimmed, "s3://")
		if idx := strings.IndexByte(trimmed, '/'); idx > 0 {
			return trimmed[:idx]
		}
		return trimmed
	}
	if cluster != nil {
		for _, plugin := range cluster.Spec.Plugins {
			if plugin.Name == rccnpg.BarmanPluginName && plugin.Parameters["barmanObjectName"] != "" {
				return plugin.Parameters["barmanObjectName"]
			}
		}
	}
	return ""
}

func jobSucceeded(job *batchv1.Job) bool {
	if job == nil {
		return false
	}
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func jobFailed(job *batchv1.Job) (bool, string) {
	if job == nil {
		return false, ""
	}
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			if cond.Message != "" {
				return true, cond.Message
			}
			return true, fmt.Sprintf("Job %q failed", job.Name)
		}
	}
	return false, ""
}

func readJobTerminationPayload(ctx context.Context, reader client.Reader, job *batchv1.Job) *backupcopy.TerminationPayload {
	if job == nil {
		return nil
	}
	pods := &corev1.PodList{}
	if err := reader.List(ctx, pods, client.InNamespace(job.Namespace), client.MatchingLabels{
		drbackup.BackupNameLabel: job.Labels[drbackup.BackupNameLabel],
		drbackup.JobRoleLabel:    job.Labels[drbackup.JobRoleLabel],
	}); err != nil {
		return nil
	}
	for i := range pods.Items {
		for _, cs := range pods.Items[i].Status.ContainerStatuses {
			if cs.State.Terminated != nil && cs.State.Terminated.Message != "" {
				var payload backupcopy.TerminationPayload
				if err := json.Unmarshal([]byte(cs.State.Terminated.Message), &payload); err == nil {
					return &payload
				}
			}
		}
	}
	return nil
}

func (r *PostgresBackupReconciler) sharedDRInfrastructureActions(backup *v1.PostgresBackup, teamProjectID string, keepOwner bool) []action.Action {
	ns := backup.Namespace
	gsaName := drbackup.GSAName(ns)
	drBucket := r.drBucketName(ns)

	gsa := rciam.CreateIAMServiceAccount(gsaName, ns)
	gsa.Spec.DisplayName = fmt.Sprintf("Postgres DR Backup Worker for team %q", ns)

	wiPolicy := rciam.CreateWorkloadIdentityPolicyMember(
		drbackup.WorkloadIdentityPolicyName(ns),
		ns,
		ns,
		r.Config.GoogleProjectID,
		gsaName,
		drbackup.WorkerResourceName,
	)
	bucketObjPolicy := rciam.CreateStorageBucketPolicyMember(
		drbackup.BucketObjectUserPolicyName(ns),
		ns,
		teamProjectID,
		gsaName,
		drBucket,
		rciam.StorageObjectUserRole,
	)
	bucketViewPolicy := rciam.CreateStorageBucketPolicyMember(
		drbackup.BucketViewerPolicyName(ns),
		ns,
		teamProjectID,
		gsaName,
		drBucket,
		rciam.StorageBucketViewerRole,
	)
	bucket := drbackup.CreateDRStorageBucket(ns, drBucket, r.Config.Google.Location)
	ksa := drbackup.CreateWorkerServiceAccount(ns, gsaName, teamProjectID)
	netpol := drbackup.CreateWorkerNetworkPolicy(ns)
	fqdn := drbackup.CreateWorkerFQDNNetworkPolicy(ns)

	objs := []client.Object{gsa, wiPolicy, bucket, bucketObjPolicy, bucketViewPolicy, ksa, netpol, fqdn}
	actions := make([]action.Action, 0, len(objs))
	for _, obj := range objs {
		actions = append(actions, &ensureSharedDRResource{
			backup:    backup,
			desired:   obj,
			keepOwner: keepOwner,
		})
	}
	return actions
}

func (r *PostgresBackupReconciler) sourceBucketPolicyAction(backup *v1.PostgresBackup, teamProjectID, sourceBucket string, related reconciler.RelatedObjects) (action.Action, *iamcnrm.IAMPolicyMember, error) {
	policy := rciam.CreateStorageBucketPolicyMember(
		drbackup.SourceBucketPolicyName(backup.Name),
		backup.Namespace,
		teamProjectID,
		drbackup.GSAName(backup.Namespace),
		sourceBucket,
		rciam.StorageObjectViewerRole,
	)
	if err := controllerutil.SetControllerReference(backup, policy, r.Scheme); err != nil {
		return nil, nil, err
	}
	if related != nil {
		if existing := related.GetMatching(policy); existing != nil {
			return action.Claim(policy, backup, cnrmConditionsGetter, r.Recorder), existing.(*iamcnrm.IAMPolicyMember), nil
		}
	}
	return action.Create(policy, backup, cnrmConditionsGetter, r.Recorder), nil, nil
}

func drInfrastructureReady(namespace, drBucket string, sourcePolicy *iamcnrm.IAMPolicyMember, related reconciler.RelatedObjects) bool {
	if sourcePolicy == nil || !configConnectorReady(sourcePolicy) {
		return false
	}
	if related == nil {
		return false
	}
	gsaName := drbackup.GSAName(namespace)
	required := []client.Object{
		&iamcnrm.IAMServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: gsaName, Namespace: namespace}},
		&iamcnrm.IAMPolicyMember{ObjectMeta: metav1.ObjectMeta{Name: drbackup.WorkloadIdentityPolicyName(namespace), Namespace: namespace}},
		&iamcnrm.IAMPolicyMember{ObjectMeta: metav1.ObjectMeta{Name: drbackup.BucketObjectUserPolicyName(namespace), Namespace: namespace}},
		&iamcnrm.IAMPolicyMember{ObjectMeta: metav1.ObjectMeta{Name: drbackup.BucketViewerPolicyName(namespace), Namespace: namespace}},
		&storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: drBucket, Namespace: namespace}},
	}
	for _, req := range required {
		if !configConnectorReady(related.GetMatching(req)) {
			return false
		}
	}
	return true
}

// ensureSharedDRResource maintains a namespace-shared DR resource with
// non-controller OwnerReferences for every active PostgresBackup in the
// namespace. It is deleted only when the last PostgresBackup in the namespace
// has finished its DR bucket cleanup and released ownership.
type ensureSharedDRResource struct {
	backup    *v1.PostgresBackup
	desired   client.Object
	keepOwner bool
}

func (a *ensureSharedDRResource) GetObject() client.Object { return a.desired }
func (a *ensureSharedDRResource) GetOwner() api.NaisObject { return a.backup }
func (*ensureSharedDRResource) SkipOwnership() bool        { return true }

func (a *ensureSharedDRResource) Do(ctx context.Context, c client.Client, scheme *runtime.Scheme, _ ownership.OwnerManager) error {
	backups := &v1.PostgresBackupList{}
	if err := c.List(ctx, backups, client.InNamespace(a.backup.Namespace)); err != nil {
		return err
	}
	releasing := !a.backup.DeletionTimestamp.IsZero() && !a.keepOwner
	owners := make([]metav1.OwnerReference, 0, len(backups.Items))
	for _, b := range backups.Items {
		if b.UID == a.backup.UID && releasing {
			continue
		}
		if b.UID != "" {
			owners = append(owners, metav1.OwnerReference{
				APIVersion: v1.GroupVersion.String(),
				Kind:       postgresBackupKind,
				Name:       b.Name,
				UID:        b.UID,
			})
		}
	}
	if !releasing && a.backup.UID != "" && !slices.ContainsFunc(owners, func(o metav1.OwnerReference) bool { return o.UID == a.backup.UID }) {
		owners = append(owners, metav1.OwnerReference{
			APIVersion: v1.GroupVersion.String(),
			Kind:       postgresBackupKind,
			Name:       a.backup.Name,
			UID:        a.backup.UID,
		})
	}
	slices.SortFunc(owners, func(x, y metav1.OwnerReference) int { return strings.Compare(x.Name, y.Name) })

	current := a.desired.DeepCopyObject().(client.Object)
	err := c.Get(ctx, client.ObjectKeyFromObject(a.desired), current)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if apierrors.IsNotFound(err) {
		if len(owners) == 0 {
			return nil
		}
		created := a.desired.DeepCopyObject().(client.Object)
		created.SetOwnerReferences(owners)
		return c.Create(ctx, created)
	}

	for _, owner := range current.GetOwnerReferences() {
		if owner.APIVersion != v1.GroupVersion.String() || owner.Kind != postgresBackupKind || (owner.UID == a.backup.UID && releasing) {
			continue
		}
		if !slices.ContainsFunc(owners, func(candidate metav1.OwnerReference) bool { return candidate.UID == owner.UID }) {
			owner.Controller = nil
			owner.BlockOwnerDeletion = nil
			owners = append(owners, owner)
		}
	}
	if len(owners) == 0 {
		return client.IgnoreNotFound(c.Delete(ctx, current))
	}
	slices.SortFunc(owners, func(x, y metav1.OwnerReference) int { return strings.Compare(x.Name, y.Name) })

	original := current.DeepCopyObject().(client.Object)
	current.SetOwnerReferences(owners)
	applySharedDRSpec(current, a.desired)
	if equality.Semantic.DeepEqual(original, current) {
		return nil
	}
	return c.Patch(ctx, current, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{}))
}

func applySharedDRSpec(current, desired client.Object) {
	switch curr := current.(type) {
	case *storagecnrm.StorageBucket:
		des := desired.(*storagecnrm.StorageBucket)
		curr.Spec.Location = des.Spec.Location
		curr.Spec.UniformBucketLevelAccess = des.Spec.UniformBucketLevelAccess
		curr.Spec.PublicAccessPrevention = des.Spec.PublicAccessPrevention
		curr.Spec.LifecycleRules = des.Spec.LifecycleRules
	case *iamcnrm.IAMServiceAccount:
		curr.Spec = desired.(*iamcnrm.IAMServiceAccount).Spec
	case *iamcnrm.IAMPolicyMember:
		curr.Spec = desired.(*iamcnrm.IAMPolicyMember).Spec
	case *corev1.ServiceAccount:
		curr.Annotations = desired.GetAnnotations()
	case *networkingv1.NetworkPolicy:
		curr.Spec = desired.(*networkingv1.NetworkPolicy).Spec
	case *fqdnv1alpha3.FQDNNetworkPolicy:
		curr.Spec = desired.(*fqdnv1alpha3.FQDNNetworkPolicy).Spec
	}
}
