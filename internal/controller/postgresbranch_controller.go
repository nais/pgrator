package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	postgresBranchPostgresIndex       = "spec.postgres"
	postgresBranchRecoverySourceIndex = "spec.bootstrap.recovery.sourceBranch"
)

const (
	instanceBucketNameMaxLength  = 63
	instanceBucketUIDSuffixLen   = 12
	gcpServiceAccountIDMaxLength = 30
)

type PostgresBranchReconciler struct {
	Config   *config.Config
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

var _ reconciler.Reconciler[*v1.PostgresBranch, PostgresBranchPreparedData] = &PostgresBranchReconciler{}

type PostgresBranchPreparedData struct {
	PostgresName        string          `yaml:"postgresName"`
	PostgresUID         types.UID       `yaml:"postgresUID"`
	PostgresSpec        v1.PostgresSpec `yaml:"postgresSpec"`
	TeamGoogleProjectID string          `yaml:"teamGoogleProjectID"`
	ActiveBranch        string          `yaml:"activeBranch,omitempty"`
	PostgresDeleting    bool            `yaml:"postgresDeleting,omitempty"`
	RecoverySource      *rccnpg.RecoverySource
	RecoverySourceReady bool
	RecoveryClusterUID  types.UID `yaml:"recoveryClusterUID,omitempty"`
	BlockingRecoveries  []string  `yaml:"blockingRecoveries,omitempty"`
}

func (r *PostgresBranchReconciler) Name() string {
	return "postgresbranch.nais.io"
}

func (r *PostgresBranchReconciler) New() *v1.PostgresBranch {
	return &v1.PostgresBranch{}
}

func (r *PostgresBranchReconciler) OwnedTypes() []reconciler.OwnedType {
	ownedTypes := []reconciler.OwnedType{
		{
			Type: &cnpgv1.Cluster{},
			AdditionalPredicate: predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldCluster, oldOK := e.ObjectOld.(*cnpgv1.Cluster)
					newCluster, newOK := e.ObjectNew.(*cnpgv1.Cluster)
					return oldOK && newOK && (continuousArchivingReady(oldCluster) != continuousArchivingReady(newCluster) || recoveryComplete(oldCluster) != recoveryComplete(newCluster) || recoveryBackedUp(oldCluster) != recoveryBackedUp(newCluster))
				},
			},
		},
		{Type: &cnpgv1.DatabaseRole{}},
		{Type: &cnpgv1.Pooler{}},
		{Type: &networkingv1.NetworkPolicy{}},
	}
	if r.walArchivingEnabled() {
		ownedTypes = append(ownedTypes,
			reconciler.OwnedType{Type: &cnpgv1.ScheduledBackup{}},
			reconciler.OwnedType{Type: &barmanv1.ObjectStore{}},
			reconciler.OwnedType{Type: &gkenetworking.FQDNNetworkPolicy{}},
			reconciler.OwnedType{
				Type: &iamcnrm.IAMServiceAccount{},
				AdditionalPredicate: predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
					oldResource, oldOK := e.ObjectOld.(*iamcnrm.IAMServiceAccount)
					newResource, newOK := e.ObjectNew.(*iamcnrm.IAMServiceAccount)
					return oldOK && newOK && configConnectorReady(oldResource) != configConnectorReady(newResource)
				}},
			},
			reconciler.OwnedType{
				Type: &iamcnrm.IAMPolicyMember{},
				AdditionalPredicate: predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
					oldResource, oldOK := e.ObjectOld.(*iamcnrm.IAMPolicyMember)
					newResource, newOK := e.ObjectNew.(*iamcnrm.IAMPolicyMember)
					return oldOK && newOK && configConnectorReady(oldResource) != configConnectorReady(newResource)
				}},
			},
			reconciler.OwnedType{
				Type: &storagecnrm.StorageBucket{},
				AdditionalPredicate: predicate.Funcs{UpdateFunc: func(e event.UpdateEvent) bool {
					oldResource, oldOK := e.ObjectOld.(*storagecnrm.StorageBucket)
					newResource, newOK := e.ObjectNew.(*storagecnrm.StorageBucket)
					return oldOK && newOK && configConnectorReady(oldResource) != configConnectorReady(newResource)
				}},
			},
		)
	}
	return ownedTypes
}

