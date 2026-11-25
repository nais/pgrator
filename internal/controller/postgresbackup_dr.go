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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const postgresBackupKind = "PostgresBackup"

func (r *PostgresBackupReconciler) drEnabled() bool {
	return r.Config != nil && (r.Config.CNPG.DrBucketPrefix != "" || r.Config.CNPG.WalBucketPrefix != "")
}

func (r *PostgresBackupReconciler) drBucketName(namespace string) string {
	if !r.drEnabled() {
		return ""
	}
	prefix := r.Config.CNPG.DrBucketPrefix
	if prefix == "" {
		prefix = r.Config.CNPG.WalBucketPrefix
	}
	return drbackup.DRBucketName(prefix, namespace)
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
	failed, _ := jobFailed(job)
	succeeded := jobSucceeded(job)
	if !failed && !succeeded {
		return nil
	}
	pods := &corev1.PodList{}
	if err := reader.List(ctx, pods, client.InNamespace(job.Namespace), client.MatchingLabels{
		batchv1.ControllerUidLabel: string(job.UID),
	}); err != nil {
		return nil
	}
	var result *backupcopy.TerminationPayload
	var latest metav1.Time
	for _, pod := range pods.Items {
		if !metav1.IsControlledBy(&pod, job) {
			continue
		}
		for _, cs := range pod.Status.ContainerStatuses {
			term := cs.State.Terminated
			if cs.Name != drbackup.WorkerContainerName || term == nil || succeeded != (term.ExitCode == 0) ||
				(result != nil && !latest.Before(&term.FinishedAt)) {
				continue
			}
			var payload backupcopy.TerminationPayload
			if err := json.Unmarshal([]byte(term.Message), &payload); err == nil {
				latest = term.FinishedAt
				result = &payload
			}
		}
	}
	return result
}

func (r *PostgresBackupReconciler) backupDRBucket(backup *v1.PostgresBackup) string {
	if backup.Status != nil && backup.Status.Location != nil && backup.Status.Location.BucketName != "" {
		return backup.Status.Location.BucketName
	}
	return r.drBucketName(backup.Namespace)
}

func drBucketGrantNames(namespace, bucket string, related reconciler.RelatedObjects) (string, string) {
	name := func(nameFor func(string) string, role string) string {
		// Keep the old name for an existing grant to this bucket. Creating a
		// second IAMPolicyMember for the same binding makes deletion unsafe.
		if related != nil {
			legacy := rciam.MinimalPolicyMember(nameFor(namespace), namespace)
			if grant, ok := related.GetMatching(legacy).(*iamcnrm.IAMPolicyMember); ok &&
				grant.Spec.ResourceRef.Kind == "StorageBucket" && grant.Spec.ResourceRef.Name == bucket && grant.Spec.Role == role {
				return legacy.Name
			}
		}
		return nameFor(bucket)
	}
	return name(drbackup.BucketObjectUserPolicyName, rciam.StorageObjectUserRole),
		name(drbackup.BucketViewerPolicyName, rciam.StorageBucketViewerRole)
}

func (r *PostgresBackupReconciler) sharedDRInfrastructureActions(backup *v1.PostgresBackup, teamProjectID string, keepOwner bool, related reconciler.RelatedObjects) []action.Action {
	ns := backup.Namespace
	gsaName := drbackup.GSAName(ns)
	drBucket := r.backupDRBucket(backup)
	objectUserName, bucketViewerName := drBucketGrantNames(ns, drBucket, related)

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
		objectUserName,
		ns,
		teamProjectID,
		gsaName,
		drBucket,
		rciam.StorageObjectUserRole,
	)
	bucketViewPolicy := rciam.CreateStorageBucketPolicyMember(
		bucketViewerName,
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

	var actions []action.Action
	add := func(obj client.Object, bucketScope string) {
		labels := obj.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		}
		labels[drbackup.ManagedByLabel] = drbackup.ManagedByValue
		obj.SetLabels(labels)
		actions = append(actions, &ensureSharedDRResource{
			backup:    backup,
			desired:   obj,
			keepOwner: keepOwner,
			bucket:    bucketScope,
		})
	}
	for _, obj := range []client.Object{gsa, wiPolicy, ksa, netpol, fqdn} {
		add(obj, "")
	}
	for _, obj := range []client.Object{bucket, bucketObjPolicy, bucketViewPolicy} {
		add(obj, drBucket)
	}
	return actions
}

func (r *PostgresBackupReconciler) sourceBucketPolicyAction(backup *v1.PostgresBackup, teamProjectID, sourceBucket string, related reconciler.RelatedObjects) (action.Action, *iamcnrm.IAMPolicyMember, error) {
	policy := rciam.CreateStorageBucketPolicyMember(
		drbackup.SourceBucketPolicyName(sourceBucket),
		backup.Namespace,
		teamProjectID,
		drbackup.GSAName(backup.Namespace),
		sourceBucket,
		rciam.StorageObjectViewerRole,
	)
	// One cloud binding, with one synchronizer owner annotation per active
	// copy. Pruning unclaims it and deletes it only after the last copy.
	if related != nil {
		if existing := related.GetMatching(policy); existing != nil {
			resource := existing.(*iamcnrm.IAMPolicyMember)
			if !equality.Semantic.DeepEqual(resource.Spec, policy.Spec) {
				return nil, nil, fmt.Errorf("source bucket policy %q has an unexpected binding", policy.Name)
			}
			return action.Claim(resource, backup, cnrmConditionsGetter, r.Recorder), resource, nil
		}
	}
	return action.Create(policy, backup, cnrmConditionsGetter, r.Recorder), nil, nil
}

