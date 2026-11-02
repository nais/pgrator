package controller

import (
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPostgresCRDValidatesDatabaseExtensions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		extension string
		wantErr   string
	}{
		{name: "plain extension", extension: "pg_trgm"},
		{name: "hyphenated extension", extension: "uuid-ossp"},
		{name: "platform managed pg_stat_statements", extension: "pg_stat_statements", wantErr: "managed by the platform"},
		{name: "platform managed pgaudit", extension: "pgaudit", wantErr: "managed by the platform"},
		{name: "SQL injection attempt", extension: `x"; DROP DATABASE app; --`, wantErr: "should match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			postgres := &v1.Postgres{
				ObjectMeta: metav1.ObjectMeta{GenerateName: "dbext-", Namespace: "default"},
				Spec: v1.PostgresSpec{
					MajorVersion: "18",
					Database:     &v1.PostgresDatabase{Extensions: []v1.PostgresDatabaseExtension{{Name: tc.extension}}},
				},
			}
			err := k8sClient.Create(ctx, postgres)
			if tc.wantErr == "" {
				requireNoError(t, err)
				requireNoError(t, k8sClient.Delete(ctx, postgres))
				return
			}
			requireErrorContains(t, err, tc.wantErr)
		})
	}
}
