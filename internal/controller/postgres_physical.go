package controller

import (
	"context"
	"fmt"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	cnpgutils "github.com/cloudnative-pg/cloudnative-pg/pkg/utils"
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
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	instanceBucketNameMaxLength  = 63
	instanceBucketUIDSuffixLen   = 12
	gcpServiceAccountIDMaxLength = 30

	// physicalBranchLabel is the durable value of the postgres.nais.io/branch
	// pod label. Branches are retired, but metrics and Console still filter on it.
	physicalBranchLabel = v1.DefaultBranchName
)

// physicalRenderer builds the physical resources of one database: the CNPG
// Cluster, Pooler, Database and roles, network policies and, with WAL
// archiving, the archive bucket, IAM and backups.
type physicalRenderer struct {
	Config   *config.Config
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

// physicalIdentity names a database's physical resources. Name keeps the
// historical "<postgres>-main-<hash>" form (see v1.PostgresPhysicalName).
type physicalIdentity struct {
	Name          string
	Namespace     string
	Postgres      string
	CorrelationID string
}

// physicalInputs are the observed inputs the renderer needs besides the identity.
type physicalInputs struct {
	PostgresName        string
	PostgresUID         types.UID
	PostgresSpec        v1.PostgresSpec
	TeamGoogleProjectID string
	RecoverySource      *rccnpg.RecoverySource
	RecoverySourceReady bool
	RecoveryClusterUID  types.UID
}

func postgresPhysicalOwnedTypes(includeWAL bool) []reconciler.OwnedType {
	ownedTypes := []reconciler.OwnedType{
		{
			Type: &cnpgv1.Cluster{},
			AdditionalPredicate: predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldCluster, oldOK := e.ObjectOld.(*cnpgv1.Cluster)
					newCluster, newOK := e.ObjectNew.(*cnpgv1.Cluster)
					return oldOK && newOK && (oldCluster.Status.Phase != newCluster.Status.Phase ||
						!equality.Semantic.DeepEqual(metav1.GetControllerOf(oldCluster), metav1.GetControllerOf(newCluster)) ||
						oldCluster.DeletionTimestamp.IsZero() != newCluster.DeletionTimestamp.IsZero() ||
						oldCluster.Status.PhaseReason != newCluster.Status.PhaseReason ||
						!equality.Semantic.DeepEqual(oldCluster.Status.Conditions, newCluster.Status.Conditions) ||
						recoveryComplete(oldCluster) != recoveryComplete(newCluster) ||
						recoveryBackedUp(oldCluster) != recoveryBackedUp(newCluster))
				},
			},
		},
		{Type: &cnpgv1.DatabaseRole{}},
		{
			Type: &cnpgv1.Database{},
			AdditionalPredicate: predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldDatabase, oldOK := e.ObjectOld.(*cnpgv1.Database)
					newDatabase, newOK := e.ObjectNew.(*cnpgv1.Database)
					return oldOK && newOK && (oldDatabase.Status.ObservedGeneration != newDatabase.Status.ObservedGeneration ||
						!equality.Semantic.DeepEqual(oldDatabase.Status.Applied, newDatabase.Status.Applied) ||
						oldDatabase.Status.Message != newDatabase.Status.Message)
				},
			},
		},
		{Type: &cnpgv1.Pooler{}},
		{Type: &networkingv1.NetworkPolicy{}},
	}
	if includeWAL {
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

const recoveryJobRoleLabel = cnpgutils.JobRoleLabelName
const recoveryJobClusterLabel = cnpgutils.ClusterLabelName
const recoveryJobRole = "full-recovery"

func recoveryJobFailed(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func recoveryBootstrapFailed(ctx context.Context, reader client.Reader, cluster *cnpgv1.Cluster) (bool, error) {
	if cluster.Status.Phase != cnpgv1.PhaseUnrecoverable ||
		cluster.Spec.Bootstrap == nil || cluster.Spec.Bootstrap.Recovery == nil {
		return false, nil
	}
	jobs := &batchv1.JobList{}
	if err := reader.List(ctx, jobs, client.InNamespace(cluster.Namespace),
		client.MatchingLabels{
			recoveryJobRoleLabel:    recoveryJobRole,
			recoveryJobClusterLabel: cluster.Name,
		}); err != nil {
		return false, fmt.Errorf("listing recovery jobs for Cluster %q: %w", cluster.Name, err)
	}
	for i := range jobs.Items {
		job := &jobs.Items[i]
		if metav1.IsControlledBy(job, cluster) && recoveryJobFailed(job) {
			return true, nil
		}
	}
	return false, nil
}

// physicalActions renders every physical resource of a database and transfers
// controller ownership to its owning Postgres.
func (r *physicalRenderer) physicalActions(id physicalIdentity, owner api.NaisObject, prepared physicalInputs, relatedObjects reconciler.RelatedObjects) ([]action.Action, error) {
	if err := recoveryFailureError(owner); err != nil {
		return nil, err
	}
	clusterKey := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name:      rccnpg.ClusterNameFor(id.Name),
		Namespace: id.Namespace,
	}}
	existingCluster, _ := relatedObjects.GetMatching(clusterKey).(*cnpgv1.Cluster)
	if prepared.RecoverySource != nil {
		var existingUID types.UID
		if existingCluster != nil {
			existingUID = existingCluster.UID
		}
		if existingUID != prepared.RecoveryClusterUID {
			return nil, fmt.Errorf("recovery cluster %q changed since preparation", clusterKey.Name)
		}
	}

	specSource := &v1.Postgres{
		ObjectMeta: metav1.ObjectMeta{
			Name:      id.Name,
			Namespace: id.Namespace,
			UID:       prepared.PostgresUID,
			Annotations: map[string]string{
				api.DeploymentCorrelationIDAnnotation: id.CorrelationID,
			},
		},
		Spec: prepared.PostgresSpec,
	}

	wal := r.walArchive(id, prepared)
	actions, err := r.physicalRoleActions(id, owner)
	if err != nil {
		return nil, err
	}

	database, err := r.database(id, specSource, relatedObjects)
	if err != nil {
		return nil, err
	}
	if err := transferControllerOwnership(owner, database, r.Scheme); err != nil {
		return nil, err
	}
	actions = append(actions, action.CreateOrUpdate(database, owner, databaseConditionGetter, r.Recorder))

	pooler, err := rccnpg.CreatePooler(r.Scheme, specSource)
	if err != nil {
		return nil, fmt.Errorf("creating Pooler spec: %w", err)
	}
	// Billing identity belongs to the owning Postgres.
	pooler.Spec.Template.ObjectMeta.Labels["postgres.nais.io/name"] = id.Postgres
	if err := transferControllerOwnership(owner, pooler, r.Scheme); err != nil {
		return nil, err
	}
	actions = append(actions, action.CreateOrUpdate(pooler, owner, existsConditionGetter, r.Recorder))

	netpol, err := rcnetpol.Create(r.Scheme, specSource, rccnpg.ClusterNameFor(id.Name), r.Config.APIServerIP)
	if err != nil {
		return nil, fmt.Errorf("creating NetworkPolicy spec: %w", err)
	}
	if err := transferControllerOwnership(owner, netpol, r.Scheme); err != nil {
		return nil, err
	}
	actions = append(actions, action.CreateOrUpdate(netpol, owner, existsConditionGetter, r.Recorder))

	clusterExists := existingCluster != nil
	recovered := prepared.RecoverySource != nil && recoveryFinished(owner, existingCluster)
	recoveryInProgress := prepared.RecoverySource != nil && !recovered
	if wal.Enabled() {
		walActions, err := r.walActions(id, owner, prepared, wal, recoveryInProgress, relatedObjects, specSource)
		if err != nil {
			return nil, err
		}
		actions = append(actions, walActions...)
	}
	if prepared.RecoverySource == nil || clusterExists || prepared.RecoverySourceReady && recoveryInfrastructureReady(id, wal, prepared.RecoverySource, relatedObjects) {
		recoverySource := prepared.RecoverySource
		if recovered {
			// The source archive belongs to another database and may have been
			// deleted. Once recovered, replicas and backups use this database's own archive.
			recoverySource = nil
		}
		cluster, err := rccnpg.CreateCluster(r.Scheme, specSource, r.Config, wal, recoverySource, database)
		if err != nil {
			return nil, fmt.Errorf("creating CNPG Cluster spec: %w", err)
		}
		cluster.Labels["postgres.nais.io/name"] = id.Postgres
		cluster.Labels["postgres.nais.io/branch"] = physicalBranchLabel
		cluster.Spec.InheritedMetadata.Labels["postgres.nais.io/name"] = id.Postgres
		cluster.Spec.InheritedMetadata.Labels["postgres.nais.io/branch"] = physicalBranchLabel
		// postInitSQL, which creates the app_readwritecreate group role, runs only
		// at initdb. Mark the cluster as readwritecreate-capable only when this
		// reconcile creates a fresh initdb cluster; once set, the marker is
		// preserved across reconciles. Clusters that predate the role and clusters
		// bootstrapped by recovery stay unmarked.
		freshInitDBCluster := !clusterExists && prepared.RecoverySource == nil
		if freshInitDBCluster || rccnpg.ReadWriteCreateCapable(existingCluster) {
			metav1.SetMetaDataAnnotation(&cluster.ObjectMeta, rccnpg.ReadWriteCreateCapableAnnotation, "true")
		}
		if err := transferControllerOwnership(owner, cluster, r.Scheme); err != nil {
			return nil, err
		}
		if prepared.RecoverySource != nil {
			if clusterExists {
				actions = append(actions, action.UpdateSameUID(cluster, owner, prepared.RecoveryClusterUID, clusterConditionGetter, r.Recorder))
			} else {
				actions = append(actions, action.Create(cluster, owner, clusterConditionGetter, r.Recorder))
			}
		} else if clusterExists && existingCluster.UID != "" {
			actions = append(actions, action.UpdateSameUID(cluster, owner, existingCluster.UID, clusterConditionGetter, r.Recorder))
		} else {
			actions = append(actions, action.CreateOrUpdate(cluster, owner, clusterConditionGetter, r.Recorder))
		}
	}

	return actions, nil
}

