package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	"github.com/nais/pgrator/pkg/api"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Metadata is deliberately used instead of status: the synchronizer owns the
// complete status document, whereas this reservation is written by dependents.
// Both deletionTimestamp and this annotation are serialized by the source's
// resourceVersion. A deletion racing the Update makes that admission conflict.
const recoveryAdmissionsAnnotation = "postgres.nais.io/recovery-admissions"

func recoveryAdmissions(source client.Object) (map[string]string, error) {
	admissions := map[string]string{}
	if data := source.GetAnnotations()[recoveryAdmissionsAnnotation]; data != "" {
		if err := json.Unmarshal([]byte(data), &admissions); err != nil {
			return nil, fmt.Errorf("invalid recovery admissions on source %q: %w", source.GetName(), err)
		}
		if admissions == nil {
			return nil, fmt.Errorf("recovery admissions must be an object")
		}
		for uid, name := range admissions {
			if uid == "" || name == "" {
				return nil, fmt.Errorf("recovery admission requires target UID and name")
			}
		}
	}
	return admissions, nil
}

func recoveryWasAccepted(source client.Object, target *v1.Postgres) (bool, error) {
	admissions, err := recoveryAdmissions(source)
	return target.UID != "" && admissions[string(target.UID)] == target.Name, err
}

type acceptPostgresRecovery struct {
	target    *v1.Postgres
	source    client.Object
	sourceUID types.UID
	finalizer string
}

func newAcceptPostgresRecovery(target *v1.Postgres, source client.Object, sourceUID types.UID, finalizer string) *acceptPostgresRecovery {
	return &acceptPostgresRecovery{
		target:    target,
		source:    source,
		sourceUID: sourceUID,
		finalizer: finalizer,
	}
}

func (a *acceptPostgresRecovery) GetObject() client.Object { return a.source }
func (a *acceptPostgresRecovery) GetOwner() api.NaisObject { return a.target }
func (*acceptPostgresRecovery) SkipOwnership() bool        { return true }
func (a *acceptPostgresRecovery) Do(ctx context.Context, c client.Client, _ *runtime.Scheme, _ ownership.OwnerManager) error {
	target := &v1.Postgres{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(a.target), target); err != nil {
		return err
	}
	if target.UID == "" || target.UID != a.target.UID || !rccnpg.IsRecoveredPostgres(target) || !target.DeletionTimestamp.IsZero() || !slices.Contains(target.Finalizers, "postgres.nais.io") {
		return fmt.Errorf("recovery target changed or has no durable cleanup finalizer")
	}
	source := a.source.DeepCopyObject().(client.Object)
	if err := c.Get(ctx, client.ObjectKeyFromObject(a.source), source); err != nil {
		return err
	}
	if source.GetUID() != a.sourceUID {
		return fmt.Errorf("recovery source was replaced before admission")
	}
	admissions, err := recoveryAdmissions(source)
	if err != nil {
		return err
	}
	if admissions[string(target.UID)] == target.Name {
		return nil
	}
	if admissions[string(target.UID)] != "" {
		return fmt.Errorf("recovery UID is already reserved for another target")
	}
	if !source.GetDeletionTimestamp().IsZero() || !slices.Contains(source.GetFinalizers(), a.finalizer) {
		return fmt.Errorf("recovery source is deleting or has no cleanup finalizer")
	}
	admissions[string(target.UID)] = target.Name
	data, err := json.Marshal(admissions)
	if err != nil {
		return err
	}
	annotations := source.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[recoveryAdmissionsAnnotation] = string(data)
	source.SetAnnotations(annotations)
	// Do not retry a conflict with this admission intent: re-read deletion state
	// and the registry in a new reconcile before trying again.
	return c.Update(ctx, source)
}

func pendingRecoveryAdmissions(ctx context.Context, reader client.Reader, source client.Object) (map[string]string, error) {
	admissions, err := recoveryAdmissions(source)
	if err != nil {
		return nil, err
	}
	pending := map[string]string{}
	for uid, name := range admissions {
		target := &v1.Postgres{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: source.GetNamespace(), Name: name}, target); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		if string(target.UID) != uid {
			continue
		}
		matches, err := targetMatchesRecoverySource(ctx, reader, target, source)
		if err != nil {
			return nil, err
		}
		if !matches {
			return nil, fmt.Errorf("accepted recovery %q no longer matches its source", name)
		}
		cluster := &cnpgv1.Cluster{}
		err = reader.Get(ctx, client.ObjectKey{Namespace: source.GetNamespace(), Name: rccnpg.ClusterNameFor(v1.PostgresPhysicalName(name))}, cluster)
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		if !recoveryFinished(target, cluster) || cluster.Spec.Bootstrap == nil || cluster.Spec.Bootstrap.Recovery != nil || len(cluster.Spec.ExternalClusters) != 0 {
			pending[uid] = name
		}
	}
	return pending, nil
}

func targetMatchesRecoverySource(ctx context.Context, reader client.Reader, target *v1.Postgres, source client.Object) (bool, error) {
	switch src := source.(type) {
	case *v1.Postgres:
		if target.Spec.Recovery != nil {
			return target.Spec.Recovery.SourceUID == string(src.UID) && target.Spec.Recovery.SourcePostgres == src.Name, nil
		}
		rec, err := getCompanionRecovery(ctx, reader, target)
		if err != nil {
			if apierrors.IsNotFound(err) && completedRecoveryClusterUID(target) != "" {
				return true, nil
			}
			return false, err
		}
		return rec != nil && rec.Spec.PointInTime != nil &&
			rec.Spec.PointInTime.SourceUID == string(src.UID) &&
			rec.Spec.PointInTime.SourcePostgres == src.Name, nil
	case *v1.PostgresBackup:
		rec, err := getCompanionRecovery(ctx, reader, target)
		if err != nil {
			if apierrors.IsNotFound(err) && completedRecoveryClusterUID(target) != "" {
				return true, nil
			}
			return false, err
		}
		return rec != nil && rec.Spec.Backup != nil &&
			rec.Spec.Backup.BackupUID == string(src.UID) &&
			rec.Spec.Backup.BackupName == src.Name, nil
	default:
		return false, fmt.Errorf("unsupported recovery source %T", source)
	}
}

func pruneRecoveryAdmissions(ctx context.Context, reader client.Reader, source client.Object) error {
	if source.GetAnnotations()[recoveryAdmissionsAnnotation] == "" {
		return nil
	}
	before, err := recoveryAdmissions(source)
	if err != nil {
		return err
	}
	pending, err := pendingRecoveryAdmissions(ctx, reader, source)
	if err != nil {
		return err
	}
	if maps.Equal(before, pending) {
		return nil
	}
	annotations := source.GetAnnotations()
	if len(pending) == 0 {
		delete(annotations, recoveryAdmissionsAnnotation)
		source.SetAnnotations(annotations)
		return nil
	}
	data, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[recoveryAdmissionsAnnotation] = string(data)
	source.SetAnnotations(annotations)
	return nil
}

// Source deletion must wait until every live dependent has its own backup and
// has detached the recovery inputs from its actual CNPG Cluster.
func blockingPostgresRecoveries(ctx context.Context, reader client.Reader, source client.Object) ([]string, error) {
	pending, err := pendingRecoveryAdmissions(ctx, reader, source)
	if err != nil {
		return nil, err
	}
	var blocking []string
	for _, name := range pending {
		blocking = append(blocking, name)
	}
	slices.Sort(blocking)
	return blocking, nil
}
