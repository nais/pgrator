package controller

import (
	"context"

	data_nais_io_v1 "github.com/nais/liberator/pkg/apis/data.nais.io/v1"
	"github.com/nais/pgrator/internal/synchronizer/action"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	// TODO implement me
	panic("implement me")
}
