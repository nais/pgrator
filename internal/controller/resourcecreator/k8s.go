package resourcecreator

import (
	"fmt"

	data_nais_io_v1 "github.com/nais/pgrator/pkg/api/datav1"
	core_v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateKubernetesServiceAccount(obj *data_nais_io_v1.Postgres, pgNamespace, teamGoogleProjectID string) *core_v1.ServiceAccount {
	objectMeta := CreateObjectMeta(obj)
	objectMeta.Name = KSAName
	objectMeta.Namespace = pgNamespace

	gsaEmail := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", GSAName, teamGoogleProjectID)
	meta_v1.SetMetaDataAnnotation(&objectMeta, "iam.gke.io/gcp-service-account", gsaEmail)

	return &core_v1.ServiceAccount{
		TypeMeta: meta_v1.TypeMeta{
			Kind:       "ServiceAccount",
			APIVersion: "v1",
		},
		ObjectMeta: objectMeta,
	}
}
