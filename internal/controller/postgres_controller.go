package controller

import (
	"context"
	"fmt"

	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	"github.com/nais/liberator/pkg/namegen"
	"github.com/nais/pgrator/internal/controller/resourcecreator"
	"github.com/nais/pgrator/internal/synchronizer/action"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Max length is 63, but we need to save space for suffixes added by Zalando operator or StatefulSets
	maxClusterNameLength = 50
)

// PostgresReconciler reconciles a Postgres object
type PostgresReconciler struct {
}

type PreparedData struct {
}

func (r *PostgresReconciler) Name() string {
	return "postgres.data.nais.io"
}

func (r *PostgresReconciler) New() *data_nais_io_v1.Postgres {
	return &data_nais_io_v1.Postgres{}
}

func (r *PostgresReconciler) Delete(ctx context.Context, obj *data_nais_io_v1.Postgres) ([]action.Action, ctrl.Result, error) {
	// TODO implement me
	panic("implement me")
}

func (r *PostgresReconciler) Prepare(_ctx context.Context, _reader client.Reader, _obj *data_nais_io_v1.Postgres) (PreparedData, ctrl.Result, error) {
	return PreparedData{}, ctrl.Result{}, nil
}

func (r *PostgresReconciler) Update(ctx context.Context, obj *data_nais_io_v1.Postgres, preparedData PreparedData) ([]action.Action, ctrl.Result, error) {
	var err error
	pgClusterName := obj.GetName()
	if obj.Spec.Cluster.Name != "" {
		pgClusterName = obj.Spec.Cluster.Name
	}
	if len(pgClusterName) > maxClusterNameLength {
		pgClusterName, err = namegen.ShortName(pgClusterName, maxClusterNameLength)
		if err != nil {
			return nil, ctrl.Result{}, fmt.Errorf("failed to shorten PostgreSQL cluster name: %w", err)
		}
	}
	pgNamespace := fmt.Sprintf("pg-%s", obj.GetNamespace())

	var actions []action.Action

	actions = append(actions, resourcecreator.CreateClusterSpec(obj, pgClusterName, pgNamespace))
	//createNetworkPolicies(source, ast, pgClusterName, pgNamespace)
	//err = createIAMPolicyMember(source, ast, cfg.GetGoogleProjectID(), pgNamespace)
	//if err != nil {
	//	return fmt.Errorf("failed to create IAMPolicyMember: %w", err)
	//}

	return actions, ctrl.Result{}, nil
}