func drInfrastructureReady(namespace, drBucket string, sourcePolicy *iamcnrm.IAMPolicyMember, related reconciler.RelatedObjects) bool {
	if sourcePolicy == nil || !sourcePolicy.DeletionTimestamp.IsZero() || !configConnectorReady(sourcePolicy) {
		return false
	}
	if related == nil {
		return false
	}
	gsaName := drbackup.GSAName(namespace)
	objectUserName, bucketViewerName := drBucketGrantNames(namespace, drBucket, related)
	required := []client.Object{
		&iamcnrm.IAMServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: gsaName, Namespace: namespace}},
		&iamcnrm.IAMPolicyMember{ObjectMeta: metav1.ObjectMeta{Name: drbackup.WorkloadIdentityPolicyName(namespace), Namespace: namespace}},
		&iamcnrm.IAMPolicyMember{ObjectMeta: metav1.ObjectMeta{Name: objectUserName, Namespace: namespace}},
		&iamcnrm.IAMPolicyMember{ObjectMeta: metav1.ObjectMeta{Name: bucketViewerName, Namespace: namespace}},
		&storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: drBucket, Namespace: namespace}},
	}
	for _, req := range required {
		obj := related.GetMatching(req)
		if obj == nil || !obj.GetDeletionTimestamp().IsZero() || !configConnectorReady(obj) {
			return false
		}
	}
	return true
}

// ensureSharedDRResource maintains non-controller OwnerReferences for backups
// sharing a bucket or namespace. The last owner releases it after cleanup.
type ensureSharedDRResource struct {
	backup    *v1.PostgresBackup
	desired   client.Object
	keepOwner bool
	bucket    string // empty for resources shared across all DR buckets
}

func (a *ensureSharedDRResource) GetObject() client.Object { return a.desired }
func (a *ensureSharedDRResource) GetOwner() api.NaisObject { return a.backup }
func (*ensureSharedDRResource) SkipOwnership() bool        { return true }

func (a *ensureSharedDRResource) desiredOwners(backups []v1.PostgresBackup, releasing bool) ([]metav1.OwnerReference, map[types.UID]bool) {
	owners := make([]metav1.OwnerReference, 0, len(backups))
	otherBucket := make(map[types.UID]bool)
	for _, b := range backups {
		if b.UID == a.backup.UID && releasing {
			continue
		}
		if a.bucket != "" && b.UID != a.backup.UID {
			if b.Status == nil || b.Status.Location == nil {
				continue // retain an existing claim below, but do not add one yet
			}
			if b.Status.Location.BucketName != a.bucket {
				otherBucket[b.UID] = true
				continue
			}
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
	return owners, otherBucket
}

func (a *ensureSharedDRResource) Do(ctx context.Context, c client.Client, _ *runtime.Scheme, _ ownership.OwnerManager) error {
	backups := &v1.PostgresBackupList{}
	if err := c.List(ctx, backups, client.InNamespace(a.backup.Namespace)); err != nil {
		return err
	}
	releasing := !a.backup.DeletionTimestamp.IsZero() && !a.keepOwner
	owners, otherBucket := a.desiredOwners(backups.Items, releasing)
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

	managed := current.GetLabels()[drbackup.ManagedByLabel] == drbackup.ManagedByValue
	for _, owner := range current.GetOwnerReferences() {
		if owner.APIVersion == v1.GroupVersion.String() && owner.Kind == postgresBackupKind &&
			(owner.UID == a.backup.UID || slices.ContainsFunc(owners, func(candidate metav1.OwnerReference) bool { return candidate.UID == owner.UID })) {
			managed = true // adopt resources created before all DR resources carried the label
		}
		if owner.APIVersion != v1.GroupVersion.String() || owner.Kind != postgresBackupKind || (owner.UID == a.backup.UID && releasing) || otherBucket[owner.UID] {
			continue
		}
		if !slices.ContainsFunc(owners, func(candidate metav1.OwnerReference) bool { return candidate.UID == owner.UID }) {
			owner.Controller = nil
			owner.BlockOwnerDeletion = nil
			owners = append(owners, owner)
		}
	}
	if !managed {
		return fmt.Errorf("refusing to modify unmanaged shared DR resource %T %s", current, client.ObjectKeyFromObject(current))
	}
	if len(owners) == 0 {
		uid, version := current.GetUID(), current.GetResourceVersion()
		return client.IgnoreNotFound(c.Delete(ctx, current, client.Preconditions{UID: &uid, ResourceVersion: &version}))
	}
	if !current.GetDeletionTimestamp().IsZero() {
		return fmt.Errorf("shared DR resource %T %s is terminating", current, client.ObjectKeyFromObject(current))
	}
	slices.SortFunc(owners, func(x, y metav1.OwnerReference) int { return strings.Compare(x.Name, y.Name) })

	original := current.DeepCopyObject().(client.Object)
	current.SetOwnerReferences(owners)
	labels := current.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[drbackup.ManagedByLabel] = drbackup.ManagedByValue
	current.SetLabels(labels)
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
