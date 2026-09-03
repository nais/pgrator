package controller

import (
	"context"
	"testing"

	"github.com/nais/pgrator/internal/synchronizer"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestPostgresBindingReconciliation(t *testing.T) {
	t.Run("removes the finalizer when the referenced Postgres is already missing", func(t *testing.T) {
		binding := &v1.PostgresBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "missing-postgres-deletion",
				Namespace:  "default",
				Finalizers: []string{"postgresbinding.nais.io"},
			},
			Spec: v1.PostgresBindingSpec{
				Postgres: "already-gone",
				Consumer: v1.PostgresBindingConsumer{
					Workload: &v1.PostgresBindingWorkload{
						Name: "myapp",
						Type: v1.PostgresBindingWorkloadTypeApplication,
					},
				},
				SecretName: "already-gone-myapp-read-client-cert",
				Role:       v1.PostgresBindingRoleRead,
			},
		}
		requireNoError(t, k8sClient.Create(context.Background(), binding))
		requireNoError(t, k8sClient.Delete(context.Background(), binding))

		bindingReconciler := &PostgresBindingReconciler{Recorder: recorder, Scheme: scheme.Scheme}
		syncReconciler := synchronizer.NewSynchronizer(k8sClient, scheme.Scheme, bindingReconciler, recorder)
		key := client.ObjectKeyFromObject(binding)
		_, err := syncReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: key})
		requireNoError(t, err)

		err = k8sClient.Get(context.Background(), key, &v1.PostgresBinding{})
		requireTrue(t, apierrors.IsNotFound(err), "binding should be deleted after finalizer removal")
	})
}