func (r *physicalRenderer) physicalRoleActions(id physicalIdentity, owner api.NaisObject) ([]action.Action, error) {
	actions := make([]action.Action, 0, 2)
	for _, create := range []func(physicalIdentity) *cnpgv1.DatabaseRole{createPersonalAccessGroupRole, createDurableOwnerRole} {
		role := create(id)
		if pg, direct := owner.(*v1.Postgres); direct && role.Spec.Name == rccnpg.PersonalAccessRole(id.Name) {
			role.Spec.Name = rccnpg.PersonalAccessRoleForPostgres(id.Name, pg)
		}
		if err := transferControllerOwnership(owner, role, r.Scheme); err != nil {
			return nil, err
		}
		actions = append(actions, action.CreateOrUpdate(role, owner, existsConditionGetter, r.Recorder))
	}
	return actions, nil
}

func (r *physicalRenderer) database(id physicalIdentity, specSource *v1.Postgres, relatedObjects reconciler.RelatedObjects) (*cnpgv1.Database, error) {
	existing, _ := relatedObjects.GetMatching(&cnpgv1.Database{ObjectMeta: metav1.ObjectMeta{
		Name:      rccnpg.DatabaseResourceName(specSource),
		Namespace: id.Namespace,
	}}).(*cnpgv1.Database)
	database, err := rccnpg.CreateDatabase(r.Scheme, specSource, existing)
	if err != nil {
		return nil, fmt.Errorf("creating Database spec: %w", err)
	}
	return database, nil
}

