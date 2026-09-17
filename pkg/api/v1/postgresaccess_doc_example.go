package v1

import (
	"time"

	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ExamplePostgresAccessForDocumentation() api.NaisObject {
	return &PostgresAccess{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PostgresAccess",
			APIVersion: "nais.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "frode-orders-access",
			Namespace: "myteam",
		},
		Spec: PostgresAccessSpec{
			PostgresInstance:         "orders",
			Username:                 "frode.sundby@nav.no",
			AccessLevel:              PostgresAccessLevelRead,
			ExpiresAt:                metav1.NewTime(time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)),
			ClientWireGuardPublicKey: "xTIBA5rboUvnH4htodjb6e6QjEK+tWY1W5pKyCSCIRE=",
		},
	}
}
