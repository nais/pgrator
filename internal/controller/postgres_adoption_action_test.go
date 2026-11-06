package controller

import (
	"reflect"
	"testing"

	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/ownership"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestPostgresOwnershipAdoptionAPIServerRoundTrip(t *testing.T) {
	pg := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "adoption-roundtrip", Namespace: postgresAccessValidationNamespace}, Spec: v1.PostgresSpec{MajorVersion: "18"}}
	requireNoError(t, k8sClient.Create(t.Context(), pg))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, pg) })
	branch := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresPhysicalName(pg.Name), Namespace: pg.Namespace}, Spec: v1.PostgresBranchSpec{Postgres: pg.Name, BranchName: v1.DefaultBranchName}}
	requireNoError(t, controllerutil.SetControllerReference(pg, branch, scheme.Scheme))
	requireNoError(t, k8sClient.Create(t.Context(), branch))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, branch) })
	oldOwners := ownership.NewOwnerManager("postgresbranch.nais.io/owner")
	newOwners := ownership.NewOwnerManager("postgres.nais.io/owner")
	resource := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "adoption-roundtrip-child", Namespace: pg.Namespace, Labels: map[string]string{"preserved": "label"}}, Data: map[string]string{"preserved": "database configuration"}}
	requireNoError(t, controllerutil.SetControllerReference(branch, resource, scheme.Scheme))
	oldOwners.AddOwnerAnnotation(resource, branch)
	requireNoError(t, k8sClient.Create(t.Context(), resource))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, resource) })
	uid := resource.UID
	observe := func(client.Object, *runtime.Scheme) []metav1.Condition { return nil }
	adopt := action.Adopt(resource, branch, pg, oldOwners, observe, recorder)
	for range 2 {
		requireNoError(t, adopt.Do(t.Context(), k8sClient, scheme.Scheme, newOwners))
	}
	stored := &corev1.ConfigMap{}
	requireNoError(t, k8sClient.Get(t.Context(), client.ObjectKeyFromObject(resource), stored))
	if stored.UID != uid || !reflect.DeepEqual(stored.Data, resource.Data) || !reflect.DeepEqual(stored.Labels, resource.Labels) {
		t.Fatal("adoption replaced the resource or changed non-ownership content")
	}
	if !metav1.IsControlledBy(stored, pg) || oldOwners.HasOwnerAnnotation(stored, branch) || !newOwners.HasOwnerAnnotation(stored, pg) {
		t.Fatal("API server did not persist the complete ownership transfer")
	}
}
