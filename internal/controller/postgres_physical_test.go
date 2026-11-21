package controller

import (
	"context"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/cloudnative-pg/api/v1"
	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/initscheme"
	rccnpg "github.com/nais/pgrator/internal/resourcecreator/cnpg"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	storagecnrm "github.com/nais/pgrator/internal/thirdparty/google/storage/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

const testPostgresUID = types.UID("feedab1e-beef-cafe-babe-700d1e100d1e")

// renderPhysical renders the physical resources of Postgres "mydb" in "myteam".
func renderPhysical(t *testing.T, cfg *config.Config, spec v1.PostgresSpec, related *relatedobjectsmap.RelatedObjectsMap) []action.Action {
	t.Helper()
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	if related == nil {
		related = relatedobjectsmap.NewRelatedObjectsMap(scheme)
	}
	owner := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "mydb", Namespace: "myteam", UID: testPostgresUID}, Spec: spec}
	identity := physicalIdentity{Name: v1.PostgresPhysicalName(owner.Name), Namespace: owner.Namespace, Postgres: owner.Name}
	inputs := physicalInputs{PostgresUID: owner.UID, PostgresName: owner.Name, PostgresSpec: spec, TeamGoogleProjectID: "team-gcp-project"}
	actions, err := (&physicalRenderer{Config: cfg, Scheme: scheme, Recorder: recorder}).physicalActions(identity, owner, inputs, related)
	requireNoError(t, err)
	for _, a := range actions {
		if !metav1.IsControlledBy(a.GetObject(), owner) {
			t.Errorf("%T %s is not controlled by the owning Postgres", a.GetObject(), a.GetObject().GetName())
		}
	}
	return actions
}

func walConfig() *config.Config {
	return &config.Config{
		GoogleProjectID: "cluster-gcp-project",
		Google:          config.Google{Location: "europe-north1"},
		CNPG:            config.CNPG{WalBucketPrefix: "wal-bucket-prefix"},
	}
}

func TestPersonalAccessGroupHasNoLoginOrPrivileges(t *testing.T) {
	identity := physicalIdentity{Name: v1.PostgresPhysicalName("orders"), Namespace: "team"}
	group := createPersonalAccessGroupRole(identity)
	role := group.Spec.RoleConfiguration
	if group.Spec.ClusterRef.Name != rccnpg.ClusterNameFor(identity.Name) || role.Name != rccnpg.PersonalAccessRole(identity.Name) {
		t.Errorf("personal group not scoped to its physical database: %#v", group.Spec)
	}
	if role.Login || !role.DisablePassword || role.Superuser || role.CreateDB || role.CreateRole || role.Replication || role.BypassRLS || len(role.InRoles) != 0 || group.Spec.ClientCertificate != nil {
		t.Errorf("personal authentication group can log in or grant privileges: %#v", group.Spec)
	}
	if group.Spec.ReclaimPolicy != cnpgv1.DatabaseRoleReclaimDelete {
		t.Errorf("personal authentication group reclaim policy = %q", group.Spec.ReclaimPolicy)
	}
}

func TestPhysicalResourcesCarryIdentityAndBillingLabels(t *testing.T) {
	for _, ha := range []bool{false, true} {
		actions := renderPhysical(t, &config.Config{CNPG: config.CNPG{ImageCatalogName: "postgresql", StorageClass: "standard-rwo"}}, v1.PostgresSpec{
			MajorVersion: "18", HighAvailability: ha,
			Resources: v1.PostgresResources{Cpu: apiresource.MustParse("100m"), Memory: apiresource.MustParse("512Mi"), DiskSize: apiresource.MustParse("10Gi")},
		}, nil)
		var foundCluster, foundPooler bool
		for _, a := range actions {
			switch obj := a.GetObject().(type) {
			case *cnpgv1.Cluster:
				foundCluster = true
				labels := obj.Spec.InheritedMetadata.Labels
				// The branch label is a durable metric label; its value stays main.
				if labels["postgres.nais.io/name"] != "mydb" || labels["postgres.nais.io/branch"] != "main" {
					t.Errorf("inherited Postgres identity labels = %v", labels)
				}
				if labels["kyverno.policy.exclusion.nais.io/restrict-volume-types"] != "true" {
					t.Errorf("inherited labels = %v, want restrict-volume-types Kyverno exclusion", labels)
				}
				want := 2
				if ha {
					want = 3
				}
				if obj.Spec.Instances != want {
					t.Errorf("instances = %d, want %d", obj.Spec.Instances, want)
				}
			case *cnpgv1.Pooler:
				foundPooler = true
				labels := obj.Spec.Template.ObjectMeta.Labels
				if labels["postgres.nais.io/name"] != "mydb" || labels["apiserver-access"] != "enabled" {
					t.Errorf("pooler pod labels = %v, want billing identity and API server access", labels)
				}
			}
		}
		if !foundCluster || !foundPooler {
			t.Fatalf("rendered Cluster: %t, Pooler: %t; want both", foundCluster, foundPooler)
		}
	}
}

