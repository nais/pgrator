package v1

import (
	"github.com/nais/pgrator/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ExampleValkeyForDocumentation() api.NaisObject {
	return &Valkey{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Valkey",
			APIVersion: "nais.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myvalkey",
			Namespace: "myteam",
			Labels: map[string]string{
				"team": "myteam",
			},
		},
		Spec: ValkeySpec{
			Tier:                 ValkeyTierHighAvailability,
			Memory:               ValkeyMemory1GB,
			MaxMemoryPolicy:      ValkeyMaxMemoryPolicyNoEviction,
			NotifyKeyspaceEvents: "KEA",
			Persistence: &ValkeyPersistence{
				Disabled: true,
			},
			Databases: new(16),
		},
	}
}
