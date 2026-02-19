package v1

import (
	"github.com/nais/pgrator/pkg/api"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ExampleOpenSearchForDocumentation() api.NaisObject {
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
			ShardIndexingPressure: &OpenSearchShardIndexingPressure{
				Enabled:  true,
				Enforced: false,
			},
			Indices: &OpenSearchIndices{
				QueryBoolMaxClauseCount: new(1024),
			},
			Http: &OpenSearchHttp{
				MaxContentLength: new(resource.MustParse("100Mi")),
			},
		},
	}
}
