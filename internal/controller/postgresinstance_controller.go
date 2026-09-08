package controller

import (
	"context"
	"fmt"
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
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	gkenetworking "github.com/nais/pgrator/internal/thirdparty/google/networking/v1alpha3"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const postgresInstancePostgresIndex = "spec.postgres"

const (
	instanceBucketNameMaxLength = 63
	instanceBucketUIDSuffixLen  = 12
)

type PostgresInstanceReconciler struct {
	Config   *config.Config
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

var _ reconciler.Reconciler[*v1.PostgresInstance, PostgresInstancePreparedData] = &PostgresInstanceReconciler{}

type PostgresInstancePreparedData struct {
	PostgresName        string          `yaml:"postgresName"`
	PostgresUID         types.UID       `yaml:"postgresUID"`
	PostgresSpec        v1.PostgresSpec `yaml:"postgresSpec"`
	TeamGoogleProjectID string          `yaml:"teamGoogleProjectID"`
}

func (r *PostgresInstanceReconciler) Name() string {
	return "postgresinstance.nais.io"
}

func (r *PostgresInstanceReconciler) New() *v1.PostgresInstance {
	return &v1.PostgresInstance{}
}

func (r *PostgresInstanceReconciler) OwnedTypes() []reconciler.OwnedType {
	ownedTypes := []reconciler.OwnedType{
		{Type: &cnpgv1.Cluster{}},
		{Type: &cnpgv1.DatabaseRole{}},
		{Type: &cnpgv1.Pooler{}},
		{Type: &networkingv1.NetworkPolicy{}},
	}
	if r.walArchivingEnabled() {
		ownedTypes = append(ownedTypes,
			reconciler.OwnedType{Type: &cnpgv1.ScheduledBackup{}},
			reconciler.OwnedType{Type: &barmanv1.ObjectStore{}},
			reconciler.OwnedType{Type: &gkenetworking.FQDNNetworkPolicy{}},
			reconciler.OwnedType{Type: &iamcnrm.IAMServiceAccount{}},
			reconciler.OwnedType{Type: &iamcnrm.IAMPolicyMember{}},
			reconciler.OwnedType{Type: &storagecnrm.StorageBucket{}},
		)
	}
	return ownedTypes
}

func (r *PostgresInstanceReconciler) AdditionalTypes() []client.Object {
	return nil
}

func (r *PostgresInstanceReconciler) Indexes() []reconciler.Index {
	return []reconciler.Index{{
		Object: &v1.PostgresInstance{},
		Field:  postgresInstancePostgresIndex,
		ExtractValue: func(object client.Object) []string {
			instance, ok := object.(*v1.PostgresInstance)
			if !ok || instance.Spec.Postgres == "" {
				return nil
			}
			return []string{instance.Spec.Postgres}
		},
	}}
}

func (r *PostgresInstanceReconciler) RelationshipWatches() []reconciler.RelationshipWatch {
	return []reconciler.RelationshipWatch{{
		Type:      &v1.Postgres{},
		Map:       r.instancesForPostgres,
		Predicate: predicate.GenerationChangedPredicate{},
	}}
}

func (r *PostgresInstanceReconciler) instancesForPostgres(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
	postgres, ok := object.(*v1.Postgres)
	if !ok {
		return nil, nil
	}

	instances := &v1.PostgresInstanceList{}
	if err := reader.List(ctx, instances, client.InNamespace(postgres.GetNamespace()), client.MatchingFields{postgresInstancePostgresIndex: postgres.GetName()}); err != nil {
		return nil, fmt.Errorf("listing PostgresInstances for Postgres %q: %w", postgres.GetName(), err)
	}

	requests := make([]reconcile.Request, 0, len(instances.Items))
	for i := range instances.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&instances.Items[i])})
	}
	return requests, nil
}

