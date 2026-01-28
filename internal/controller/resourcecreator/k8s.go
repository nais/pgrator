package resourcecreator

import (
	"fmt"

	core_v1 "k8s.io/api/core/v1"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateKubernetesServiceAccount(name, pgNamespace, teamGoogleProjectID, GSAName string) *core_v1.ServiceAccount {
	objectMeta := meta_v1.ObjectMeta{
		Name:      name,
		Namespace: pgNamespace,
	}

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