func TestContinuousArchivingReady(t *testing.T) {
	for _, tt := range []struct {
		name       string
		conditions []metav1.Condition
		want       bool
	}{
		{name: "is not ready without a condition"},
		{name: "is not ready when false", conditions: []metav1.Condition{{Type: string(cnpgv1.ConditionContinuousArchiving), Status: metav1.ConditionFalse}}},
		{name: "is ready when true", conditions: []metav1.Condition{{Type: string(cnpgv1.ConditionContinuousArchiving), Status: metav1.ConditionTrue}}, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := continuousArchivingReady(&cnpgv1.Cluster{Status: cnpgv1.ClusterStatus{Conditions: tt.conditions}}); got != tt.want {
				t.Errorf("continuousArchivingReady() = %t, want %t", got, tt.want)
			}
		})
	}
	if continuousArchivingReady(nil) {
		t.Error("missing cluster is ready for archiving")
	}
}

func TestRenderRepairsExistingWALBucketAccess(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	identity := physicalIdentity{Name: v1.PostgresPhysicalName("mydb"), Namespace: "myteam"}
	related := relatedobjectsmap.NewRelatedObjectsMap(scheme)
	related.Insert(&storagecnrm.StorageBucket{ObjectMeta: metav1.ObjectMeta{
		Name: (&physicalRenderer{Config: walConfig()}).bucketName(identity, physicalInputs{PostgresUID: testPostgresUID}), Namespace: "myteam",
	}})
	for _, a := range renderPhysical(t, walConfig(), v1.PostgresSpec{MajorVersion: "18"}, related) {
		if bucket, ok := a.GetObject().(*storagecnrm.StorageBucket); ok {
			if !bucket.Spec.UniformBucketLevelAccess {
				t.Fatal("existing WAL bucket was not reconciled with uniform bucket-level access")
			}
			return
		}
	}
	t.Fatal("existing WAL bucket was not reconciled")
}