func (r *PostgresInstanceReconciler) Prepare(ctx context.Context, reader client.Reader, obj *v1.PostgresInstance) (PostgresInstancePreparedData, ctrl.Result, error) {
	postgres := &v1.Postgres{}
	key := client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.Spec.Postgres}
	if err := reader.Get(ctx, key, postgres); err != nil {
		if apierrors.IsNotFound(err) && !obj.GetDeletionTimestamp().IsZero() {
			return PostgresInstancePreparedData{}, ctrl.Result{}, nil
		}
		return PostgresInstancePreparedData{}, ctrl.Result{}, fmt.Errorf("getting Postgres %q: %w", obj.Spec.Postgres, err)
	}

	prepared := PostgresInstancePreparedData{
		PostgresName: postgres.GetName(),
		PostgresUID:  postgres.GetUID(),
		PostgresSpec: postgres.Spec,
	}
	if !r.walArchivingEnabled() {
		return prepared, ctrl.Result{}, nil
	}

	teamNamespace := &corev1.Namespace{}
	if err := reader.Get(ctx, client.ObjectKey{Name: obj.GetNamespace()}, teamNamespace); err != nil {
		return PostgresInstancePreparedData{}, ctrl.Result{}, fmt.Errorf("getting namespace %q: %w", obj.GetNamespace(), err)
	}

	projectID, ok := teamNamespace.Labels[ProjectIDLabel]
	if !ok || projectID == "" {
		projectID, ok = teamNamespace.Annotations[ProjectIDAnnotationFallback]
	}
	if !ok || projectID == "" {
		return PostgresInstancePreparedData{}, ctrl.Result{}, fmt.Errorf(
			"namespace %q has neither the %q label nor the %q annotation",
			obj.GetNamespace(), ProjectIDLabel, ProjectIDAnnotationFallback)
	}
	prepared.TeamGoogleProjectID = projectID

	return prepared, ctrl.Result{}, nil
}

func (r *PostgresInstanceReconciler) Update(obj *v1.PostgresInstance, prepared PostgresInstancePreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	specSource := &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{
			Name:      obj.GetName(),
			Namespace: obj.GetNamespace(),
			UID:       prepared.PostgresUID,
			Annotations: map[string]string{
				api.DeploymentCorrelationIDAnnotation: obj.GetCorrelationId(),
			},
		},
		Spec: prepared.PostgresSpec,
	}

	actions := make([]action.Action, 0, 5)
	wal := r.walArchive(obj, prepared)

	cluster, err := rccnpg.CreateCluster(r.Scheme, specSource, r.Config, wal)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating CNPG Cluster spec: %w", err)
	}
	if err := transferControllerOwnership(obj, cluster, r.Scheme); err != nil {
		return nil, ctrl.Result{}, err
	}
	actions = append(actions, action.CreateOrUpdate(cluster, obj, clusterConditionGetter, r.Recorder))

	ownerRole, err := createDurableOwnerRole(r.Scheme, obj)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating durable app DatabaseRole: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(ownerRole, obj, existsConditionGetter, r.Recorder))

	pooler, err := rccnpg.CreatePooler(r.Scheme, specSource)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating Pooler spec: %w", err)
	}
	if err := transferControllerOwnership(obj, pooler, r.Scheme); err != nil {
		return nil, ctrl.Result{}, err
	}
	actions = append(actions, action.CreateOrUpdate(pooler, obj, existsConditionGetter, r.Recorder))

	netpol, err := rcnetpol.Create(r.Scheme, specSource, rccnpg.ClusterNameFor(obj.GetName()), r.Config.APIServerIP)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating NetworkPolicy spec: %w", err)
	}
	if err := transferControllerOwnership(obj, netpol, r.Scheme); err != nil {
		return nil, ctrl.Result{}, err
	}
	actions = append(actions, action.CreateOrUpdate(netpol, obj, existsConditionGetter, r.Recorder))

	if wal.Enabled() {
		walActions, err := r.walActions(obj, prepared, wal, relatedObjects, specSource)
		if err != nil {
			return nil, ctrl.Result{}, err
		}
		actions = append(actions, walActions...)
	}

	return actions, ctrl.Result{}, nil
}

func createDurableOwnerRole(scheme *runtime.Scheme, instance *v1.PostgresInstance) (*cnpgv1.DatabaseRole, error) {
	role := &cnpgv1.DatabaseRole{
		TypeMeta: metav1.TypeMeta{Kind: "DatabaseRole", APIVersion: cnpgv1.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rccnpg.ClusterNameFor(instance.GetName()) + "-app",
			Namespace: instance.GetNamespace(),
			Labels: map[string]string{
				"postgres.nais.io/name": instance.Spec.Postgres,
			},
		},
		Spec: cnpgv1.DatabaseRoleSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: rccnpg.ClusterNameFor(instance.GetName())},
			ReclaimPolicy: cnpgv1.DatabaseRoleReclaimDelete,
			RoleConfiguration: cnpgv1.RoleConfiguration{
				Name:    rccnpg.OwnerRole,
				Login:   true,
				Comment: fmt.Sprintf("Managed by pgrator for PostgresInstance %q", instance.GetName()),
			},
			ClientCertificate: &cnpgv1.ClientCertificateConfiguration{Enabled: new(true)},
		},
	}
	if instance.GetCorrelationId() != "" {
		role.Annotations = map[string]string{api.DeploymentCorrelationIDAnnotation: instance.GetCorrelationId()}
	}
	if err := controllerutil.SetControllerReference(instance, role, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on durable app DatabaseRole: %w", err)
	}
	return role, nil
}

