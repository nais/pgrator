package controller

import (
	"context"

	"github.com/nais/pgrator/internal/synchronizer"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("PostgresBinding reconciliation", func() {
	It("removes the finalizer when the referenced Postgres is already missing", func() {
		binding := &v1.PostgresBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "missing-postgres-deletion",
				Namespace:  "default",
				Finalizers: []string{"postgresbinding.nais.io"},
			},
			Spec: v1.PostgresBindingSpec{
				Postgres: "already-gone",
				Workload: v1.PostgresBindingWorkload{
					Name: "myapp",
					Type: v1.PostgresBindingWorkloadTypeApplication,
				},
				SecretName: "already-gone-myapp-read-client-cert",
				Role:       v1.PostgresBindingRoleRead,
			},
		}
		Expect(k8sClient.Create(context.Background(), binding)).To(Succeed())
		Expect(k8sClient.Delete(context.Background(), binding)).To(Succeed())

		bindingReconciler := &PostgresBindingReconciler{Recorder: recorder, Scheme: scheme.Scheme}
		syncReconciler := synchronizer.NewSynchronizer(k8sClient, scheme.Scheme, bindingReconciler, recorder)
		key := client.ObjectKeyFromObject(binding)
		_, err := syncReconciler.Reconcile(context.Background(), reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		err = k8sClient.Get(context.Background(), key, &v1.PostgresBinding{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})
})
