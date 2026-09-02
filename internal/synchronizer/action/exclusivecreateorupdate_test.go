package action

import (
	"context"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newExclusiveCreateOrUpdateScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(corev1): %v", err)
	}
	if err := cnpgv1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(cnpgv1): %v", err)
	}
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(v1): %v", err)
	}
	return scheme
}

func newExclusiveOwner(name string) *v1.PostgresBinding {
	return &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: "myteam", UID: types.UID(name),
	}}
}

func newExclusiveSecret(owner *v1.PostgresBinding, value string) *corev1.Secret {
	controller := true
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "binding-config", Namespace: "myteam",
			OwnerReferences: []metav1.OwnerReference{{
				Name: owner.Name, UID: owner.UID, Controller: &controller,
			}},
		},
		StringData: map[string]string{"value": value},
	}
}

func noConditions(client.Object, *runtime.Scheme) []metav1.Condition { return nil }

func performExclusiveCreateOrUpdate(scheme *runtime.Scheme, k8sClient client.Client, desired *corev1.Secret, owner *v1.PostgresBinding) error {
	return ExclusiveCreateOrUpdate(desired, owner, noConditions, &mockRecorder{}).Do(context.Background(), k8sClient, scheme, &mockOwnerManager{})
}

func newExclusiveRole(owner *v1.PostgresBinding, comment string) *cnpgv1.DatabaseRole {
	controller := true
	return &cnpgv1.DatabaseRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "shared-role-resource", Namespace: "myteam",
			OwnerReferences: []metav1.OwnerReference{{
				Name: owner.Name, UID: owner.UID, Controller: &controller,
			}},
		},
		Spec: cnpgv1.DatabaseRoleSpec{RoleConfiguration: cnpgv1.RoleConfiguration{Comment: comment}},
	}
}

func performExclusiveCreateOrUpdateRole(scheme *runtime.Scheme, k8sClient client.Client, desired *cnpgv1.DatabaseRole, owner *v1.PostgresBinding) error {
	return ExclusiveCreateOrUpdate(desired, owner, noConditions, &mockRecorder{}).Do(context.Background(), k8sClient, scheme, &mockOwnerManager{})
}

func TestExclusiveCreateOrUpdateCreatesAnUnclaimedChild(t *testing.T) {
	scheme := newExclusiveCreateOrUpdateScheme(t)
	owner := newExclusiveOwner("first")
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	if err := performExclusiveCreateOrUpdate(scheme, k8sClient, newExclusiveSecret(owner, "initial"), owner); err != nil {
		t.Fatalf("perform: %v", err)
	}
	created := &corev1.Secret{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "myteam", Name: "binding-config"}, created); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := created.StringData["value"]; got != "initial" {
		t.Errorf("StringData[value] = %q, want %q", got, "initial")
	}
}

func TestExclusiveCreateOrUpdateUpdatesAChildAlreadyOwnedByTheBinding(t *testing.T) {
	scheme := newExclusiveCreateOrUpdateScheme(t)
	owner := newExclusiveOwner("first")
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newExclusiveSecret(owner, "initial")).Build()

	if err := performExclusiveCreateOrUpdate(scheme, k8sClient, newExclusiveSecret(owner, "updated"), owner); err != nil {
		t.Fatalf("perform: %v", err)
	}
	updated := &corev1.Secret{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "myteam", Name: "binding-config"}, updated); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := updated.StringData["value"]; got != "updated" {
		t.Errorf("StringData[value] = %q, want %q", got, "updated")
	}
}

func TestExclusiveCreateOrUpdateRejectsAChildControlledByAnotherBindingWithoutChangingIt(t *testing.T) {
	scheme := newExclusiveCreateOrUpdateScheme(t)
	first := newExclusiveOwner("first")
	second := newExclusiveOwner("second")
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newExclusiveSecret(first, "initial")).Build()

	err := performExclusiveCreateOrUpdate(scheme, k8sClient, newExclusiveSecret(second, "hijacked"), second)
	if err == nil || !strings.Contains(err.Error(), `already claimed by "first"`) {
		t.Fatalf("err = %v, want error containing 'already claimed by \"first\"'", err)
	}
	existing := &corev1.Secret{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "myteam", Name: "binding-config"}, existing); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := existing.StringData["value"]; got != "initial" {
		t.Errorf("StringData[value] = %q, want %q", got, "initial")
	}
}

func TestExclusiveCreateOrUpdateUpdatesADatabaseRoleAlreadyOwnedByTheBinding(t *testing.T) {
	scheme := newExclusiveCreateOrUpdateScheme(t)
	owner := newExclusiveOwner("first")
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newExclusiveRole(owner, "initial")).Build()

	if err := performExclusiveCreateOrUpdateRole(scheme, k8sClient, newExclusiveRole(owner, "updated"), owner); err != nil {
		t.Fatalf("perform: %v", err)
	}
	updated := &cnpgv1.DatabaseRole{}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "myteam", Name: "shared-role-resource"}, updated); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := updated.Spec.Comment; got != "updated" {
		t.Errorf("Spec.Comment = %q, want %q", got, "updated")
	}
}

func TestExclusiveCreateOrUpdateRejectsTheSameDatabaseRoleNameFromAnotherBinding(t *testing.T) {
	scheme := newExclusiveCreateOrUpdateScheme(t)
	first := newExclusiveOwner("first")
	second := newExclusiveOwner("second")
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newExclusiveRole(first, "initial")).Build()

	err := performExclusiveCreateOrUpdateRole(scheme, k8sClient, newExclusiveRole(second, "hijacked"), second)
	if err == nil || !strings.Contains(err.Error(), `already claimed by "first"`) {
		t.Fatalf("err = %v, want error containing 'already claimed by \"first\"'", err)
	}
}

func TestExclusiveCreateOrUpdateRejectsAnOwnerlessExistingChild(t *testing.T) {
	scheme := newExclusiveCreateOrUpdateScheme(t)
	owner := newExclusiveOwner("first")
	existing := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "binding-config", Namespace: "myteam"}}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

	err := performExclusiveCreateOrUpdate(scheme, k8sClient, newExclusiveSecret(owner, "hijacked"), owner)
	if err == nil || !strings.Contains(err.Error(), "already exists without a controller owner") {
		t.Fatalf("err = %v, want error containing 'already exists without a controller owner'", err)
	}
}
