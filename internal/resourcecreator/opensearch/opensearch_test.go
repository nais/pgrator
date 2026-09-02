package opensearch

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/nais/pgrator/pkg/api/v1"
)

func TestServiceName(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		instance  string
		want      string
	}{
		{name: "basic team and instance", namespace: "my-team", instance: "my-opensearch", want: "opensearch-my-team-my-opensearch"},
		{name: "different team and instance names", namespace: "production", instance: "search", want: "opensearch-production-search"},
		{name: "names with hyphens", namespace: "my-awesome-team", instance: "my-app-search", want: "opensearch-my-awesome-team-my-app-search"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &v1.OpenSearch{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.instance,
					Namespace: tt.namespace,
				},
			}

			got := ServiceName(obj)
			if got != tt.want {
				t.Errorf("ServiceName() = %q, want %q", got, tt.want)
			}
		})
	}
}