func (r *PostgresBranchReconciler) AdditionalTypes() []client.Object {
	return nil
}

func (r *PostgresBranchReconciler) Indexes() []reconciler.Index {
	return []reconciler.Index{
		{
			Object: &v1.PostgresBranch{},
			Field:  postgresBranchPostgresIndex,
			ExtractValue: func(object client.Object) []string {
				instance, ok := object.(*v1.PostgresBranch)
				if !ok || instance.Spec.Postgres == "" {
					return nil
				}
				return []string{instance.Spec.Postgres}
			},
		},
		{
			Object:       &v1.PostgresBranch{},
			Field:        postgresBranchRecoverySourceIndex,
			ExtractValue: recoverySourceBranchIndex,
		},
	}
}

func (r *PostgresBranchReconciler) RelationshipWatches() []reconciler.RelationshipWatch {
	return []reconciler.RelationshipWatch{
		{
			Type:      &v1.Postgres{},
			Map:       r.branchesForPostgres,
			Predicate: predicate.GenerationChangedPredicate{},
		},
		{
			Type:      &v1.PostgresBranch{},
			Map:       r.branchesForRecoverySource,
			Predicate: predicate.GenerationChangedPredicate{},
		},
		{
			Type:      &storagecnrm.StorageBucket{},
			Map:       r.branchesForRecoverySourceBucket,
			Predicate: configConnectorReadinessEventFilter(),
		},
	}
}

func recoverySourceBranchIndex(object client.Object) []string {
	instance, ok := object.(*v1.PostgresBranch)
	if !ok || instance.Spec.Bootstrap == nil || instance.Spec.Bootstrap.Recovery == nil || instance.Spec.Bootstrap.Recovery.SourceBranch == "" {
		return nil
	}
	return []string{v1.PostgresBranchObjectName(instance.Spec.Postgres, instance.Spec.Bootstrap.Recovery.SourceBranch)}
}

func (r *PostgresBranchReconciler) branchesForPostgres(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
	postgres, ok := object.(*v1.Postgres)
	if !ok {
		return nil, nil
	}

	instances := &v1.PostgresBranchList{}
	if err := reader.List(ctx, instances, client.InNamespace(postgres.GetNamespace()), client.MatchingFields{postgresBranchPostgresIndex: postgres.GetName()}); err != nil {
		return nil, fmt.Errorf("listing PostgresBranches for Postgres %q: %w", postgres.GetName(), err)
	}

	requests := make([]reconcile.Request, 0, len(instances.Items))
	for i := range instances.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&instances.Items[i])})
	}
	return requests, nil
}

func (r *PostgresBranchReconciler) branchesForRecoverySource(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
	instance, ok := object.(*v1.PostgresBranch)
	if !ok {
		return nil, nil
	}
	if !validBranchIdentity(instance) {
		return nil, nil
	}
	return r.recoveryRequestsForSource(ctx, reader, instance.GetNamespace(), instance.GetName())
}

func (r *PostgresBranchReconciler) branchesForRecoverySourceBucket(ctx context.Context, reader client.Reader, object client.Object) ([]reconcile.Request, error) {
	bucket, ok := object.(*storagecnrm.StorageBucket)
	if !ok {
		return nil, nil
	}
	objectStore := &barmanv1.ObjectStore{}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(bucket), objectStore); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting ObjectStore for recovery source bucket %q: %w", bucket.GetName(), err)
	}
	return r.recoveryRequestsForSource(ctx, reader, bucket.GetNamespace(), objectStore.Labels[rcstorage.OwnerNameLabel])
}

func (r *PostgresBranchReconciler) recoveryRequestsForSource(ctx context.Context, reader client.Reader, namespace, source string) ([]reconcile.Request, error) {
	if source == "" {
		return nil, nil
	}
	instances := &v1.PostgresBranchList{}
	if err := reader.List(ctx, instances, client.InNamespace(namespace), client.MatchingFields{postgresBranchRecoverySourceIndex: source}); err != nil {
		return nil, fmt.Errorf("listing recovery PostgresBranches for source %q: %w", source, err)
	}
	requests := make([]reconcile.Request, 0, len(instances.Items))
	for i := range instances.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&instances.Items[i])})
	}
	return requests, nil
}