func transferControllerOwnership(owner *v1.PostgresInstance, object client.Object, scheme *runtime.Scheme) error {
	object.SetOwnerReferences(nil)
	if err := controllerutil.SetControllerReference(owner, object, scheme); err != nil {
		return fmt.Errorf("setting controller reference on %T %s: %w", object, client.ObjectKeyFromObject(object), err)
	}
	return nil
}

func (r *PostgresInstanceReconciler) walArchivingEnabled() bool {
	return r.Config.CNPG.WalBucketPrefix != ""
}

func (r *PostgresInstanceReconciler) walArchive(instance *v1.PostgresInstance, prepared PostgresInstancePreparedData) rccnpg.WALArchive {
	if !r.walArchivingEnabled() {
		return rccnpg.WALArchive{}
	}
	return rccnpg.WALArchive{
		GSAName:       gsaNameFor(instance.GetName()),
		TeamProjectID: prepared.TeamGoogleProjectID,
		BucketName:    r.bucketName(instance, prepared),
	}
}

func gsaNameFor(instance string) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-%s", instance), validation.DNS1035LabelMaxLength)
}

func workloadIdentityPolicyNameFor(instance string) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-wi-user-%s", instance), validation.DNS1123SubdomainMaxLength)
}

func storageBucketPolicyNameFor(instance string) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-wal-%s", instance), validation.DNS1123SubdomainMaxLength)
}

func storageBucketViewerPolicyNameFor(instance string) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-wal-viewer-%s", instance), validation.DNS1123SubdomainMaxLength)
}

func (r *PostgresInstanceReconciler) bucketName(instance *v1.PostgresInstance, prepared PostgresInstancePreparedData) string {
	if !r.walArchivingEnabled() {
		return ""
	}

	uid := string(prepared.PostgresUID)
	if uid == "" {
		uid = string(instance.GetUID())
	}
	uid = strings.ReplaceAll(uid, "-", "")
	if len(uid) > instanceBucketUIDSuffixLen {
		uid = uid[:instanceBucketUIDSuffixLen]
	}

	prefix := strings.Trim(r.Config.CNPG.WalBucketPrefix, "-")
	maxBaseLength := instanceBucketNameMaxLength - len(uid) - 1
	base := bucketNameBase(prefix, instance.GetNamespace(), instance.GetName(), maxBaseLength)
	return fmt.Sprintf("%s-%s", base, uid)
}

