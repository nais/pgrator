package v1

import (
	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ExamplePostgresBackupForDocumentation() api.NaisObject {
	return &PostgresBackup{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PostgresBackup",
			APIVersion: "nais.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-pre-migration",
			Namespace: "myteam",
		},
		Spec: PostgresBackupSpec{
			Postgres:      "orders",
			RetentionDays: new(DefaultPostgresBackupRetentionDays),
		},
	}
}
