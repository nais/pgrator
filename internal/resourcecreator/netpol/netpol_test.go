package netpol

import (
	"strings"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
)

func TestNetworkPolicyName(t *testing.T) {
	tests := []struct {
		name     string
		postgres *v1.Postgres
		want     string
	}{
		{
			name: "owner name and short UID",
			postgres: &v1.Postgres{ObjectMeta: metav1.ObjectMeta{
				Name: "mydb",
				UID:  types.UID("feedab1e-beef-cafe-babe-700d1e100d1e"),
			}},
			want: "pg-mydb-feedab1ebeef",
		},
		{
			name: "object without UID",
			postgres: &v1.Postgres{ObjectMeta: metav1.ObjectMeta{
				Name: "mydb",
			}},
			want: "pg-mydb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := networkPolicyName(tt.postgres); got != tt.want {
				t.Errorf("networkPolicyName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNetworkPolicyNameRespectsKubernetesLengthLimit(t *testing.T) {
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{
		Name: strings.Repeat("a", validation.DNS1123SubdomainMaxLength),
		UID:  types.UID("feedab1e-beef-cafe-babe-700d1e100d1e"),
	}}

	got := networkPolicyName(postgres)
	if len(got) != validation.DNS1123SubdomainMaxLength {
		t.Errorf("len(networkPolicyName()) = %d, want %d", len(got), validation.DNS1123SubdomainMaxLength)
	}
	if !strings.HasPrefix(got, "pg-") {
		t.Errorf("networkPolicyName() = %q, want pg- prefix", got)
	}
	if !strings.HasSuffix(got, "-feedab1ebeef") {
		t.Errorf("networkPolicyName() = %q, want shortened UID suffix", got)
	}
}