func (r *PostgresBranchReconciler) Prepare(ctx context.Context, reader client.Reader, obj *v1.PostgresBranch) (PostgresBranchPreparedData, ctrl.Result, error) {
	if !validBranchIdentity(obj) {
		message := fmt.Sprintf("PostgresBranch %q does not match spec.postgres %q and spec.branchName %q", obj.Name, obj.Spec.Postgres, obj.Spec.BranchName)
		obj.GetStatus().SetCondition(metav1.Condition{Type: readyCondition, Status: metav1.ConditionFalse, Reason: "InvalidIdentity", Message: message, ObservedGeneration: obj.Generation})
		return PostgresBranchPreparedData{}, ctrl.Result{}, errors.New(message)
	}
	postgres := &v1.Postgres{}
	key := client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.Spec.Postgres}
	if err := reader.Get(ctx, key, postgres); err != nil {
		if apierrors.IsNotFound(err) && !obj.GetDeletionTimestamp().IsZero() {
			return PostgresBranchPreparedData{}, ctrl.Result{}, nil
		}
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("getting Postgres %q: %w", obj.Spec.Postgres, err)
	}

	prepared := PostgresBranchPreparedData{
		PostgresName: postgres.GetName(),
		PostgresUID:  postgres.GetUID(),
		PostgresSpec: postgres.Spec,
	}
	prepared.ActiveBranch = effectiveActiveBranch(postgres)
	prepared.PostgresDeleting = !postgres.GetDeletionTimestamp().IsZero()
	// The source archive must remain until dependent recoveries are detached.
	// No recovery inputs are required to delete this branch itself.
	if obj.DeletionTimestamp != nil {
		if !prepared.PostgresDeleting {
			blocking, err := r.blockingRecoveries(ctx, reader, obj)
			if err != nil {
				return PostgresBranchPreparedData{}, ctrl.Result{}, err
			}
			prepared.BlockingRecoveries = blocking
		}
		return prepared, ctrl.Result{}, nil
	}
	if obj.Spec.Bootstrap != nil && obj.Spec.Bootstrap.Recovery != nil && !r.walArchivingEnabled() {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("recovery requires WAL archiving")
	}
	if !r.walArchivingEnabled() {
		return prepared, ctrl.Result{}, nil
	}

	teamNamespace := &corev1.Namespace{}
	if err := reader.Get(ctx, client.ObjectKey{Name: obj.GetNamespace()}, teamNamespace); err != nil {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("getting namespace %q: %w", obj.GetNamespace(), err)
	}

	projectID, ok := teamNamespace.Labels[ProjectIDLabel]
	if !ok || projectID == "" {
		projectID, ok = teamNamespace.Annotations[ProjectIDAnnotationFallback]
	}
	if !ok || projectID == "" {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf(
			"namespace %q has neither the %q label nor the %q annotation",
			obj.GetNamespace(), ProjectIDLabel, ProjectIDAnnotationFallback)
	}
	prepared.TeamGoogleProjectID = projectID
	if obj.Spec.Bootstrap == nil || obj.Spec.Bootstrap.Recovery == nil {
		return prepared, ctrl.Result{}, nil
	}
	return r.prepareRecovery(ctx, reader, obj, prepared)
}

func (r *PostgresBranchReconciler) blockingRecoveries(ctx context.Context, reader client.Reader, branch *v1.PostgresBranch) ([]string, error) {
	dependents := &v1.PostgresBranchList{}
	if err := reader.List(ctx, dependents, client.InNamespace(branch.Namespace), client.MatchingFields{postgresBranchRecoverySourceIndex: branch.Name}); err != nil {
		return nil, fmt.Errorf("listing dependent recoveries: %w", err)
	}
	var blocking []string
	for i := range dependents.Items {
		dependent := &dependents.Items[i]
		if dependent.DeletionTimestamp != nil {
			continue
		}
		cluster := &cnpgv1.Cluster{}
		key := client.ObjectKey{Namespace: branch.Namespace, Name: rccnpg.ClusterNameFor(dependent.Name)}
		if err := reader.Get(ctx, key, cluster); err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("getting dependent recovery cluster %q: %w", key.Name, err)
		}
		if !recoveryFinished(dependent, cluster) || cluster.Spec.Bootstrap == nil || cluster.Spec.Bootstrap.Recovery != nil || len(cluster.Spec.ExternalClusters) != 0 {
			blocking = append(blocking, dependent.Name)
		}
	}
	return blocking, nil
}

