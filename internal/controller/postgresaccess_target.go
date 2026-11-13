package controller

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// resolveAccessCluster accepts both immutable reference shapes without changing
// physical names. A missing legacy main may resolve only to a directly managed
// Postgres; a missing non-main branch must never fall back to another database.
func resolveAccessCluster(ctx context.Context, reader client.Reader, access *v1.PostgresAccess) (*cnpgv1.Cluster, error) {
	if (access.Spec.Postgres == "") == (access.Spec.PostgresBranch == "") {
		return nil, fmt.Errorf("exactly one of postgres and postgresBranch must be set")
	}
	postgres, err := resolveAccessPostgres(ctx, reader, access)
	if err != nil {
		return nil, err
	}
	if !postgres.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("postgres %q is deleting", postgres.Name)
	}
	clusterName := rccnpg.ClusterNameFor(access.Spec.PhysicalName())
	if directClusterName(postgres) != "" {
		if access.Spec.PhysicalName() != v1.PostgresPhysicalName(postgres.Name) || directClusterName(postgres) != clusterName {
			return nil, fmt.Errorf("access reference does not match the directly managed Postgres %q", postgres.Name)
		}
	} else {
		return nil, fmt.Errorf("postgres %q has no observed physical cluster yet", postgres.Name)
	}
	cluster := &cnpgv1.Cluster{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: access.Namespace, Name: clusterName}, cluster); err != nil {
		return nil, fmt.Errorf("getting CNPG Cluster %q: %w", clusterName, err)
	}
	if !cluster.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("CNPG Cluster %q is deleting", clusterName)
	}
	if directClusterName(postgres) != "" {
		owner := metav1.GetControllerOf(cluster)
		if owner == nil || postgres.UID == "" || owner.UID != postgres.UID || owner.Name != postgres.Name || owner.Kind != postgresKind || owner.APIVersion != v1.GroupVersion.String() {
			return nil, fmt.Errorf("CNPG Cluster %q is not controlled by Postgres %q", cluster.Name, postgres.Name)
		}
		if postgres.Status.Ownership != nil && postgres.Status.Ownership.ClusterUID != "" && postgres.Status.Ownership.ClusterUID != string(cluster.UID) {
			return nil, fmt.Errorf("CNPG Cluster %q was replaced after ownership was checkpointed", cluster.Name)
		}
	}
	return cluster, nil
}

func resolveAccessPostgres(ctx context.Context, reader client.Reader, access *v1.PostgresAccess) (*v1.Postgres, error) {
	postgres := &v1.Postgres{}
	if access.Spec.Postgres != "" {
		if err := reader.Get(ctx, client.ObjectKey{Namespace: access.Namespace, Name: access.Spec.Postgres}, postgres); err != nil {
			return nil, fmt.Errorf("getting Postgres %q: %w", access.Spec.Postgres, err)
		}
		return postgres, nil
	}
	// Legacy access specs are immutable, but their main reference can be
	// resolved from the logical parents without reading the retired branch API.
	postgreses := &v1.PostgresList{}
	if err := reader.List(ctx, postgreses, client.InNamespace(access.Namespace)); err != nil {
		return nil, fmt.Errorf("resolving legacy main access: %w", err)
	}
	var found *v1.Postgres
	for i := range postgreses.Items {
		candidate := &postgreses.Items[i]
		if v1.PostgresPhysicalName(candidate.Name) != access.Spec.PostgresBranch {
			continue
		}
		if directClusterName(candidate) == "" {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("ambiguous legacy main reference %q", access.Spec.PostgresBranch)
		}
		found = candidate
	}
	if found != nil {
		return found, nil
	}
	return nil, fmt.Errorf("legacy access has no matching directly managed Postgres")
}
