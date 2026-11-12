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

// PostgresClusterReconciler replaces both legacy controllers at runtime. There
// must never be a PostgresBranch controller running alongside this reconciler.
// The old renderers remain private compatibility adapters for stable identities.
type PostgresClusterReconciler struct {
	Config   *config.Config
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

type PostgresClusterPreparedData struct {
	Branch    *v1.PostgresBranch `yaml:"branch,omitempty"`
	Cluster   *cnpgv1.Cluster    `yaml:"cluster,omitempty"`
	Children  []client.Object    `yaml:"-"`
	ProjectID string             `yaml:"projectID,omitempty"`
	Adopting  bool               `yaml:"adopting,omitempty"`
}

var _ reconciler.Reconciler[*v1.Postgres, PostgresClusterPreparedData] = &PostgresClusterReconciler{}

func (r *PostgresClusterReconciler) Name() string      { return "postgres.nais.io" }
func (r *PostgresClusterReconciler) New() *v1.Postgres { return &v1.Postgres{} }
func (r *PostgresClusterReconciler) legacy() *PostgresBranchReconciler {
	return &PostgresBranchReconciler{Config: r.Config, Recorder: r.Recorder, Scheme: r.Scheme}
}
func (r *PostgresClusterReconciler) OwnedTypes() []reconciler.OwnedType {
	return append(r.legacy().OwnedTypes(), reconciler.OwnedType{Type: &v1.PostgresBranch{}})
}
func (r *PostgresClusterReconciler) AdditionalTypes() []client.Object { return nil }

// Physical resources are never deleted merely because a configuration or action
// list no longer mentions them. Delete uses a fresh, explicit ownership inventory.
func (r *PostgresClusterReconciler) PruneUnreferenced() bool { return false }
func (r *PostgresClusterReconciler) RelationshipWatches() []reconciler.RelationshipWatch {
	return (&PostgresReconciler{}).RelationshipWatches()
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
	// Non-main databases and recoveries require manual investigation. Freezing
	// them must not invoke legacy deletion or prune any of their resources.
	if pg.Spec.ActiveBranch != "" && pg.Spec.ActiveBranch != v1.DefaultBranchName || observedActiveBranch(pg) != v1.DefaultBranchName {
		return p, ctrl.Result{}, fmt.Errorf("automatic ownership transfer supports only main")
	}
	branch, err := readLegacyMain(ctx, reader, pg)
	if err != nil {
		return p, ctrl.Result{}, err
	}
	p.Branch = branch
	children, err := r.ownedChildren(ctx, reader, pg, p.Branch)
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
		if !physicalControlledBy(cluster, pg, postgresKind) && (p.Branch == nil || !physicalControlledBy(cluster, p.Branch, "PostgresBranch")) {
			return p, ctrl.Result{}, fmt.Errorf("CNPG cluster has unexpected ownership")
		}
		p.Cluster = cluster
	}
	if !pg.DeletionTimestamp.IsZero() {
		// Deletion is explicit and can finish an interrupted ownership transfer.
		return p, ctrl.Result{}, nil
	}
	if err := prepareOwnershipJournal(pg, p); err != nil {
		return p, ctrl.Result{}, err
	}
	journal := pg.Status.Ownership
	if err := r.checkInventory(journal, children); err != nil {
		return p, ctrl.Result{}, err
	}
	if p.Cluster != nil {
		journal.ClusterUID = string(p.Cluster.UID)
		for _, child := range children {
			if p.Branch != nil && (metav1.IsControlledBy(child, p.Branch) || ownership.NewOwnerManager("postgresbranch.nais.io/owner").HasOwnerAnnotation(child, p.Branch)) {
				p.Adopting = true
			}
		}
		if !p.Adopting {
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
	}
	// Adoption never depends on current builder configuration or namespace labels:
	// the first pass changes metadata only, even after configuration drift.
	if journal.Phase == ownershipAdopting || p.Branch != nil {
		return p, ctrl.Result{}, nil
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
	return p, ctrl.Result{}, nil
}

func readLegacyMain(ctx context.Context, reader client.Reader, pg *v1.Postgres) (*v1.PostgresBranch, error) {
	branches := &v1.PostgresBranchList{}
	if err := reader.List(ctx, branches, client.InNamespace(pg.Namespace)); err != nil {
		return nil, err
	}
	var main *v1.PostgresBranch
	for i := range branches.Items {
		branch := &branches.Items[i]
		if branch.Spec.Postgres != pg.Name && !metav1.IsControlledBy(branch, pg) {
			continue
		}
		if !validBranchIdentity(branch) || branch.Spec.Postgres != pg.Name || branch.Spec.BranchName != v1.DefaultBranchName || !postgresBranchOwnedBy(branch, pg) || branch.Spec.Bootstrap != nil {
			return nil, fmt.Errorf("PostgresBranch %q has unsupported identity, ownership or bootstrap", branch.Name)
		}
		main = branch
	}
	if journal := pg.Status.Ownership; journal != nil && journal.LegacyBranchUID != "" && main != nil && string(main.UID) != journal.LegacyBranchUID {
		return nil, fmt.Errorf("legacy branch was replaced")
	}
	return main, nil
}

func prepareOwnershipJournal(pg *v1.Postgres, p PostgresClusterPreparedData) error {
	if p.Cluster != nil && !p.Cluster.DeletionTimestamp.IsZero() {
		return fmt.Errorf("CNPG cluster is deleting")
	}
	journal := pg.Status.Ownership
	if journal == nil {
		if p.Branch == nil && (pg.Status.ActiveBranch != "" || pg.Status.ClusterName != "") {
			return fmt.Errorf("previously observed database has no branch or ownership checkpoint; refusing to provision a replacement")
		}
		journal = &v1.PostgresOwnership{Phase: ownershipProvisioning}
		if p.Branch != nil {
			if !p.Branch.DeletionTimestamp.IsZero() || p.Cluster == nil {
				return fmt.Errorf("legacy branch is deleting or its cluster is missing")
			}
			journal.Phase, journal.LegacyBranchUID = ownershipAdopting, string(p.Branch.UID)
		}
		pg.Status.Ownership = journal
	}
	if journal.Phase != ownershipProvisioning && journal.Phase != ownershipAdopting && journal.Phase != ownershipManaged {
		return fmt.Errorf("unknown ownership checkpoint phase %q", journal.Phase)
	}
	if journal.Phase == ownershipManaged && journal.ClusterUID == "" {
		return fmt.Errorf("managed ownership checkpoint has no cluster UID")
	}
	if journal.ClusterUID != "" && (p.Cluster == nil || string(p.Cluster.UID) != journal.ClusterUID) {
		return fmt.Errorf("checkpointed CNPG cluster disappeared or was replaced; refusing to recreate it")
	}
	if journal.Phase == ownershipAdopting && p.Branch == nil {
		return fmt.Errorf("legacy branch disappeared before adoption completed")
	}
	if p.Branch != nil && journal.LegacyBranchUID == "" {
		return fmt.Errorf("unexpected branch after direct provisioning")
	}
	return nil
}

// Inventory all known physical types even if WAL creation is now disabled, so
// configuration drift cannot hide an archive from adoption or detach checks.
func (r *PostgresClusterReconciler) physicalTypes() []reconciler.OwnedType {
	return postgresPhysicalOwnedTypes(true)
}

func (r *PostgresClusterReconciler) ownedChildren(ctx context.Context, reader client.Reader, pg *v1.Postgres, branch *v1.PostgresBranch) ([]client.Object, error) {
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
			if branch != nil {
				for _, ref := range child.GetOwnerReferences() {
					if ref.UID == branch.UID && !physicalControlledBy(child, branch, "PostgresBranch") {
						return fmt.Errorf("resource %s has an unexpected non-controller branch reference", child.GetName())
					}
				}
			}
			old := branch != nil && (metav1.IsControlledBy(child, branch) || oldOwners.HasOwnerAnnotation(child, branch))
			current := metav1.IsControlledBy(child, pg) || newOwners.HasOwnerAnnotation(child, pg)
			if !old && !current {
				return nil
			}
			for _, previous := range oldOwners.GetOwnerAnnotations(child) {
				if branch == nil || previous != client.ObjectKeyFromObject(branch).String() {
					return fmt.Errorf("resource %s has a foreign branch ownership annotation", child.GetName())
				}
			}
			for _, current := range newOwners.GetOwnerAnnotations(child) {
				if current != client.ObjectKeyFromObject(pg).String() {
					return fmt.Errorf("resource %s has a foreign Postgres ownership annotation", child.GetName())
				}
			}
			if !physicalControlledBy(child, pg, postgresKind) && (branch == nil || !physicalControlledBy(child, branch, "PostgresBranch")) {
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

func physicalControlledBy(child, owner client.Object, kind string) bool {
	ref := metav1.GetControllerOf(child)
	return ref != nil && owner.GetUID() != "" && ref.UID == owner.GetUID() && ref.Name == owner.GetName() && ref.Kind == kind && ref.APIVersion == v1.GroupVersion.String()
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
	for _, child := range p.Children {
		if p.Adopting && (metav1.IsControlledBy(child, p.Branch) || ownership.NewOwnerManager("postgresbranch.nais.io/owner").HasOwnerAnnotation(child, p.Branch)) {
			actions = append(actions, action.Adopt(child, p.Branch, pg, ownership.NewOwnerManager("postgresbranch.nais.io/owner"), existsConditionGetter, r.Recorder))
		}
	}
	if p.Adopting {
		return actions, ctrl.Result{RequeueAfter: time.Second}, nil
	}
	if p.Branch != nil {
		// No functional spec writes in the adoption/detach pass.
		actions = append(actions, &detachPostgresBranch{reconciler: r, postgres: pg.DeepCopy(), branch: p.Branch.DeepCopy()})
		return actions, ctrl.Result{RequeueAfter: time.Second}, nil
	}
	identity := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresPhysicalName(pg.Name), Namespace: pg.Namespace, Annotations: maps.Clone(pg.Annotations)}, Spec: v1.PostgresBranchSpec{Postgres: pg.Name, BranchName: v1.DefaultBranchName}}
	planned, result, err := r.legacy().physicalActions(identity, pg, PostgresBranchPreparedData{PostgresUID: pg.UID, PostgresName: pg.Name, PostgresSpec: pg.Spec, TeamGoogleProjectID: p.ProjectID}, related)
	if err != nil {
		return nil, result, err
	}
	for i, a := range planned {
		if existing := related.GetMatching(a.GetObject()); existing != nil && !physicalControlledBy(existing, pg, postgresKind) {
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
	var actions []action.Action
	for _, child := range p.Children {
		if child.GetDeletionTimestamp().IsZero() {
			actions = append(actions, action.DeleteIfExists(child, pg, existsConditionGetter, r.Recorder))
		}
	}
	if len(p.Children) > 0 {
		return actions, ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	if p.Branch != nil {
		actions = append(actions, &detachPostgresBranch{reconciler: r, postgres: pg.DeepCopy(), branch: p.Branch.DeepCopy()})
		return actions, ctrl.Result{RequeueAfter: time.Second}, nil
	}
	return nil, ctrl.Result{}, nil
}

// Detach removes only the retired branch's own finalizer, after a fresh UID and
// child inventory check. It never uses the legacy branch deletion/unclaim path.
type detachPostgresBranch struct {
	reconciler *PostgresClusterReconciler
	postgres   *v1.Postgres
	branch     *v1.PostgresBranch
}

func (a *detachPostgresBranch) GetObject() client.Object { return a.branch }
func (a *detachPostgresBranch) GetOwner() api.NaisObject { return a.postgres }
func (*detachPostgresBranch) SkipOwnership() bool        { return true }
func (a *detachPostgresBranch) Do(ctx context.Context, c client.Client, _ *runtime.Scheme, _ ownership.OwnerManager) error {
	pg := &v1.Postgres{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(a.postgres), pg); err != nil {
		return err
	}
	if pg.UID != a.postgres.UID {
		return fmt.Errorf("postgres was replaced before detach")
	}
	branch := &v1.PostgresBranch{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(a.branch), branch); err != nil {
		return client.IgnoreNotFound(err)
	}
	if branch.UID != a.branch.UID || !postgresBranchOwnedBy(branch, pg) {
		return fmt.Errorf("branch changed before detach")
	}
	if pg.DeletionTimestamp.IsZero() && (pg.Status == nil || pg.Status.Ownership == nil || pg.Status.Ownership.Phase != ownershipManaged || pg.Status.Ownership.LegacyBranchUID != string(branch.UID)) {
		return fmt.Errorf("adoption is not checkpointed")
	}
	children, err := a.reconciler.ownedChildren(ctx, c, pg, branch)
	if err != nil {
		return err
	}
	for _, child := range children {
		if metav1.IsControlledBy(child, branch) || ownership.NewOwnerManager("postgresbranch.nais.io/owner").HasOwnerAnnotation(child, branch) {
			return fmt.Errorf("branch still owns %s", client.ObjectKeyFromObject(child))
		}
	}
	if pg.DeletionTimestamp.IsZero() {
		// Recheck the persisted inventory immediately before allowing GC to
		// forget the legacy owner. Do not detach after a resource replacement.
		if err := a.reconciler.checkInventory(pg.Status.Ownership.DeepCopy(), children); err != nil {
			return err
		}
	}
	original := branch.DeepCopy()
	branch.Finalizers = slices.DeleteFunc(branch.Finalizers, func(f string) bool { return f == "postgresbranch.nais.io" })
	if !reflect.DeepEqual(original.Finalizers, branch.Finalizers) {
		if err := c.Patch(ctx, branch, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
	}
	uid := branch.UID
	return client.IgnoreNotFound(c.Delete(ctx, branch, client.Preconditions{UID: &uid}))
}
