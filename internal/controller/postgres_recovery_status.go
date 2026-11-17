package controller

import (
	"errors"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/pkg/api"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const recoveryCompletedCondition = "RecoveryCompleted"
const recoveryFailedCondition = "RecoveryFailed"

func recoveryFailureError(owner api.NaisObject) error {
	condition := meta.FindStatusCondition(owner.GetStatus().GetConditions(), recoveryFailedCondition)
	if condition == nil || condition.Status != metav1.ConditionTrue {
		return nil
	}
	owner.GetStatus().SetCondition(metav1.Condition{
		Type: readyCondition, Status: metav1.ConditionFalse, Reason: recoveryFailedCondition,
		Message: condition.Message, ObservedGeneration: owner.GetGeneration(),
	})
	owner.GetStatus().SetReconcilePhase("Failed")
	return reconcile.TerminalError(errors.New(condition.Message))
}

// recoveryFinished is a one-way bootstrap milestone for this particular
// Cluster object, independent of whether it is currently Ready.
func recoveryFinished(owner api.NaisObject, cluster *cnpgv1.Cluster) bool {
	return cluster != nil && cluster.DeletionTimestamp == nil &&
		metav1.IsControlledBy(cluster, owner) && cluster.UID != "" &&
		completedRecoveryClusterUID(owner) == string(cluster.UID)
}

func completedRecoveryClusterUID(owner api.NaisObject) string {
	for _, condition := range owner.GetStatus().GetConditions() {
		if condition.Type == recoveryCompletedCondition && condition.Status == metav1.ConditionTrue {
			return condition.Message
		}
	}
	return ""
}

// A successful backup of this Cluster proves that it no longer depends on
// the source archive. Initialized alone is not sufficient: the recovered
// database must first have a usable archive of its own.
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