func (r *PostgresBranchReconciler) prepareRecovery(ctx context.Context, reader client.Reader, obj *v1.PostgresBranch, prepared PostgresBranchPreparedData) (PostgresBranchPreparedData, ctrl.Result, error) {
	recovery := obj.Spec.Bootstrap.Recovery
	sourceName := v1.PostgresBranchObjectName(obj.Spec.Postgres, recovery.SourceBranch)
	prepared.RecoverySource = &rccnpg.RecoverySource{
		BucketName: r.bucketNameForInstanceName(obj.GetNamespace(), sourceName, prepared.PostgresUID),
		ServerName: rccnpg.ClusterNameFor(sourceName),
		TargetTime: recovery.TargetTime,
	}
	if recovery.SourceBranch == obj.Spec.BranchName {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("recovery source branch cannot be itself")
	}
	if recovery.TargetTime.IsZero() {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("recovery target time is required")
	}
	_, offset := recovery.TargetTime.Zone()
	if offset != 0 {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("recovery target time must be UTC")
	}
	cluster := &cnpgv1.Cluster{}
	err := reader.Get(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: rccnpg.ClusterNameFor(obj.GetName())}, cluster)
	if err == nil {
		if !metav1.IsControlledBy(cluster, obj) {
			return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("recovery cluster %q is not owned by PostgresBranch %q", cluster.Name, obj.Name)
		}
		if cluster.DeletionTimestamp != nil {
			return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("recovery cluster %q is being deleted", cluster.Name)
		}
		if cluster.UID == "" {
			return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("recovery cluster %q has no UID", cluster.Name)
		}
		prepared.RecoveryClusterUID = cluster.UID
		if recoveredClusterUID := completedRecoveryClusterUID(obj); recoveredClusterUID != "" && recoveredClusterUID != string(cluster.UID) {
			return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("recovered Cluster %q was replaced; refusing to bootstrap from the original recovery source", cluster.Name)
		}
		if recoveryFinished(obj, cluster) {
			return prepared, ctrl.Result{}, nil
		}
		if recoveryBackedUp(cluster) {
			// Synchronizer persists status before Update runs. Record the backed-up
			// Cluster UID before removing bootstrap inputs from the Cluster spec.
			obj.GetStatus().SetCondition(metav1.Condition{
				Type: recoveryCompletedCondition, Status: metav1.ConditionTrue,
				Reason: "Completed", Message: string(cluster.UID), ObservedGeneration: obj.Generation,
			})
			return prepared, ctrl.Result{}, nil
		}
	} else if !apierrors.IsNotFound(err) {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("getting recovery cluster: %w", err)
	} else if completedRecoveryClusterUID(obj) != "" {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("recovered Cluster %q disappeared; refusing to bootstrap from the original recovery source", rccnpg.ClusterNameFor(obj.GetName()))
	}
	source := &v1.PostgresBranch{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: sourceName}, source); err != nil {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("getting recovery source branch %q: %w", recovery.SourceBranch, err)
	}
	if !validBranchIdentity(source) || source.Spec.Postgres != obj.Spec.Postgres || source.Spec.BranchName != recovery.SourceBranch {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("recovery source branch %q belongs to Postgres %q, want %q", source.GetName(), source.Spec.Postgres, obj.Spec.Postgres)
	}
	if source.DeletionTimestamp != nil {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("recovery source branch %q is being deleted", source.Name)
	}
	sourceArchives := &barmanv1.ObjectStoreList{}
	if err := reader.List(ctx, sourceArchives, client.InNamespace(obj.GetNamespace()), client.MatchingLabels{rcstorage.OwnerNameLabel: source.GetName()}); err != nil {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("listing recovery source archives for branch %q: %w", source.GetName(), err)
	}
	if len(sourceArchives.Items) != 1 {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("recovery source branch %q has %d archives, want 1", source.GetName(), len(sourceArchives.Items))
	}
	prepared.RecoverySource.BucketName = sourceArchives.Items[0].GetName()
	sourceBucket := &storagecnrm.StorageBucket{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: prepared.RecoverySource.BucketName}, sourceBucket); err != nil {
		return PostgresBranchPreparedData{}, ctrl.Result{}, fmt.Errorf("getting recovery source archive for branch %q: %w", source.GetName(), err)
	}
	prepared.RecoverySourceReady = configConnectorReady(sourceBucket)
	return prepared, ctrl.Result{}, nil
}

