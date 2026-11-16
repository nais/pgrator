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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Metadata is deliberately used instead of status: the synchronizer owns the
// complete status document, whereas this reservation is written by dependents.
// Both deletionTimestamp and this annotation are serialized by the source's
// resourceVersion. A deletion racing the Update makes that admission conflict.
const recoveryAdmissionsAnnotation = "postgres.nais.io/recovery-admissions"

func recoveryAdmissions(source *v1.Postgres) (map[string]string, error) {
	admissions := map[string]string{}
	if data := source.Annotations[recoveryAdmissionsAnnotation]; data != "" {
		if err := json.Unmarshal([]byte(data), &admissions); err != nil {
			return nil, fmt.Errorf("invalid recovery admissions on source %q: %w", source.Name, err)
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

func recoveryWasAccepted(source, target *v1.Postgres) (bool, error) {
	admissions, err := recoveryAdmissions(source)
	return target.UID != "" && admissions[string(target.UID)] == target.Name, err
}

type acceptPostgresRecovery struct {
	target *v1.Postgres
	source *v1.Postgres
}

func newAcceptPostgresRecovery(target *v1.Postgres) *acceptPostgresRecovery {
	return &acceptPostgresRecovery{target: target, source: &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: target.Spec.Recovery.SourcePostgres, Namespace: target.Namespace}}}
}

func (a *acceptPostgresRecovery) GetObject() client.Object { return a.source }
func (a *acceptPostgresRecovery) GetOwner() api.NaisObject { return a.target }
func (*acceptPostgresRecovery) SkipOwnership() bool        { return true }
func (a *acceptPostgresRecovery) Do(ctx context.Context, c client.Client, _ *runtime.Scheme, _ ownership.OwnerManager) error {
	target := &v1.Postgres{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(a.target), target); err != nil {
		return err
	}
	if target.UID == "" || target.UID != a.target.UID || target.Spec.Recovery == nil || !target.DeletionTimestamp.IsZero() || !slices.Contains(target.Finalizers, "postgres.nais.io") {
		return fmt.Errorf("recovery target changed or has no durable cleanup finalizer")
	}
	source := &v1.Postgres{}
	if err := c.Get(ctx, client.ObjectKeyFromObject(a.source), source); err != nil {
		return err
	}
	if string(source.UID) != target.Spec.Recovery.SourceUID {
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
	if !source.DeletionTimestamp.IsZero() || !slices.Contains(source.Finalizers, "postgres.nais.io") {
		return fmt.Errorf("recovery source is deleting or has no cleanup finalizer")
	}
	admissions[string(target.UID)] = target.Name
	data, err := json.Marshal(admissions)
	if err != nil {
		return err
	}
	metav1.SetMetaDataAnnotation(&source.ObjectMeta, recoveryAdmissionsAnnotation, string(data))
	// Do not retry a conflict with this admission intent: re-read deletion state
	// and the registry in a new reconcile before trying again.
	return c.Update(ctx, source)
}

func pendingRecoveryAdmissions(ctx context.Context, reader client.Reader, source *v1.Postgres) (map[string]string, error) {
	admissions, err := recoveryAdmissions(source)
	if err != nil {
		return nil, err
	}
	pending := map[string]string{}
	for uid, name := range admissions {
		target := &v1.Postgres{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: source.Namespace, Name: name}, target); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		if string(target.UID) != uid {
			continue
		}
		if target.Spec.Recovery == nil || target.Spec.Recovery.SourceUID != string(source.UID) || target.Spec.Recovery.SourcePostgres != source.Name {
			return nil, fmt.Errorf("accepted recovery %q no longer matches its source", name)
		}
		cluster := &cnpgv1.Cluster{}
		err := reader.Get(ctx, client.ObjectKey{Namespace: source.Namespace, Name: rccnpg.ClusterNameFor(v1.PostgresPhysicalName(name))}, cluster)
		if client.IgnoreNotFound(err) != nil {
			return nil, err
		}
		if !recoveryFinished(target, cluster) || cluster.Spec.Bootstrap == nil || cluster.Spec.Bootstrap.Recovery != nil || len(cluster.Spec.ExternalClusters) != 0 {
			pending[uid] = name
		}
	}
	return pending, nil
}

func pruneRecoveryAdmissions(ctx context.Context, reader client.Reader, source *v1.Postgres) error {
	if source.Annotations[recoveryAdmissionsAnnotation] == "" {
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
	if len(pending) == 0 {
		delete(source.Annotations, recoveryAdmissionsAnnotation)
		return nil
	}
	data, err := json.Marshal(pending)
	if err != nil {
		return err
	}
	metav1.SetMetaDataAnnotation(&source.ObjectMeta, recoveryAdmissionsAnnotation, string(data))
	return nil
}
