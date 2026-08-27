package v1

import (
	"github.com/nais/pgrator/pkg/api"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ExamplePostgresForDocumentation() api.NaisObject {
	return &Postgres{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Postgres",
			APIVersion: "nais.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mypostgres",
			Namespace: "myteam",
			Labels: map[string]string{
				"team": "myteam",
			},
		},
		Spec: PostgresSpec{
			Resources: PostgresResources{
				DiskSize: resource.MustParse("10Gi"),
				Cpu:      resource.MustParse("100m"),
				Memory:   resource.MustParse("512Mi"),
			},
			MajorVersion:     "18",
			HighAvailability: true,
			Extensions: []PostgresExtension{
				{
					Name: "postgis",
				},
			},
		},
	}
}
