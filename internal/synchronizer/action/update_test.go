package action

import (
	"context"
	"strings"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestUpdateSameUIDDoesNotRecreateOrReplaceResource(t *testing.T) {
	scheme := newExclusiveCreateOrUpdateScheme(t)
	owner := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: "branch", Namespace: "myteam"}}
	for _, tc := range []struct {
		name      string
		observed  types.UID
		wantError string
	}{
		{name: "same resource", observed: "original-uid"},
		{name: "missing resource", wantError: "not found"},
		{name: "replacement resource", observed: "replacement-uid", wantError: "was replaced"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := []client.Object{}
			if tc.observed != "" {
				objects = append(objects, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "branch", Namespace: "myteam", UID: tc.observed}, Data: map[string]string{"value": "old"}})
			}
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			desired := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "branch", Namespace: "myteam"}, Data: map[string]string{"value": "new"}}
			err := UpdateSameUID(desired, owner, "original-uid", noConditions, &mockRecorder{}).Do(context.Background(), k8sClient, scheme, &mockOwnerManager{})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.wantError) {
					t.Fatalf("UpdateSameUID error = %v, want %q", err, tc.wantError)
				}
			} else if err != nil {
				t.Fatalf("UpdateSameUID: %v", err)
			}
			stored := &corev1.ConfigMap{}
			getErr := k8sClient.Get(context.Background(), client.ObjectKey{Namespace: "myteam", Name: "branch"}, stored)
			if tc.observed == "" {
				if getErr == nil {
					t.Fatal("missing resource was recreated")
				}
				return
			}
			if getErr != nil {
				t.Fatal(getErr)
			}
			want := "old"
			if tc.wantError == "" {
				want = "new"
			}
			if stored.Data["value"] != want {
				t.Errorf("data = %q, want %q", stored.Data["value"], want)
			}
		})
	}
}