func TestScheduledBackupWaitsForContinuousArchiving(t *testing.T) {
	name := rccnpg.ClusterNameFor(v1.PostgresPhysicalName("mydb"))
	for _, tt := range []struct {
		name              string
		continuousArchive bool
		existingBackup    bool
		want              bool
	}{
		{name: "not before continuous archiving is ready"},
		{name: "creates after continuous archiving is ready", continuousArchive: true, want: true},
		{name: "keeps existing backup if archiving later becomes unavailable", existingBackup: true, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			initscheme.InitScheme(scheme)
			related := relatedobjectsmap.NewRelatedObjectsMap(scheme)
			if tt.continuousArchive {
				related.Insert(&cnpgv1.Cluster{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "myteam"},
					Status:     cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{Type: string(cnpgv1.ConditionContinuousArchiving), Status: metav1.ConditionTrue}}},
				})
			}
			if tt.existingBackup {
				related.Insert(&cnpgv1.ScheduledBackup{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "myteam"}})
			}
			got := false
			for _, a := range renderPhysical(t, walConfig(), v1.PostgresSpec{MajorVersion: "18"}, related) {
				if _, ok := a.GetObject().(*cnpgv1.ScheduledBackup); ok {
					got = true
				}
			}
			if got != tt.want {
				t.Errorf("ScheduledBackup rendered = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestClusterStatusChangesTriggerReconcile(t *testing.T) {
	filter := postgresPhysicalOwnedTypes(false)[0].AdditionalPredicate
	for _, tc := range []struct {
		name   string
		change func(*cnpgv1.Cluster)
		want   bool
	}{
		{name: "unchanged"},
		{name: "phase", want: true, change: func(c *cnpgv1.Cluster) { c.Status.Phase = cnpgv1.PhaseUnrecoverable }},
		{name: "phase reason", want: true, change: func(c *cnpgv1.Cluster) { c.Status.PhaseReason = "recovery job failed" }},
		{name: "ready reason", want: true, change: func(c *cnpgv1.Cluster) { c.Status.Conditions[0].Reason = "ClusterUnrecoverable" }},
		{name: "initialized", want: true, change: func(c *cnpgv1.Cluster) {
			c.Status.Conditions = append(c.Status.Conditions, metav1.Condition{Type: string(cnpgv1.ConditionInitialized), Status: metav1.ConditionFalse})
		}},
		{name: "continuous archiving", want: true, change: func(c *cnpgv1.Cluster) {
			c.Status.Conditions = append(c.Status.Conditions, metav1.Condition{Type: string(cnpgv1.ConditionContinuousArchiving), Status: metav1.ConditionTrue})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := &cnpgv1.Cluster{Status: cnpgv1.ClusterStatus{Conditions: []metav1.Condition{{Type: string(cnpgv1.ConditionClusterReady), Status: metav1.ConditionFalse}}}}
			updated := old.DeepCopy()
			if tc.change != nil {
				tc.change(updated)
			}
			if got := filter.Update(event.UpdateEvent{ObjectOld: old, ObjectNew: updated}); got != tc.want {
				t.Errorf("Update() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestRecoveryBootstrapFailedRequiresFailedJobOfThisCluster(t *testing.T) {
	scheme := runtime.NewScheme()
	initscheme.InitScheme(scheme)
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "pg-restored", Namespace: "team", UID: "cluster-uid"},
		Spec:       cnpgv1.ClusterSpec{Bootstrap: &cnpgv1.BootstrapConfiguration{Recovery: &cnpgv1.BootstrapRecovery{Source: "source"}}},
		Status:     cnpgv1.ClusterStatus{Phase: cnpgv1.PhaseUnrecoverable},
	}
	failedJob := func() *batchv1.Job {
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: cluster.Name + "-1-full-recovery", Namespace: cluster.Namespace, Labels: map[string]string{
				recoveryJobRoleLabel: recoveryJobRole, recoveryJobClusterLabel: cluster.Name,
			}},
			Status: batchv1.JobStatus{Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}},
		}
		requireNoError(t, controllerutil.SetControllerReference(cluster, job, scheme))
		return job
	}
	for _, tc := range []struct {
		name    string
		cluster func(*cnpgv1.Cluster)
		job     func(*batchv1.Job)
		noJob   bool
		want    bool
	}{
		{name: "failed recovery job", want: true},
		{name: "no job evidence", noJob: true},
		{name: "job not failed", job: func(j *batchv1.Job) { j.Status.Conditions = nil }},
		{name: "replica join job", job: func(j *batchv1.Job) { j.Labels[recoveryJobRoleLabel] = "join" }},
		{name: "other cluster's job", job: func(j *batchv1.Job) { j.Labels[recoveryJobClusterLabel] = "other-cluster" }},
		{name: "job of a previous cluster", job: func(j *batchv1.Job) { j.OwnerReferences[0].UID = "old-uid" }},
		{name: "cluster not unrecoverable", cluster: func(c *cnpgv1.Cluster) { c.Status.Phase = cnpgv1.PhaseFirstPrimary }},
		{name: "not a recovery bootstrap", cluster: func(c *cnpgv1.Cluster) {
			c.Spec.Bootstrap = &cnpgv1.BootstrapConfiguration{InitDB: &cnpgv1.BootstrapInitDB{}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cluster.DeepCopy()
			if tc.cluster != nil {
				tc.cluster(c)
			}
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if !tc.noJob {
				job := failedJob()
				if tc.job != nil {
					tc.job(job)
				}
				builder = builder.WithObjects(job)
			}
			got, err := recoveryBootstrapFailed(context.Background(), builder.Build(), c)
			requireNoError(t, err)
			if got != tc.want {
				t.Errorf("recoveryBootstrapFailed() = %t, want %t", got, tc.want)
			}
		})
	}
}
