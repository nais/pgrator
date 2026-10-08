package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestBindingBranchAdmission(t *testing.T) {
	for i, tt := range []struct {
		branch string
		valid  bool
	}{
		{valid: true},
		{branch: "main", valid: true},
		{branch: "pr-123", valid: true},
		{branch: strings.Repeat("a", 63), valid: true},
		{branch: strings.Repeat("a", 64)},
		{branch: "INVALID"},
		{branch: "other/branch"},
		{branch: "-branch"},
	} {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			binding := &v1.PostgresBinding{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("branch-validation-%d", i), Namespace: "default"}, Spec: v1.PostgresBindingSpec{
				Postgres: "orders", Branch: tt.branch,
				Consumer:    v1.PostgresBindingConsumer{Workload: &v1.PostgresBindingWorkload{Name: "consumer", Type: v1.PostgresBindingWorkloadTypeApplication}},
				Credentials: []v1.PostgresBindingCredential{v1.PostgresBindingCredentialRead},
			}}
			err := k8sClient.Create(t.Context(), binding)
			if (err == nil) != tt.valid {
				t.Fatalf("branch=%q: Create error=%v, valid=%t", tt.branch, err, tt.valid)
			}
			if err != nil {
				return
			}
			t.Cleanup(func() { requireNoError(t, client.IgnoreNotFound(k8sClient.Delete(context.Background(), binding))) })
			// A workload may pin a branch, switch it, then return to following active.
			for _, branch := range []string{"pr-456", ""} {
				binding.Spec.Branch = branch
				requireNoError(t, k8sClient.Update(t.Context(), binding))
			}
		})
	}
}