func databaseConditionGetter(obj client.Object, scheme *runtime.Scheme) []metav1.Condition {
	database, ok := obj.(*cnpgv1.Database)
	if !ok {
		return nil
	}
	condition := metav1.Condition{
		Type:               fmt.Sprintf("%s/Available", typePrefix(obj, scheme)),
		Status:             metav1.ConditionUnknown,
		ObservedGeneration: obj.GetGeneration(),
		Reason:             "Pending",
		Message:            "Waiting for CloudNativePG to reconcile the Database",
	}
	if database.Status.Applied == nil || database.Status.ObservedGeneration < database.GetGeneration() {
		if database.Status.Message != "" {
			condition.Message = database.Status.Message
		}
		return []metav1.Condition{condition}
	}
	if *database.Status.Applied {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Applied"
		condition.Message = "Database reconciled by CloudNativePG"
	} else {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "ReconciliationFailed"
		condition.Message = "CloudNativePG failed to reconcile the Database"
	}
	if database.Status.Message != "" {
		condition.Message = database.Status.Message
	}
	return []metav1.Condition{condition}
}

func createPersonalAccessGroupRole(id physicalIdentity) *cnpgv1.DatabaseRole {
	role := &cnpgv1.DatabaseRole{
		TypeMeta: metav1.TypeMeta{Kind: "DatabaseRole", APIVersion: cnpgv1.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rccnpg.ClusterNameFor(id.Name) + "-personal-access",
			Namespace: id.Namespace,
		},
		Spec: cnpgv1.DatabaseRoleSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: rccnpg.ClusterNameFor(id.Name)},
			ReclaimPolicy: cnpgv1.DatabaseRoleReclaimDelete,
			RoleConfiguration: cnpgv1.RoleConfiguration{
				Name:            rccnpg.PersonalAccessRole(id.Name),
				Login:           false,
				DisablePassword: true,
				Comment:         "Authentication group for personal Postgres access",
			},
		},
	}
	return role
}

