package controller

import (
	"context"
	"strings"
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPostgresAccessReferenceMatchesPostgresObjectNames(t *testing.T) {
	for _, tc := range []struct {
		name     string
		postgres string
		valid    bool
	}{
		{name: "dns label", postgres: "access-name-validation", valid: true},
		{name: "dns subdomain", postgres: "orders.api", valid: true},
		{name: "longer than a label", postgres: strings.Repeat("a", 64), valid: true},
		{name: "maximum object name", postgres: strings.Repeat("a", 253), valid: true},
		{name: "maximum dotted object name", postgres: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61), valid: true},
		{name: "too long", postgres: strings.Repeat("a", 254)},
		{name: "uppercase", postgres: "Orders.api"},
		{name: "empty label", postgres: "orders..api"},
		{name: "leading dot", postgres: ".orders"},
		{name: "trailing dot", postgres: "orders."},
		{name: "leading hyphen", postgres: "-orders.api"},
		{name: "hyphen after dot", postgres: "orders.-api"},
		{name: "trailing label hyphen", postgres: "orders-.api"},
		{name: "underscore", postgres: "orders_api"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: tc.postgres, Namespace: postgresAccessValidationNamespace}, Spec: v1.PostgresSpec{MajorVersion: "18"}}
			postgresErr := k8sClient.Create(t.Context(), postgres)
			if postgresErr == nil {
				t.Cleanup(func() { requireNoError(t, client.IgnoreNotFound(k8sClient.Delete(context.Background(), postgres))) })
			}
			if (postgresErr == nil) != tc.valid {
				t.Fatalf("Postgres name acceptance = %t, want %t: %v", postgresErr == nil, tc.valid, postgresErr)
			}
			access, _, _, _ := accessFixture()
			access.Name = "access-name-" + strings.ReplaceAll(tc.name, " ", "-")
			access.Namespace, access.UID = postgresAccessValidationNamespace, ""
			access.Spec.Postgres, access.Spec.PostgresBranch = tc.postgres, ""
			accessErr := k8sClient.Create(t.Context(), access)
			if accessErr == nil {
				t.Cleanup(func() { requireNoError(t, client.IgnoreNotFound(k8sClient.Delete(context.Background(), access))) })
			}
			if (accessErr == nil) != tc.valid {
				t.Fatalf("direct reference acceptance differs from Postgres.metadata.name: %v", accessErr)
			}
		})
	}
}
