package controller

import (
	"context"
	"reflect"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/initscheme"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func TestPostgresBranchObservesConfiguration(t *testing.T) {
	for _, tt := range []struct {
		name       string
		missing    bool
		unowned    bool
		deleting   bool
		failed     bool
		parameters map[string]string
		want       *v1.PostgresBranchConfiguration
	}{
		{
			name:       "observed values replace previous configuration",
			parameters: map[string]string{"work_mem": "8MB", "pgaudit.log": "read,write,ddl,role"},
			want:       &v1.PostgresBranchConfiguration{Parameters: map[string]string{"work_mem": "8MB", "pgaudit.log": "read,write,ddl,role"}},
		},
		{name: "no parameters does not imply unavailable configuration", want: &v1.PostgresBranchConfiguration{Parameters: map[string]string{}}},
		{name: "missing cluster clears stale configuration", missing: true},
		{name: "unowned cluster is not published", unowned: true},
		{name: "terminating cluster clears stale configuration", deleting: true},
		{
			name: "failed recovery still refreshes configuration", failed: true,
			parameters: map[string]string{"work_mem": "16MB"},
			want:       &v1.PostgresBranchConfiguration{Parameters: map[string]string{"work_mem": "16MB"}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			branch := &v1.PostgresBranch{
				ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "main"), Namespace: "team", UID: "branch-uid"},
				Spec:       v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"},
				Status:     &v1.PostgresBranchStatus{Configuration: &v1.PostgresBranchConfiguration{Parameters: map[string]string{"obsolete": "old"}}},
			}
			if tt.failed {
				branch.Status.SetCondition(metav1.Condition{Type: recoveryFailedCondition, Status: metav1.ConditionTrue, Reason: "Unrecoverable"})
			}
			postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: branch.Namespace}}
			cluster := &cnpgv1.Cluster{
				ObjectMeta: metav1.ObjectMeta{Name: rccnpg.ClusterNameFor(branch.Name), Namespace: branch.Namespace},
				Spec:       cnpgv1.ClusterSpec{PostgresConfiguration: cnpgv1.PostgresConfiguration{Parameters: tt.parameters}},
			}
			if !tt.unowned {
				if err := controllerutil.SetControllerReference(branch, cluster, scheme); err != nil {
					t.Fatal(err)
				}
			}
			if tt.deleting {
				now := metav1.Now()
				cluster.DeletionTimestamp = &now
				cluster.Finalizers = []string{"test-finalizer"}
			}
			objects := []client.Object{postgres}
			if !tt.missing {
				objects = append(objects, cluster)
			}
			reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			r := &PostgresBranchReconciler{Config: &config.Config{}, Scheme: scheme}
			_, _, err := r.Prepare(context.Background(), reader, branch)
			if (err != nil) != tt.failed {
				t.Fatalf("Prepare() error = %v, want error %t", err, tt.failed)
			}
			if !reflect.DeepEqual(tt.want, branch.Status.Configuration) {
				t.Fatalf("configuration = %#v, want %#v", branch.Status.Configuration, tt.want)
			}
			if branch.Status.Configuration != nil && len(branch.Status.Configuration.Parameters) > 0 {
				copy := branch.DeepCopy()
				copy.Status.Configuration.Parameters["new"] = "value"
				if _, ok := branch.Status.Configuration.Parameters["new"]; ok {
					t.Fatal("deep copy shares configuration parameters")
				}
			}
		})
	}
}

func TestPostgresBranchConfigurationFollowsClusterChanges(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	branch := &v1.PostgresBranch{
		ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "main"), Namespace: "team", UID: "branch-uid"},
		Spec:       v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"},
	}
	postgres := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: branch.Namespace}}
	cluster := &cnpgv1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: rccnpg.ClusterNameFor(branch.Name), Namespace: branch.Namespace}}
	if err := controllerutil.SetControllerReference(branch, cluster, scheme); err != nil {
		t.Fatal(err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(postgres, cluster).Build()
	r := &PostgresBranchReconciler{Config: &config.Config{}, Scheme: scheme}
	prepare := func() {
		t.Helper()
		if _, _, err := r.Prepare(context.Background(), cl, branch); err != nil {
			t.Fatal(err)
		}
	}
	prepare()
	if branch.Status.Configuration == nil || len(branch.Status.Configuration.Parameters) != 0 {
		t.Fatal("existing cluster should publish empty configuration")
	}
	cluster.Spec.PostgresConfiguration.Parameters = map[string]string{"shared_buffers": "128MB"}
	if err := cl.Update(context.Background(), cluster); err != nil {
		t.Fatal(err)
	}
	prepare()
	if got := branch.Status.Configuration.Parameters["shared_buffers"]; got != "128MB" {
		t.Fatalf("updated shared_buffers = %q, want 128MB", got)
	}
	if err := cl.Delete(context.Background(), cluster); err != nil {
		t.Fatal(err)
	}
	prepare()
	if branch.Status.Configuration != nil {
		t.Fatal("deleted cluster should clear configuration")
	}
}

func TestPostgresBranchConfigurationStatusRoundTrip(t *testing.T) {
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "branch-configuration-"}}
	if err := k8sClient.Create(ctx, namespace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := k8sClient.Delete(ctx, namespace); err != nil {
			t.Error(err)
		}
	})
	branch := &v1.PostgresBranch{
		ObjectMeta: metav1.ObjectMeta{Name: v1.PostgresBranchObjectName("orders", "main"), Namespace: namespace.Name},
		Spec:       v1.PostgresBranchSpec{Postgres: "orders", BranchName: "main"},
	}
	if err := k8sClient.Create(ctx, branch); err != nil {
		t.Fatal(err)
	}
	for _, configuration := range []*v1.PostgresBranchConfiguration{
		{Parameters: map[string]string{"work_mem": "8MB", "pgaudit.log": "read,write,ddl,role"}},
		{Parameters: map[string]string{}},
		nil,
	} {
		branch.GetStatus()
		branch.Status.Configuration = configuration
		if err := k8sClient.Status().Update(ctx, branch); err != nil {
			t.Fatal(err)
		}
		observed := &v1.PostgresBranch{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(branch), observed); err != nil {
			t.Fatal(err)
		}
		observed.GetStatus()
		if !reflect.DeepEqual(configuration, observed.Status.Configuration) {
			t.Fatalf("stored configuration = %#v, want %#v", observed.Status.Configuration, configuration)
		}
		branch = observed
	}
}

func TestPostgresBranchObservesClusterTermination(t *testing.T) {
	r := &PostgresBranchReconciler{Config: &config.Config{}}
	cluster := &cnpgv1.Cluster{}
	terminating := cluster.DeepCopy()
	now := metav1.Now()
	terminating.DeletionTimestamp = &now
	filter := r.OwnedTypes()[0].AdditionalPredicate
	if !filter.Update(event.UpdateEvent{ObjectOld: cluster, ObjectNew: terminating}) {
		t.Fatal("cluster termination must trigger reconciliation to clear configuration")
	}
	if filter.Update(event.UpdateEvent{ObjectOld: cluster, ObjectNew: cluster.DeepCopy()}) {
		t.Fatal("unchanged cluster must not trigger extra reconciliation")
	}
}