func createDurableOwnerRole(id physicalIdentity) *cnpgv1.DatabaseRole {
	role := &cnpgv1.DatabaseRole{
		TypeMeta: metav1.TypeMeta{Kind: "DatabaseRole", APIVersion: cnpgv1.SchemeGroupVersion.String()},
		ObjectMeta: metav1.ObjectMeta{
			Name:      rccnpg.ClusterNameFor(id.Name) + "-app",
			Namespace: id.Namespace,
			Labels: map[string]string{
				"postgres.nais.io/name": id.Postgres,
			},
		},
		Spec: cnpgv1.DatabaseRoleSpec{
			ClusterRef:    corev1.LocalObjectReference{Name: rccnpg.ClusterNameFor(id.Name)},
			ReclaimPolicy: cnpgv1.DatabaseRoleReclaimDelete,
			RoleConfiguration: cnpgv1.RoleConfiguration{
				Name:  rccnpg.OwnerRole,
				Login: true,
				// Durable SQL role comment; kept unchanged so existing roles are not rewritten.
				Comment: fmt.Sprintf("Managed by pgrator for PostgresBranch %q", id.Name),
			},
			ClientCertificate: &cnpgv1.ClientCertificateConfiguration{Enabled: new(true)},
		},
	}
	if id.CorrelationID != "" {
		role.Annotations = map[string]string{api.DeploymentCorrelationIDAnnotation: id.CorrelationID}
	}
	return role
}

func transferControllerOwnership(owner client.Object, object client.Object, scheme *runtime.Scheme) error {
	object.SetOwnerReferences(nil)
	if err := controllerutil.SetControllerReference(owner, object, scheme); err != nil {
		return fmt.Errorf("setting controller reference on %T %s: %w", object, client.ObjectKeyFromObject(object), err)
	}
	return nil
}

func (r *physicalRenderer) walArchivingEnabled() bool {
	return r.Config.CNPG.WalBucketPrefix != ""
}

