package action

import (
	"context"
	"strings"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newExclusiveCreateScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(corev1): %v", err)
	}
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme(v1): %v", err)
	}
	return scheme
}

func newExclusiveCreateOwner(name string) *v1.PostgresBinding {
	return &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "myteam", UID: types.UID(name)}}
}

func newExclusiveCreateLock(owner *v1.PostgresBinding) *corev1.ConfigMap {
	controller := true
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name:      "postgresbinding-admin-lock",
		Namespace: "myteam",
		OwnerReferences: []metav1.OwnerReference{{
			Name: owner.Name, UID: owner.UID, Controller: &controller,
		}},
	}}
}

func TestExclusiveCreateAllowsOwnerThatCreatedTheLockToReconcileIt(t *testing.T) {
	scheme := newExclusiveCreateScheme(t)
	ownerManager := &mockOwnerManager{}
	owner := newExclusiveCreateOwner("first")
	lock := newExclusiveCreateLock(owner)
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(lock).Build()

	if err := ExclusiveCreate(newExclusiveCreateLock(owner), owner).Do(context.Background(), k8sClient, scheme, ownerManager); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

func TestExclusiveCreateRejectsAnotherOwnerOfTheSameLock(t *testing.T) {
	scheme := newExclusiveCreateScheme(t)
	ownerManager := &mockOwnerManager{}
	firstOwner := newExclusiveCreateOwner("first")
	secondOwner := newExclusiveCreateOwner("second")
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newExclusiveCreateLock(firstOwner)).Build()

	err := ExclusiveCreate(newExclusiveCreateLock(secondOwner), secondOwner).Do(context.Background(), k8sClient, scheme, ownerManager)
	if err == nil || !strings.Contains(err.Error(), `already claimed by "first"`) {
		t.Fatalf("err = %v, want error containing 'already claimed by \"first\"'", err)
	}
}

func TestExclusiveCreateCreatesAnUnclaimedLock(t *testing.T) {
	scheme := newExclusiveCreateScheme(t)
	ownerManager := &mockOwnerManager{}
	owner := newExclusiveCreateOwner("first")
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	if err := ExclusiveCreate(newExclusiveCreateLock(owner), owner).Do(context.Background(), k8sClient, scheme, ownerManager); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if err := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "myteam", Name: "postgresbinding-admin-lock"}, &corev1.ConfigMap{}); err != nil {
		t.Fatalf("Get: %v", err)
	}
}

func TestExclusiveCreateAllowsExactlyOneOfTwoConcurrentOwnersToClaimALock(t *testing.T) {
	scheme := newExclusiveCreateScheme(t)
	ownerManager := &mockOwnerManager{}
	first := newExclusiveCreateOwner("first")
	second := newExclusiveCreateOwner("second")
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	errs := make(chan error, 2)
	start := make(chan struct{})
	for _, owner := range []*v1.PostgresBinding{first, second} {
		go func(owner *v1.PostgresBinding) {
			<-start
			errs <- ExclusiveCreate(newExclusiveCreateLock(owner), owner).Do(context.Background(), k8sClient, scheme, ownerManager)
		}(owner)
	}
	close(start)

	results := []error{<-errs, <-errs}

	var successes, claimErrors int
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case strings.Contains(err.Error(), "already claimed by"):
			claimErrors++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || claimErrors != 1 {
		t.Fatalf("expected exactly one success and one claim error, got %d successes, %d claim errors (results=%v)", successes, claimErrors, results)
	}
}
