package v1

import (
	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ExamplePostgresBindingForDocumentation() api.NaisObject {
	return &PostgresBinding{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PostgresBinding",
			APIVersion: "nais.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-mypostgres",
			Namespace: "myteam",
		},
		Spec: PostgresBindingSpec{
			Postgres: "mypostgres",
			Workload: "myapp",
			Role:     PostgresBindingRoleReadWrite,
		},
	}
}