func (r *PostgresBranchReconciler) Update(obj *v1.PostgresBranch, prepared PostgresBranchPreparedData, relatedObjects reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	clusterKey := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name:      rccnpg.ClusterNameFor(obj.GetName()),
		Namespace: obj.GetNamespace(),
	}}
	existingCluster, _ := relatedObjects.GetMatching(clusterKey).(*cnpgv1.Cluster)
	if prepared.RecoverySource != nil {
		if existingCluster == nil && prepared.RecoveryClusterUID != "" || existingCluster != nil && existingCluster.UID != prepared.RecoveryClusterUID {
			return nil, ctrl.Result{}, fmt.Errorf("recovery cluster %q changed since preparation", clusterKey.Name)
		}
	}
	setObservedClusterName(obj, existingCluster)

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

	actions := make([]action.Action, 0, 6)
	wal := r.walArchive(obj, prepared)

	personalGroup, err := createPersonalAccessGroupRole(r.Scheme, obj)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating personal access group DatabaseRole: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(personalGroup, obj, existsConditionGetter, r.Recorder))

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

	clusterExists := existingCluster != nil
	recovered := prepared.RecoverySource != nil && recoveryFinished(obj, existingCluster)
	recoveryInProgress := prepared.RecoverySource != nil && !recovered
	if wal.Enabled() {
		walActions, err := r.walActions(obj, prepared, wal, recoveryInProgress, relatedObjects, specSource)
		if err != nil {
			return nil, ctrl.Result{}, err
		}
		actions = append(actions, walActions...)
	}
	if prepared.RecoverySource == nil || clusterExists || prepared.RecoverySourceReady && recoveryInfrastructureReady(obj, wal, prepared.RecoverySource, relatedObjects) {
		recoverySource := prepared.RecoverySource
		if recovered {
			// The source archive belongs to another branch and may have been deleted.
			// Once recovered, replicas and backups use this branch's own archive.
			recoverySource = nil
		}
		cluster, err := rccnpg.CreateCluster(r.Scheme, specSource, r.Config, wal, recoverySource)
		if err != nil {
			return nil, ctrl.Result{}, fmt.Errorf("creating CNPG Cluster spec: %w", err)
		}
		cluster.Labels["postgres.nais.io/name"] = obj.Spec.Postgres
		cluster.Labels["postgres.nais.io/branch"] = obj.Spec.BranchName
		// postInitSQL, which creates the app_readwritecreate group role, runs only
		// at initdb. Mark the cluster as readwritecreate-capable only when this
		// reconcile creates a fresh initdb cluster; once set, the marker is
		// preserved across reconciles. Clusters that predate the role and clusters
		// bootstrapped by recovery stay unmarked.
		freshInitDBCluster := !clusterExists && prepared.RecoverySource == nil
		if freshInitDBCluster || rccnpg.ReadWriteCreateCapable(existingCluster) {
			metav1.SetMetaDataAnnotation(&cluster.ObjectMeta, rccnpg.ReadWriteCreateCapableAnnotation, "true")
		}
		if err := transferControllerOwnership(obj, cluster, r.Scheme); err != nil {
			return nil, ctrl.Result{}, err
		}
		if prepared.RecoverySource != nil {
			if clusterExists {
				actions = append(actions, action.UpdateSameUID(cluster, obj, prepared.RecoveryClusterUID, clusterConditionGetter, r.Recorder))
			} else {
				actions = append(actions, action.Create(cluster, obj, clusterConditionGetter, r.Recorder))
			}
		} else if clusterExists && existingCluster.UID != "" {
			actions = append(actions, action.UpdateSameUID(cluster, obj, existingCluster.UID, clusterConditionGetter, r.Recorder))
		} else {
			actions = append(actions, action.CreateOrUpdate(cluster, obj, clusterConditionGetter, r.Recorder))
		}
	}

	return actions, ctrl.Result{}, nil
}

