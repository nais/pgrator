package controller

import (
	"fmt"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	barmanv1 "github.com/cloudnative-pg/plugin-barman-cloud/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/initscheme"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	iamcnrm "github.com/nais/pgrator/internal/thirdparty/google/iam/v1beta1"
	gkenetworking "github.com/nais/pgrator/internal/thirdparty/google/networking/v1alpha3"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestPostgresBranchDeletionWaitsForOwnedResources(t *testing.T) {
	for _, child := range []client.Object{
		&cnpgv1.Cluster{},
		&cnpgv1.DatabaseRole{},
		&cnpgv1.Pooler{},
		&networkingv1.NetworkPolicy{},
		&cnpgv1.ScheduledBackup{},
		&barmanv1.ObjectStore{},
		&gkenetworking.FQDNNetworkPolicy{},
		&iamcnrm.IAMServiceAccount{},
		&iamcnrm.IAMPolicyMember{},
		&storagecnrm.StorageBucket{},
	} {
		t.Run(fmt.Sprintf("%T", child), func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			branch := &v1.PostgresBranch{ObjectMeta: metav1.ObjectMeta{Name: "branch", Namespace: "team", UID: "branch-uid"}}
			child.SetName("child")
			child.SetNamespace("team")
			requireNoError(t, controllerutil.SetControllerReference(branch, child, scheme))
			related := relatedobjectsmap.NewRelatedObjectsMap(scheme)
			related.Insert(child)
			r := &PostgresBranchReconciler{Config: &config.Config{CNPG: config.CNPG{WalBucketPrefix: "wal"}}, Scheme: scheme, Recorder: recorder}
			actions, result, err := r.Delete(branch, PostgresBranchPreparedData{PostgresDeleting: true}, related)
			requireNoError(t, err)
			if result.IsZero() || len(actions) != 1 {
				t.Fatalf("Delete() = %d actions, %v; want cleanup and requeue", len(actions), result)
			}
		})
	}
}