func (r *PostgresInstanceReconciler) walActions(instance *v1.PostgresInstance, prepared PostgresInstancePreparedData, wal rccnpg.WALArchive, relatedObjects reconciler.RelatedObjects, specSource *v1.Postgres) ([]action.Action, error) {
	actions := make([]action.Action, 0, 8)

	gsa := rciam.CreateIAMServiceAccount(wal.GSAName, instance.GetNamespace())
	if err := controllerutil.SetControllerReference(instance, gsa, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on IAMServiceAccount: %w", err)
	}
	switch existing := relatedObjects.GetMatching(gsa); {
	case existing == nil:
		actions = append(actions, action.Create(gsa, instance, cnrmConditionsGetter, r.Recorder))
	case iamServiceAccountHasChanges(gsa, existing.(*iamcnrm.IAMServiceAccount)):
		recreate, err := r.recreateIAM(gsa, instance)
		if err != nil {
			return nil, err
		}
		actions = append(actions, recreate)
	default:
		actions = append(actions, action.Claim(gsa, instance, cnrmConditionsGetter, r.Recorder))
	}

	wiPolicy := rciam.CreateWorkloadIdentityPolicyMember(
		workloadIdentityPolicyNameFor(instance.GetName()),
		instance.GetNamespace(),
		instance.GetNamespace(),
		r.Config.GoogleProjectID,
		wal.GSAName,
		rccnpg.ClusterNameFor(instance.GetName()),
	)
	if err := controllerutil.SetControllerReference(instance, wiPolicy, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on Workload Identity IAMPolicyMember: %w", err)
	}
	wiAction, err := r.policyMemberAction(wiPolicy, instance, relatedObjects)
	if err != nil {
		return nil, err
	}
	actions = append(actions, wiAction)

	bucket := rcstorage.CreateStorageBucket(specSource, wal.BucketName, r.Config.Google.Location)
	if err := transferControllerOwnership(instance, bucket, r.Scheme); err != nil {
		return nil, err
	}
	if existing := relatedObjects.GetMatching(bucket); existing != nil {
		copyCnrmAnnotations(existing, bucket)
		bucket.Spec.ResourceID = existing.(*storagecnrm.StorageBucket).Spec.ResourceID
		actions = append(actions, action.Update(bucket, instance, cnrmConditionsGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(bucket, instance, cnrmConditionsGetter, r.Recorder))
	}

	bucketPolicies := []struct {
		name string
		role string
	}{
		{name: storageBucketPolicyNameFor(instance.GetName()), role: rciam.StorageObjectUserRole},
		{name: storageBucketViewerPolicyNameFor(instance.GetName()), role: rciam.StorageBucketViewerRole},
	}
	for _, policy := range bucketPolicies {
		bucketPolicy := rciam.CreateStorageBucketPolicyMember(
			policy.name,
			instance.GetNamespace(),
			prepared.TeamGoogleProjectID,
			wal.GSAName,
			wal.BucketName,
			policy.role,
		)
		if err := controllerutil.SetControllerReference(instance, bucketPolicy, r.Scheme); err != nil {
			return nil, fmt.Errorf("setting controller reference on bucket IAMPolicyMember: %w", err)
		}
		bucketPolicyAction, err := r.policyMemberAction(bucketPolicy, instance, relatedObjects)
		if err != nil {
			return nil, err
		}
		actions = append(actions, bucketPolicyAction)
	}

	objectStore := rcstorage.CreateObjectStore(wal.BucketName, metav1.ObjectMeta{
		Namespace: instance.GetNamespace(),
		Labels: map[string]string{
			rcstorage.OwnerNameLabel: instance.GetName(),
		},
	})
	if err := controllerutil.SetControllerReference(instance, objectStore, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on ObjectStore: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(objectStore, instance, existsConditionGetter, r.Recorder))

	backup, err := rccnpg.CreateScheduledBackup(r.Scheme, specSource)
	if err != nil {
		return nil, fmt.Errorf("creating ScheduledBackup spec: %w", err)
	}
	if err := transferControllerOwnership(instance, backup, r.Scheme); err != nil {
		return nil, err
	}
	actions = append(actions, action.CreateOrUpdate(backup, instance, existsConditionGetter, r.Recorder))

	fqdnPolicy, err := rcfqdnpolicy.Create(r.Scheme, specSource, rccnpg.ClusterNameFor(instance.GetName()))
	if err != nil {
		return nil, fmt.Errorf("creating WAL FQDNNetworkPolicy spec: %w", err)
	}
	if err := transferControllerOwnership(instance, fqdnPolicy, r.Scheme); err != nil {
		return nil, err
	}
	actions = append(actions, action.CreateOrUpdate(fqdnPolicy, instance, existsConditionGetter, r.Recorder))

	return actions, nil
}

func (r *PostgresInstanceReconciler) policyMemberAction(desired *iamcnrm.IAMPolicyMember, owner *v1.PostgresInstance, relatedObjects reconciler.RelatedObjects) (action.Action, error) {
	existing := relatedObjects.GetMatching(desired)
	if existing == nil {
		return action.Create(desired, owner, cnrmConditionsGetter, r.Recorder), nil
	}
	if iamPolicyHasChanges(desired, existing.(*iamcnrm.IAMPolicyMember)) {
		return r.recreateIAM(desired, owner)
	}
	return action.Claim(desired, owner, cnrmConditionsGetter, r.Recorder), nil
}

func (r *PostgresInstanceReconciler) recreateIAM(desired client.Object, owner *v1.PostgresInstance) (action.Action, error) {
	if !r.Config.ResyncIAMPermissions {
		return nil, fmt.Errorf("want to change %T %s, but configuration does not allow recreate", desired, client.ObjectKeyFromObject(desired))
	}
	return action.Recreate(desired, owner, cnrmConditionsGetter, r.Recorder), nil
}

func (r *PostgresInstanceReconciler) Delete(_ *v1.PostgresInstance, _ PostgresInstancePreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	return nil, ctrl.Result{}, nil
}

func bucketNameBase(prefix, namespace, name string, maxLength int) string {
	if len(prefix) > maxLength-4 {
		prefix = prefix[:maxLength-4]
	}

	ownerLength := maxLength - len(prefix) - 2
	namespace, name = shortenPair(namespace, name, ownerLength)
	return fmt.Sprintf("%s-%s-%s", prefix, namespace, name)
}

func shortenPair(left, right string, maxLength int) (string, string) {
	leftLength := min(len(left), maxLength/2)
	rightLength := min(len(right), maxLength-leftLength)

	if rightLength < maxLength-leftLength {
		leftLength = min(len(left), maxLength-rightLength)
	}
	if leftLength < maxLength-rightLength {
		rightLength = min(len(right), maxLength-leftLength)
	}

	return left[:leftLength], right[:rightLength]
}