func setObservedClusterName(branch *v1.PostgresBranch, cluster *cnpgv1.Cluster) {
	status := branch.GetStatus().(*v1.PostgresBranchStatus)
	status.ClusterName = ""
	if cluster != nil && metav1.IsControlledBy(cluster, branch) {
		status.ClusterName = cluster.GetName()
	}
}

func createPersonalAccessGroupRole(scheme *runtime.Scheme, instance *v1.PostgresBranch) (*cnpgv1.DatabaseRole, error) {
	role := &cnpgv1.DatabaseRole{
		TypeMeta: metav1.TypeMeta{Kind: "DatabaseRole", APIVersion: cnpgv1.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rccnpg.ClusterNameFor(instance.GetName()) + "-personal-access",
			Namespace: instance.GetNamespace(),
		},
		Spec: cnpgv1.DatabaseRoleSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: rccnpg.ClusterNameFor(instance.GetName())},
			ReclaimPolicy: cnpgv1.DatabaseRoleReclaimDelete,
			RoleConfiguration: cnpgv1.RoleConfiguration{
				Name:            rccnpg.PersonalAccessRole(instance.Name),
				Login:           false,
				DisablePassword: true,
				Comment:         "Authentication group for personal Postgres access",
			},
		},
	}
	if err := controllerutil.SetControllerReference(instance, role, scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on personal access group DatabaseRole: %w", err)
	}
	return role, nil
}

func createDurableOwnerRole(scheme *runtime.Scheme, instance *v1.PostgresBranch) (*cnpgv1.DatabaseRole, error) {
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
				Comment: fmt.Sprintf("Managed by pgrator for PostgresBranch %q", instance.GetName()),
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

func transferControllerOwnership(owner *v1.PostgresBranch, object client.Object, scheme *runtime.Scheme) error {
	object.SetOwnerReferences(nil)
	if err := controllerutil.SetControllerReference(owner, object, scheme); err != nil {
		return fmt.Errorf("setting controller reference on %T %s: %w", object, client.ObjectKeyFromObject(object), err)
	}
	return nil
}

func (r *PostgresBranchReconciler) walArchivingEnabled() bool {
	return r.Config.CNPG.WalBucketPrefix != ""
}

func (r *PostgresBranchReconciler) walArchive(instance *v1.PostgresBranch, prepared PostgresBranchPreparedData) rccnpg.WALArchive {
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
	return namegen.MustShortenName(fmt.Sprintf("cnpg-%s", instance), gcpServiceAccountIDMaxLength)
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

func recoverySourcePolicyNameFor(instance string) string {
	return namegen.MustShortenName(fmt.Sprintf("cnpg-recovery-source-%s", instance), validation.DNS1123SubdomainMaxLength)
}

func (r *PostgresBranchReconciler) bucketName(instance *v1.PostgresBranch, prepared PostgresBranchPreparedData) string {
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

	return r.bucketNameForInstanceName(instance.GetNamespace(), instance.GetName(), types.UID(uid))
}

func (r *PostgresBranchReconciler) bucketNameForInstanceName(namespace, instanceName string, postgresUID types.UID) string {
	uid := strings.ReplaceAll(string(postgresUID), "-", "")
	if len(uid) > instanceBucketUIDSuffixLen {
		uid = uid[:instanceBucketUIDSuffixLen]
	}
	prefix := strings.Trim(r.Config.CNPG.WalBucketPrefix, "-")
	maxBaseLength := instanceBucketNameMaxLength - len(uid) - 1
	base := bucketNameBase(prefix, namespace, instanceName, maxBaseLength)
	return fmt.Sprintf("%s-%s", base, uid)
}

func configConnectorReadinessEventFilter() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		DeleteFunc: func(event.DeleteEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldResource, oldOK := e.ObjectOld.(*storagecnrm.StorageBucket)
			newResource, newOK := e.ObjectNew.(*storagecnrm.StorageBucket)
			return oldOK && newOK && configConnectorReady(oldResource) != configConnectorReady(newResource)
		},
	}
}

