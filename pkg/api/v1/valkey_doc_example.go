package v1

import (
	"github.com/nais/pgrator/internal/synchronizer/object"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ExampleValkeyForDocumentation() object.NaisObject {
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
		},
	}
}