func (r *physicalRenderer) walArchive(id physicalIdentity, prepared physicalInputs) rccnpg.WALArchive {
	if !r.walArchivingEnabled() {
		return rccnpg.WALArchive{}
	}
	return rccnpg.WALArchive{
		GSAName:       gsaNameFor(id.Name),
		TeamProjectID: prepared.TeamGoogleProjectID,
		BucketName:    r.bucketName(id, prepared),
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

func (r *physicalRenderer) bucketName(id physicalIdentity, prepared physicalInputs) string {
	if !r.walArchivingEnabled() {
		return ""
	}

	uid := string(prepared.PostgresUID)
	uid = strings.ReplaceAll(uid, "-", "")
	if len(uid) > instanceBucketUIDSuffixLen {
		uid = uid[:instanceBucketUIDSuffixLen]
	}

	return r.bucketNameForInstanceName(id.Namespace, id.Name, types.UID(uid))
}

func (r *physicalRenderer) bucketNameForInstanceName(namespace, instanceName string, postgresUID types.UID) string {
	uid := strings.ReplaceAll(string(postgresUID), "-", "")
	if len(uid) > instanceBucketUIDSuffixLen {
		uid = uid[:instanceBucketUIDSuffixLen]
	}
	prefix := strings.Trim(r.Config.CNPG.WalBucketPrefix, "-")
	maxBaseLength := instanceBucketNameMaxLength - len(uid) - 1
	base := bucketNameBase(prefix, namespace, instanceName, maxBaseLength)
	return fmt.Sprintf("%s-%s", base, uid)
}

func (r *physicalRenderer) walActions(id physicalIdentity, owner api.NaisObject, prepared physicalInputs, wal rccnpg.WALArchive, needsRecoverySourceAccess bool, relatedObjects reconciler.RelatedObjects, specSource *v1.Postgres) ([]action.Action, error) {
	actions := make([]action.Action, 0, 8)

	gsa := rciam.CreateIAMServiceAccount(wal.GSAName, id.Namespace)
	if err := controllerutil.SetControllerReference(owner, gsa, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on IAMServiceAccount: %w", err)
	}
	switch existing := relatedObjects.GetMatching(gsa); {
	case existing == nil:
		actions = append(actions, action.Create(gsa, owner, cnrmConditionsGetter, r.Recorder))
	case iamServiceAccountHasChanges(gsa, existing.(*iamcnrm.IAMServiceAccount)):
		recreate, err := r.recreateIAM(gsa, owner)
		if err != nil {
			return nil, err
		}
		actions = append(actions, recreate)
	default:
		actions = append(actions, action.Claim(gsa, owner, cnrmConditionsGetter, r.Recorder))
	}

	wiPolicy := rciam.CreateWorkloadIdentityPolicyMember(
		workloadIdentityPolicyNameFor(id.Name),
		id.Namespace,
		id.Namespace,
		r.Config.GoogleProjectID,
		wal.GSAName,
		rccnpg.ClusterNameFor(id.Name),
	)
	if err := controllerutil.SetControllerReference(owner, wiPolicy, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on Workload Identity IAMPolicyMember: %w", err)
	}
	wiAction, err := r.policyMemberAction(wiPolicy, owner, relatedObjects)
	if err != nil {
		return nil, err
	}
	actions = append(actions, wiAction)

	bucket := rcstorage.CreateStorageBucket(specSource, wal.BucketName, r.Config.Google.Location)
	if err := transferControllerOwnership(owner, bucket, r.Scheme); err != nil {
		return nil, err
	}
	if existing := relatedObjects.GetMatching(bucket); existing != nil {
		copyCnrmAnnotations(existing, bucket)
		bucket.Spec.ResourceID = existing.(*storagecnrm.StorageBucket).Spec.ResourceID
		actions = append(actions, action.Update(bucket, owner, cnrmConditionsGetter, r.Recorder))
	} else {
		actions = append(actions, action.Create(bucket, owner, cnrmConditionsGetter, r.Recorder))
	}

	bucketPolicies := []struct {
		name string
		role string
	}{
		{name: storageBucketPolicyNameFor(id.Name), role: rciam.StorageObjectUserRole},
		{name: storageBucketViewerPolicyNameFor(id.Name), role: rciam.StorageBucketViewerRole},
	}
	for _, policy := range bucketPolicies {
		bucketPolicy := rciam.CreateStorageBucketPolicyMember(
			policy.name,
			id.Namespace,
			prepared.TeamGoogleProjectID,
			wal.GSAName,
			wal.BucketName,
			policy.role,
		)
		if err := controllerutil.SetControllerReference(owner, bucketPolicy, r.Scheme); err != nil {
			return nil, fmt.Errorf("setting controller reference on bucket IAMPolicyMember: %w", err)
		}
		bucketPolicyAction, err := r.policyMemberAction(bucketPolicy, owner, relatedObjects)
		if err != nil {
			return nil, err
		}
		actions = append(actions, bucketPolicyAction)
	}
	if needsRecoverySourceAccess {
		sourcePolicy := rciam.CreateStorageBucketPolicyMember(
			recoverySourcePolicyNameFor(id.Name),
			id.Namespace,
			prepared.TeamGoogleProjectID,
			wal.GSAName,
			prepared.RecoverySource.BucketName,
			rciam.StorageObjectViewerRole,
		)
		if err := controllerutil.SetControllerReference(owner, sourcePolicy, r.Scheme); err != nil {
			return nil, fmt.Errorf("setting controller reference on recovery source IAMPolicyMember: %w", err)
		}
		sourcePolicyAction, err := r.policyMemberAction(sourcePolicy, owner, relatedObjects)
		if err != nil {
			return nil, err
		}
		actions = append(actions, sourcePolicyAction)
	}

	objectStore := rcstorage.CreateObjectStore(wal.BucketName, metav1.ObjectMeta{
		Namespace: id.Namespace,
		Labels: map[string]string{
			rcstorage.OwnerNameLabel: id.Name,
		},
	}, prepared.PostgresSpec.BackupRetentionDays())
	if err := controllerutil.SetControllerReference(owner, objectStore, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on ObjectStore: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(objectStore, owner, existsConditionGetter, r.Recorder))

	backup := &cnpgv1.ScheduledBackup{ObjectMeta: metav1.ObjectMeta{
		Name:      rccnpg.ClusterNameFor(id.Name),
		Namespace: id.Namespace,
	}}
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name:      rccnpg.ClusterNameFor(id.Name),
		Namespace: id.Namespace,
	}}
	existingCluster, _ := relatedObjects.GetMatching(cluster).(*cnpgv1.Cluster)
	if continuousArchivingReady(existingCluster) || relatedObjects.GetMatching(backup) != nil {
		backup, err := rccnpg.CreateScheduledBackup(r.Scheme, specSource)
		if err != nil {
			return nil, fmt.Errorf("creating ScheduledBackup spec: %w", err)
		}
		if err := transferControllerOwnership(owner, backup, r.Scheme); err != nil {
			return nil, err
		}
		actions = append(actions, action.CreateOrUpdate(backup, owner, existsConditionGetter, r.Recorder))
	}

	fqdnPolicy, err := rcfqdnpolicy.Create(r.Scheme, specSource, rccnpg.ClusterNameFor(id.Name))
	if err != nil {
		return nil, fmt.Errorf("creating WAL FQDNNetworkPolicy spec: %w", err)
	}
	if err := transferControllerOwnership(owner, fqdnPolicy, r.Scheme); err != nil {
		return nil, err
	}
	actions = append(actions, action.CreateOrUpdate(fqdnPolicy, owner, existsConditionGetter, r.Recorder))

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

func recoveryInfrastructureReady(id physicalIdentity, wal rccnpg.WALArchive, recovery *rccnpg.RecoverySource, relatedObjects reconciler.RelatedObjects) bool {
	resources := []client.Object{
		rciam.CreateIAMServiceAccount(gsaNameFor(id.Name), id.Namespace),
		rciam.CreateWorkloadIdentityPolicyMember(workloadIdentityPolicyNameFor(id.Name), id.Namespace, id.Namespace, "", gsaNameFor(id.Name), rccnpg.ClusterNameFor(id.Name)),
		rciam.CreateStorageBucketPolicyMember(storageBucketPolicyNameFor(id.Name), id.Namespace, "", gsaNameFor(id.Name), wal.BucketName, rciam.StorageObjectUserRole),
		rciam.CreateStorageBucketPolicyMember(storageBucketViewerPolicyNameFor(id.Name), id.Namespace, "", gsaNameFor(id.Name), wal.BucketName, rciam.StorageBucketViewerRole),
		rciam.CreateStorageBucketPolicyMember(recoverySourcePolicyNameFor(id.Name), id.Namespace, "", gsaNameFor(id.Name), recovery.BucketName, rciam.StorageObjectViewerRole),
		&storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{Name: wal.BucketName, Namespace: id.Namespace}},
	}
	for _, resource := range resources {
		if !configConnectorReady(relatedObjects.GetMatching(resource)) {
			return false
		}
	}
	objectStore := &barmanv1.ObjectStore{ObjectMeta: metav1.ObjectMeta{Name: wal.BucketName, Namespace: id.Namespace}}
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

func (r *physicalRenderer) policyMemberAction(desired *iamcnrm.IAMPolicyMember, owner api.NaisObject, relatedObjects reconciler.RelatedObjects) (action.Action, error) {
	existing := relatedObjects.GetMatching(desired)
	if existing == nil {
		return action.Create(desired, owner, cnrmConditionsGetter, r.Recorder), nil
	}
	if iamPolicyHasChanges(desired, existing.(*iamcnrm.IAMPolicyMember)) {
		return r.recreateIAM(desired, owner)
	}
	return action.Claim(desired, owner, cnrmConditionsGetter, r.Recorder), nil
}

func (r *physicalRenderer) recreateIAM(desired client.Object, owner api.NaisObject) (action.Action, error) {
	if _, direct := owner.(*v1.Postgres); direct {
		// During the ownership transition resource identities are pinned. A
		// general replacement protocol is deliberately outside this migration.
		return nil, fmt.Errorf("refusing IAM recreation for directly managed Postgres: %T %s", desired, client.ObjectKeyFromObject(desired))
	}
	if !r.Config.ResyncIAMPermissions {
		return nil, fmt.Errorf("want to change %T %s, but configuration does not allow recreate", desired, client.ObjectKeyFromObject(desired))
	}
	return action.Recreate(desired, owner, cnrmConditionsGetter, r.Recorder), nil
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