func (r *PostgresBranchReconciler) walActions(instance *v1.PostgresBranch, prepared PostgresBranchPreparedData, wal rccnpg.WALArchive, needsRecoverySourceAccess bool, relatedObjects reconciler.RelatedObjects, specSource *v1.Postgres) ([]action.Action, error) {
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
	if needsRecoverySourceAccess {
		sourcePolicy := rciam.CreateStorageBucketPolicyMember(
			recoverySourcePolicyNameFor(instance.GetName()),
			instance.GetNamespace(),
			prepared.TeamGoogleProjectID,
			wal.GSAName,
			prepared.RecoverySource.BucketName,
			rciam.StorageObjectViewerRole,
		)
		if err := controllerutil.SetControllerReference(instance, sourcePolicy, r.Scheme); err != nil {
			return nil, fmt.Errorf("setting controller reference on recovery source IAMPolicyMember: %w", err)
		}
		sourcePolicyAction, err := r.policyMemberAction(sourcePolicy, instance, relatedObjects)
		if err != nil {
			return nil, err
		}
		actions = append(actions, sourcePolicyAction)
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

	backup := &cnpgv1.ScheduledBackup{ObjectMeta: metav1.ObjectMeta{
		Name:      rccnpg.ClusterNameFor(instance.GetName()),
		Namespace: instance.GetNamespace(),
	}}
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name:      rccnpg.ClusterNameFor(instance.GetName()),
		Namespace: instance.GetNamespace(),
	}}
	existingCluster, _ := relatedObjects.GetMatching(cluster).(*cnpgv1.Cluster)
	if continuousArchivingReady(existingCluster) || relatedObjects.GetMatching(backup) != nil {
		backup, err := rccnpg.CreateScheduledBackup(r.Scheme, specSource)
		if err != nil {
			return nil, fmt.Errorf("creating ScheduledBackup spec: %w", err)
		}
		if err := transferControllerOwnership(instance, backup, r.Scheme); err != nil {
			return nil, err
		}
		actions = append(actions, action.CreateOrUpdate(backup, instance, existsConditionGetter, r.Recorder))
	}

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

func continuousArchivingReady(cluster *cnpgv1.Cluster) bool {
	if cluster == nil {
		return false
	}
	for _, condition := range cluster.Status.Conditions {
		if condition.Type == string(cnpgv1.ConditionContinuousArchiving) {
			return condition.Status == metav1.ConditionTrue
		}
	}
	return false
}

const recoveryCompletedCondition = "RecoveryCompleted"

// recoveryFinished is a one-way bootstrap milestone for this particular
// Cluster object, independent of whether it is currently Ready.
func recoveryFinished(branch *v1.PostgresBranch, cluster *cnpgv1.Cluster) bool {
	return cluster != nil && cluster.DeletionTimestamp == nil &&
		metav1.IsControlledBy(cluster, branch) && cluster.UID != "" &&
		completedRecoveryClusterUID(branch) == string(cluster.UID)
}

func completedRecoveryClusterUID(branch *v1.PostgresBranch) string {
	for _, condition := range branch.GetStatus().GetConditions() {
		if condition.Type == recoveryCompletedCondition && condition.Status == metav1.ConditionTrue {
			return condition.Message
		}
	}
	return ""
}

// A successful backup of this Cluster proves that it no longer depends on
// the source archive. Initialized alone is not sufficient: the new branch
// must first have a usable archive of its own.
func recoveryBackedUp(cluster *cnpgv1.Cluster) bool {
	if !cluster.IsInitialized() || cluster.Spec.Bootstrap == nil || cluster.Spec.Bootstrap.Recovery == nil {
		return false
	}
	for _, condition := range cluster.Status.Conditions {
		if condition.Type == "LastBackupSucceeded" && condition.Status == metav1.ConditionTrue &&
			condition.LastTransitionTime.After(cluster.CreationTimestamp.Time) {
			return true
		}
	}
	return false
}

func recoveryComplete(cluster *cnpgv1.Cluster) bool {
	if cluster == nil || !cluster.IsInitialized() {
		return false
	}
	for _, condition := range cluster.Status.Conditions {
		if condition.Type == string(cnpgv1.ConditionClusterReady) {
			return condition.Status == metav1.ConditionTrue
		}
	}
	return false
}

