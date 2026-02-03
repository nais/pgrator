package v1

import (
	"github.com/nais/pgrator/internal/synchronizer/object"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ExampleOpenSearchForDocumentation() object.NaisObject {
	return &OpenSearch{
		TypeMeta: metav1.TypeMeta{
			Kind:       "OpenSearch",
			APIVersion: "nais.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myopensearch",
			Namespace: "myteam",
			Labels: map[string]string{
				"team": "myteam",
			},
		},
		Spec: OpenSearchSpec{
			Tier:      OpenSearchTierSingleNode,
			Memory:    OpenSearchMemory4GB,
			Version:   "3.3",
			StorageGB: 80,
		},
	}
}
