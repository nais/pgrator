package controller

import (
	"context"
	"fmt"
	"maps"
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
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

const (
	ownershipProvisioning = "Provisioning"
	ownershipAdopting     = "Adopting"
	ownershipManaged      = "Managed"
)

// PostgresClusterReconciler manages already-adopted and newly provisioned
// databases without the PostgresBranch API. Complete adoption with the previous
// release first. Shared renderers retain the historical physical identities.
type PostgresClusterReconciler struct {
	Config   *config.Config
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

type PostgresClusterPreparedData struct {
	Cluster             *cnpgv1.Cluster        `yaml:"cluster,omitempty"`
	Children            []client.Object        `yaml:"-"`
	ProjectID           string                 `yaml:"projectID,omitempty"`
	RecoverySource      *rccnpg.RecoverySource `yaml:"recoverySource,omitempty"`
	RecoverySourceReady bool                   `yaml:"recoverySourceReady,omitempty"`
	BlockingRecoveries  []string               `yaml:"blockingRecoveries,omitempty"`
}

var _ reconciler.Reconciler[*v1.Postgres, PostgresClusterPreparedData] = &PostgresClusterReconciler{}

func (r *PostgresClusterReconciler) Name() string      { return "postgres.nais.io" }
func (r *PostgresClusterReconciler) New() *v1.Postgres { return &v1.Postgres{} }
func (r *PostgresClusterReconciler) legacy() *PostgresBranchReconciler {
	return &PostgresBranchReconciler{Config: r.Config, Recorder: r.Recorder, Scheme: r.Scheme}
}
func (r *PostgresClusterReconciler) OwnedTypes() []reconciler.OwnedType {
	return r.legacy().OwnedTypes()
}
func (r *PostgresClusterReconciler) AdditionalTypes() []client.Object { return nil }

// Physical resources are never deleted merely because a configuration or action
// list no longer mentions them. Delete uses a fresh, explicit ownership inventory.
func (r *PostgresClusterReconciler) PruneUnreferenced() bool { return false }
func (r *PostgresClusterReconciler) RelationshipWatches() []reconciler.RelationshipWatch {
	return nil
}
func (r *PostgresClusterReconciler) MetricsLabels(pg *v1.Postgres) map[string]string {
	return (&PostgresReconciler{}).MetricsLabels(pg)
}
func (r *PostgresClusterReconciler) StatusCheckpointRequired(current, original *v1.Postgres) bool {
	return !reflect.DeepEqual(current.Status, original.Status)
}

func (r *PostgresClusterReconciler) Prepare(ctx context.Context, reader client.Reader, pg *v1.Postgres) (PostgresClusterPreparedData, ctrl.Result, error) {
	p := PostgresClusterPreparedData{}
	pg.GetStatus()
	// This release cannot finish partial transfers, including on deletion. Do
	// not clean only the transferred subset and strand the legacy owner's children.
	if pg.Status.Ownership != nil && pg.Status.Ownership.Phase == ownershipAdopting {
		return p, ctrl.Result{}, fmt.Errorf("ownership transfer is unfinished; complete adoption with the previous release")
	}
	meta.RemoveStatusCondition(&pg.Status.Conditions, "postgresbranch.nais.io/Available")
	// Non-main databases and recoveries require manual investigation. Freezing
	// them must not invoke legacy deletion or prune any of their resources.
	if pg.Spec.ActiveBranch != "" && pg.Spec.ActiveBranch != v1.DefaultBranchName || observedActiveBranch(pg) != v1.DefaultBranchName {
		return p, ctrl.Result{}, fmt.Errorf("automatic ownership transfer supports only main")
	}
	children, err := r.ownedChildren(ctx, reader, pg)
	if err != nil {
		return p, ctrl.Result{}, err
	}
	p.Children = children
	cluster := &cnpgv1.Cluster{}
	err = reader.Get(ctx, client.ObjectKey{Namespace: pg.Namespace, Name: rccnpg.ClusterNameFor(v1.PostgresPhysicalName(pg.Name))}, cluster)
	if err != nil && !apierrors.IsNotFound(err) {
		return p, ctrl.Result{}, err
	}
	if err == nil {
		if !physicalControlledBy(cluster, pg) {
			return p, ctrl.Result{}, fmt.Errorf("CNPG cluster has unexpected ownership")
		}
		p.Cluster = cluster
	}
	if !pg.DeletionTimestamp.IsZero() {
		// Explicit deletion is supported only after the transfer guard above.
		p.BlockingRecoveries, err = blockingPostgresRecoveries(ctx, reader, pg)
		return p, ctrl.Result{}, err
	}
	if err := prepareOwnershipJournal(pg, p); err != nil {
		return p, ctrl.Result{}, err
	}
	journal := pg.Status.Ownership
	inventory := slices.DeleteFunc(slices.Clone(children), func(child client.Object) bool { return isPostgresRecoveryGrant(pg, child) })
	if err := r.checkInventory(journal, inventory); err != nil {
		return p, ctrl.Result{}, err
	}
	if p.Cluster != nil {
		journal.ClusterUID = string(p.Cluster.UID)
		journal.Phase = ownershipManaged
		pg.Status.ClusterName = p.Cluster.Name
		pg.Status.ActiveBranch = v1.DefaultBranchName
		pg.Status.Configuration = &v1.PostgresConfiguration{Parameters: maps.Clone(p.Cluster.Spec.PostgresConfiguration.Parameters)}
		if pg.Status.Configuration.Parameters == nil {
			pg.Status.Configuration.Parameters = map[string]string{}
		}
		for _, condition := range clusterConditionGetter(p.Cluster, r.Scheme) {
			pg.Status.SetCondition(condition)
		}
	}
	if r.legacy().walArchivingEnabled() {
		namespace := &corev1.Namespace{}
		if err := reader.Get(ctx, client.ObjectKey{Name: pg.Namespace}, namespace); err != nil {
			return p, ctrl.Result{}, err
		}
		p.ProjectID = namespace.Labels[ProjectIDLabel]
		if p.ProjectID == "" {
			p.ProjectID = namespace.Annotations[ProjectIDAnnotationFallback]
		}
		if p.ProjectID == "" {
			return p, ctrl.Result{}, fmt.Errorf("namespace %q has no Google project identity", pg.Namespace)
		}
	}
	if err := r.prepareDirectRecovery(ctx, reader, pg, &p); err != nil {
		return p, ctrl.Result{}, err
	}
	return p, ctrl.Result{}, nil
}

func prepareOwnershipJournal(pg *v1.Postgres, p PostgresClusterPreparedData) error {
	if p.Cluster != nil && !p.Cluster.DeletionTimestamp.IsZero() {
		return fmt.Errorf("CNPG cluster is deleting")
	}
	journal := pg.Status.Ownership
	if journal == nil {
		if pg.Status.ActiveBranch != "" || pg.Status.ClusterName != "" || p.Cluster != nil {
			return fmt.Errorf("previously observed database has no ownership checkpoint; complete adoption before using this release")
		}
		journal = &v1.PostgresOwnership{Phase: ownershipProvisioning}
		pg.Status.Ownership = journal
	}
	if journal.Phase != ownershipProvisioning && journal.Phase != ownershipManaged {
		return fmt.Errorf("unknown ownership checkpoint phase %q", journal.Phase)
	}
	if journal.Phase == ownershipManaged && journal.ClusterUID == "" {
		return fmt.Errorf("managed ownership checkpoint has no cluster UID")
	}
	if journal.ClusterUID != "" && (p.Cluster == nil || string(p.Cluster.UID) != journal.ClusterUID) {
		return fmt.Errorf("checkpointed CNPG cluster disappeared or was replaced; refusing to recreate it")
	}
	return nil
}

// Inventory all known physical types even if WAL creation is now disabled, so
// configuration drift cannot hide an archive from adoption or detach checks.
func (r *PostgresClusterReconciler) physicalTypes() []reconciler.OwnedType {
	return postgresPhysicalOwnedTypes(true)
}

func (r *PostgresClusterReconciler) ownedChildren(ctx context.Context, reader client.Reader, pg *v1.Postgres) ([]client.Object, error) {
	var children []client.Object
	oldOwners := ownership.NewOwnerManager("postgresbranch.nais.io/owner")
	newOwners := ownership.NewOwnerManager(r.Name() + "/owner")
	for _, typ := range r.physicalTypes() {
		gvk, err := apiutil.GVKForObject(typ.Type, r.Scheme)
		if err != nil {
			return nil, err
		}
		listObj, err := r.Scheme.New(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
		if err != nil {
			return nil, err
		}
		list := listObj.(client.ObjectList)
		if err := reader.List(ctx, list, client.InNamespace(pg.Namespace)); err != nil {
			if meta.IsNoMatchError(err) {
				continue
			}
			return nil, err
		}
		if err := meta.EachListItem(list, func(object runtime.Object) error {
			child := object.(client.Object)
			current := metav1.IsControlledBy(child, pg) || newOwners.HasOwnerAnnotation(child, pg)
			if !current {
				return nil
			}
			if len(oldOwners.GetOwnerAnnotations(child)) > 0 {
				return fmt.Errorf("resource %s still has legacy branch ownership", child.GetName())
			}
			for _, current := range newOwners.GetOwnerAnnotations(child) {
				if current != client.ObjectKeyFromObject(pg).String() {
					return fmt.Errorf("resource %s has a foreign Postgres ownership annotation", child.GetName())
				}
			}
			if !physicalControlledBy(child, pg) {
				return fmt.Errorf("resource %s has inconsistent controller/annotation ownership", client.ObjectKeyFromObject(child))
			}
			if child.GetUID() == "" {
				return fmt.Errorf("resource %s has no UID", client.ObjectKeyFromObject(child))
			}
			child.GetObjectKind().SetGroupVersionKind(gvk)
			children = append(children, child)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(children, func(a, b client.Object) int { return strings.Compare(resourceKey(a), resourceKey(b)) })
	return children, nil
}

func resourceKey(obj client.Object) string {
	return obj.GetObjectKind().GroupVersionKind().String() + "/" + obj.GetName()
}

func physicalControlledBy(child, owner client.Object) bool {
	ref := metav1.GetControllerOf(child)
	return ref != nil && owner.GetUID() != "" && ref.UID == owner.GetUID() && ref.Name == owner.GetName() && ref.Kind == postgresKind && ref.APIVersion == v1.GroupVersion.String()
}

func (r *PostgresClusterReconciler) checkInventory(journal *v1.PostgresOwnership, children []client.Object) error {
	for _, ref := range journal.Resources {
		found := false
		for _, child := range children {
			gvk := child.GetObjectKind().GroupVersionKind()
			if gvk.GroupVersion().String() == ref.APIVersion && gvk.Kind == ref.Kind && child.GetName() == ref.Name {
				if string(child.GetUID()) != ref.UID || !child.GetDeletionTimestamp().IsZero() {
					return fmt.Errorf("checkpointed resource %s was replaced or is deleting", ref.Name)
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("checkpointed resource %s disappeared; refusing to recreate it", ref.Name)
		}
	}
	resources := make([]v1.PostgresOwnedResource, 0, len(children))
	for _, child := range children {
		if !child.GetDeletionTimestamp().IsZero() {
			return fmt.Errorf("resource %s is deleting", child.GetName())
		}
		gvk := child.GetObjectKind().GroupVersionKind()
		resources = append(resources, v1.PostgresOwnedResource{APIVersion: gvk.GroupVersion().String(), Kind: gvk.Kind, Name: child.GetName(), UID: string(child.GetUID())})
	}
	// Prepare errors still result in a deferred status write. Never truncate the
	// durable inventory unless the entire replacement inventory is valid.
	journal.Resources = resources
	return nil
}

func (r *PostgresClusterReconciler) Update(pg *v1.Postgres, p PostgresClusterPreparedData, related reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	actions := []action.Action{&protectPostgres{postgres: pg}}
	identity := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresPhysicalName(pg.Name), Namespace: pg.Namespace, Annotations: maps.Clone(pg.Annotations)}, Spec: v1.PostgresBranchSpec{Postgres: pg.Name, BranchName: v1.DefaultBranchName}}
	prepared := PostgresBranchPreparedData{PostgresUID: pg.UID, PostgresName: pg.Name, PostgresSpec: pg.Spec, TeamGoogleProjectID: p.ProjectID, RecoverySource: p.RecoverySource, RecoverySourceReady: p.RecoverySourceReady}
	if p.Cluster != nil {
		prepared.RecoveryClusterUID = p.Cluster.UID
	}
	planned, result, err := r.legacy().physicalActions(identity, pg, prepared, related)
	if err != nil {
		return nil, result, err
	}
	for i, a := range planned {
		if existing := related.GetMatching(a.GetObject()); existing != nil && !physicalControlledBy(existing, pg) {
			return nil, ctrl.Result{}, fmt.Errorf("desired resource %s is controlled by another resource", client.ObjectKeyFromObject(existing))
		}
		if existing := related.GetMatching(a.GetObject()); existing != nil {
			annotations := maps.Clone(existing.GetAnnotations())
			if annotations == nil {
				annotations = map[string]string{}
			}
			maps.Copy(annotations, a.GetObject().GetAnnotations())
			a.GetObject().SetAnnotations(annotations)
			labels := maps.Clone(existing.GetLabels())
			if labels == nil {
				labels = map[string]string{}
			}
			maps.Copy(labels, a.GetObject().GetLabels())
			a.GetObject().SetLabels(labels)
		}
		gvk, err := apiutil.GVKForObject(a.GetObject(), r.Scheme)
		if err != nil {
			return nil, ctrl.Result{}, err
		}
		if pg.Status != nil && pg.Status.Ownership != nil {
			for _, ref := range pg.Status.Ownership.Resources {
				if ref.APIVersion == gvk.GroupVersion().String() && ref.Kind == gvk.Kind && ref.Name == a.GetObject().GetName() {
					// Use the journal, not the later related-object snapshot. The
					// execution guard also prevents CreateOrUpdate fallback to create.
					planned[i] = action.RequireUID(a, types.UID(ref.UID))
					break
				}
			}
		}
	}
	// Do not revoke access in the same pass that removes CNPG's source inputs:
	// wait until the actual Cluster confirms they are gone.
	if recoveryFinished(pg, p.Cluster) && p.Cluster.Spec.Bootstrap != nil && p.Cluster.Spec.Bootstrap.Recovery == nil && len(p.Cluster.Spec.ExternalClusters) == 0 {
		for _, child := range p.Children {
			if isPostgresRecoveryGrant(pg, child) && child.GetDeletionTimestamp().IsZero() {
				planned = append(planned, action.DeleteIfExists(child, pg, existsConditionGetter, r.Recorder))
			}
		}
	}
	if pg.Spec.Recovery != nil && !recoveryFinished(pg, p.Cluster) {
		// CNPG's Job status can change without a Cluster update. Poll while
		// bootstrapping as well as reacting to the ordinary owned-resource watches.
		result.RequeueAfter = 10 * time.Second
	}
	return append(actions, planned...), result, nil
}

// Persist the parent finalizer before creating or adopting the first child. A
// crash after a child write must not leave the database without deletion guards.
type protectPostgres struct{ postgres *v1.Postgres }

func (a *protectPostgres) GetObject() client.Object { return a.postgres }
func (a *protectPostgres) GetOwner() api.NaisObject { return a.postgres }
func (*protectPostgres) SkipOwnership() bool        { return true }
func (a *protectPostgres) Do(ctx context.Context, c client.Client, _ *runtime.Scheme, _ ownership.OwnerManager) error {
	current := &v1.Postgres{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(a.postgres), current); err != nil {
		return err
	}
	if current.UID != a.postgres.UID || !current.DeletionTimestamp.IsZero() {
		return fmt.Errorf("postgres changed before physical reconciliation")
	}
	if current.ResourceVersion != a.postgres.ResourceVersion {
		return fmt.Errorf("postgres was updated after preparation; retry before changing children")
	}
	if !slices.Contains(current.Finalizers, "postgres.nais.io") {
		original := current.DeepCopy()
		current.Finalizers = append(current.Finalizers, "postgres.nais.io")
		if err := c.Patch(ctx, current, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
	}
	a.postgres.ResourceVersion = current.ResourceVersion
	a.postgres.Finalizers = slices.Clone(current.Finalizers)
	return nil
}

func (r *PostgresClusterReconciler) Delete(pg *v1.Postgres, p PostgresClusterPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	if len(p.BlockingRecoveries) > 0 {
		r.Recorder.RecordEvent(pg, corev1.EventTypeWarning, "DeleteBlocked", "recovery databases still depend on this archive: %v", p.BlockingRecoveries)
		return nil, ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	var actions []action.Action
	for _, child := range p.Children {
		if child.GetDeletionTimestamp().IsZero() {
			actions = append(actions, action.DeleteIfExists(child, pg, existsConditionGetter, r.Recorder))
		}
	}
	if len(p.Children) > 0 {
		return actions, ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	return nil, ctrl.Result{}, nil
}
