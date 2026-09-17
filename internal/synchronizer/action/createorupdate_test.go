package action

import (
	"testing"

	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDurableCreateOrUpdateSkipsOwnership(t *testing.T) {
	owner := &v1.PostgresAccess{}
	object := &corev1.ConfigMap{}
	action := DurableCreateOrUpdate(object, owner, func(_ client.Object, _ *runtime.Scheme) []metav1.Condition { return nil }, nil)
	durable, ok := action.(interface{ SkipOwnership() bool })
	if !ok || !durable.SkipOwnership() {
		t.Error("DurableCreateOrUpdate() must skip ownership")
	}
}