func recoveryInfrastructureReady(instance *v1.PostgresBranch, wal rccnpg.WALArchive, recovery *rccnpg.RecoverySource, relatedObjects reconciler.RelatedObjects) bool {
	resources := []client.Object{
		rciam.CreateIAMServiceAccount(gsaNameFor(instance.GetName()), instance.GetNamespace()),
		rciam.CreateWorkloadIdentityPolicyMember(workloadIdentityPolicyNameFor(instance.GetName()), instance.GetNamespace(), instance.GetNamespace(), "", gsaNameFor(instance.GetName()), rccnpg.ClusterNameFor(instance.GetName())),
		rciam.CreateStorageBucketPolicyMember(storageBucketPolicyNameFor(instance.GetName()), instance.GetNamespace(), "", gsaNameFor(instance.GetName()), wal.BucketName, rciam.StorageObjectUserRole),
		rciam.CreateStorageBucketPolicyMember(storageBucketViewerPolicyNameFor(instance.GetName()), instance.GetNamespace(), "", gsaNameFor(instance.GetName()), wal.BucketName, rciam.StorageBucketViewerRole),
		rciam.CreateStorageBucketPolicyMember(recoverySourcePolicyNameFor(instance.GetName()), instance.GetNamespace(), "", gsaNameFor(instance.GetName()), recovery.BucketName, rciam.StorageObjectViewerRole),
		&storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: wal.BucketName, Namespace: instance.GetNamespace()}},
	}
	for _, resource := range resources {
		if !configConnectorReady(relatedObjects.GetMatching(resource)) {
			return false
		}
	}
	objectStore := &barmanv1.ObjectStore{ObjectMeta: metav1.ObjectMeta{Name: wal.BucketName, Namespace: instance.GetNamespace()}}
	return relatedObjects.GetMatching(objectStore) != nil
}

func configConnectorReady(object client.Object) bool {
	var conditions []metav1.Condition
	var observedGeneration int64
	switch resource := object.(type) {
	case *iamcnrm.IAMServiceAccount:
		conditions = resource.Status.Conditions
		observedGeneration = resource.Status.ObservedGeneration
	case *iamcnrm.IAMPolicyMember:
		conditions = resource.Status.Conditions
		observedGeneration = resource.Status.ObservedGeneration
	case *storagecnrm.StorageBucket:
		conditions = resource.Status.Conditions
		if resource.Status.ObservedGeneration == nil {
			return false
		}
		observedGeneration = *resource.Status.ObservedGeneration
	default:
		return false
	}
	if object.GetGeneration() != observedGeneration {
		return false
	}
	for _, condition := range conditions {
		if condition.Type == "Ready" && condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

func (r *PostgresBranchReconciler) policyMemberAction(desired *iamcnrm.IAMPolicyMember, owner *v1.PostgresBranch, relatedObjects reconciler.RelatedObjects) (action.Action, error) {
	existing := relatedObjects.GetMatching(desired)
	if existing == nil {
		return action.Create(desired, owner, cnrmConditionsGetter, r.Recorder), nil
	}
	if iamPolicyHasChanges(desired, existing.(*iamcnrm.IAMPolicyMember)) {
		return r.recreateIAM(desired, owner)
	}
	return action.Claim(desired, owner, cnrmConditionsGetter, r.Recorder), nil
}

func (r *PostgresBranchReconciler) recreateIAM(desired client.Object, owner *v1.PostgresBranch) (action.Action, error) {
	if !r.Config.ResyncIAMPermissions {
		return nil, fmt.Errorf("want to change %T %s, but configuration does not allow recreate", desired, client.ObjectKeyFromObject(desired))
	}
	return action.Recreate(desired, owner, cnrmConditionsGetter, r.Recorder), nil
}

func (r *PostgresBranchReconciler) Delete(obj *v1.PostgresBranch, prep PostgresBranchPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	if prep.ActiveBranch != "" && obj.Spec.BranchName == prep.ActiveBranch && !prep.PostgresDeleting {
		r.Recorder.RecordEvent(obj, corev1.EventTypeWarning, "DeleteBlocked", "deletion blocked: branch is the active PostgresBranch")
		return nil, ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	if len(prep.BlockingRecoveries) > 0 {
		r.Recorder.RecordEvent(obj, corev1.EventTypeWarning, "DeleteBlocked", "deletion blocked: recovery branches still depend on this branch's archive: %v", prep.BlockingRecoveries)
		return nil, ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
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
