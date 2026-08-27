package controller

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/config"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	rcnetpol "github.com/nais/pgrator/internal/resourcecreator/netpol"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	networking_v1 "k8s.io/api/networking/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// PostgresReconciler reconciles a nais.io/v1 Postgres object into a CloudNativePG
// Cluster, an app-owner DatabaseRole (cert auth), and a NetworkPolicy.
type PostgresReconciler struct {
	Config   *config.Config
	Recorder events.Recorder
	Scheme   *runtime.Scheme
}

var _ reconciler.Reconciler[*v1.Postgres, PostgresPreparedData] = &PostgresReconciler{}

// PostgresPreparedData contains data prepared during the Prepare phase.
type PostgresPreparedData struct{}

func (r *PostgresReconciler) Name() string {
	return "postgres.nais.io"
}

func (r *PostgresReconciler) New() *v1.Postgres {
	return &v1.Postgres{}
}

func (r *PostgresReconciler) Prepare(_ context.Context, _ client.Reader, _ *v1.Postgres) (PostgresPreparedData, ctrl.Result, error) {
	return PostgresPreparedData{}, ctrl.Result{}, nil
}

func (r *PostgresReconciler) OwnedTypes() []reconciler.OwnedType {
	return []reconciler.OwnedType{
		{
			Type: &cnpgv1.Cluster{},
			AdditionalPredicate: predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldObj, ok1 := e.ObjectOld.(*cnpgv1.Cluster)
					newObj, ok2 := e.ObjectNew.(*cnpgv1.Cluster)
					if !ok1 || !ok2 {
						return false
					}
					return oldObj.Status.Phase != newObj.Status.Phase
				},
			},
		},
		{Type: &cnpgv1.DatabaseRole{}},
		{Type: &networking_v1.NetworkPolicy{}},
	}
}

func (r *PostgresReconciler) AdditionalTypes() []client.Object {
	return nil
}

func (r *PostgresReconciler) MetricsLabels(obj *v1.Postgres) map[string]string {
	ha := "false"
	if obj.Spec.HighAvailability {
		ha = "true"
	}
	return map[string]string{
		"major_version":     obj.Spec.MajorVersion,
		"high_availability": ha,
	}
}

func (r *PostgresReconciler) Update(obj *v1.Postgres, _ PostgresPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	var actions []action.Action

	cluster, err := rccnpg.CreateCluster(r.Scheme, obj, r.Config)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating CNPG Cluster spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(cluster, obj, clusterConditionGetter, r.Recorder))

	appRole, err := rccnpg.CreateAppRole(r.Scheme, obj)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating app DatabaseRole spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(appRole, obj, existsConditionGetter, r.Recorder))

	netpol, err := rcnetpol.Create(r.Scheme, obj, rccnpg.ClusterName(obj), r.Config.APIServerIP)
	if err != nil {
		return nil, ctrl.Result{}, fmt.Errorf("creating NetworkPolicy spec: %w", err)
	}
	actions = append(actions, action.CreateOrUpdate(netpol, obj, existsConditionGetter, r.Recorder))

	return actions, ctrl.Result{}, nil
}

// Delete relies on ownerReference garbage collection to remove the owned Cluster,
// DatabaseRole and NetworkPolicy when the Postgres resource is deleted.
func (r *PostgresReconciler) Delete(_ *v1.Postgres, _ PostgresPreparedData, _ reconciler.RelatedObjects) ([]action.Action, ctrl.Result, error) {
	return nil, ctrl.Result{}, nil
}

func clusterConditionGetter(obj client.Object, scheme *runtime.Scheme) []meta_v1.Condition {
	cluster, ok := obj.(*cnpgv1.Cluster)
	if !ok {
		return nil
	}
	phase := cluster.Status.Phase
	return []meta_v1.Condition{
		{
			Type:               fmt.Sprintf("%s/%s", typePrefix(obj, scheme), "ObservedState"),
			Status:             makeCondition(phase != ""),
			ObservedGeneration: obj.GetGeneration(),
			Reason:             "Reconciled",
			Message:            fmt.Sprintf("Cluster is in phase: %s", phase),
		},
	}
}
