package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/synchronizer"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/yaml"
)

func TestPostgresControllersStartWithoutBranchCRD(t *testing.T) {
	// This separate API server deliberately never installs PostgresBranch. It
	// proves that manager startup/cache sync, not just direct method calls, work.
	environment := &envtest.Environment{CRDDirectoryPaths: []string{"./testdata/external-crds"}, ErrorIfCRDPathMissing: true, BinaryAssetsDirectory: getEnvTestBinaryDir()}
	paths, err := filepath.Glob("../../config/crd/bases/*.yaml")
	requireNoError(t, err)
	for _, path := range paths {
		if strings.Contains(filepath.Base(path), "postgresbranches") {
			continue
		}
		data, err := os.ReadFile(path)
		requireNoError(t, err)
		crd := &apiextensionsv1.CustomResourceDefinition{}
		requireNoError(t, yaml.Unmarshal(data, crd))
		environment.CRDs = append(environment.CRDs, crd)
	}
	configuration, err := environment.Start()
	requireNoError(t, err)
	t.Cleanup(func() { requireNoError(t, environment.Stop()) })
	crds, err := apiextensionsclient.NewForConfig(configuration)
	requireNoError(t, err)
	if _, err := crds.ApiextensionsV1().CustomResourceDefinitions().Get(t.Context(), "postgresbranches.nais.io", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("isolated direct-controller server must not install the branch CRD: %v", err)
	}
	mgr, err := ctrl.NewManager(configuration, ctrl.Options{Scheme: scheme.Scheme, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0"})
	requireNoError(t, err)
	c, err := client.New(configuration, client.Options{Scheme: scheme.Scheme})
	requireNoError(t, err)
	cfg := &config.Config{APIServerIP: "172.16.0.2/32", CNPG: config.CNPG{ImageCatalogName: "postgresql", StorageClass: "hyperdisk-balanced"}}
	r := &PostgresClusterReconciler{Config: cfg, Scheme: scheme.Scheme, Recorder: recorder}
	requireNoError(t, synchronizer.NewSynchronizer(c, scheme.Scheme, r, recorder).SetupWithManager(mgr))
	requireNoError(t, synchronizer.NewSynchronizer(c, scheme.Scheme, &PostgresBindingReconciler{Scheme: scheme.Scheme, Recorder: recorder}, recorder).SetupWithManager(mgr))
	requireNoError(t, synchronizer.NewSynchronizer(c, scheme.Scheme, makePostgresAccessReconciler(), recorder).SetupWithManager(mgr))
	ctx, cancel := context.WithCancel(t.Context())
	finished := make(chan error, 1)
	go func() { defer close(finished); finished <- mgr.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-finished:
			requireNoError(t, err)
		case <-time.After(10 * time.Second):
			t.Error("manager did not stop")
		}
	})
	pg := &v1.Postgres{ObjectMeta: metav1.ObjectMeta{Name: "no-branch-crd", Namespace: "default"}, Spec: v1.PostgresSpec{MajorVersion: "18"}}
	requireNoError(t, c.Create(ctx, pg))
	if err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		select {
		case err := <-finished:
			return false, fmt.Errorf("manager exited before reconciliation: %v", err)
		default:
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(pg), pg); err != nil {
			return false, err
		}
		return pg.Status != nil && pg.Status.Ownership != nil && pg.Status.Ownership.Phase == ownershipManaged, nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(pg.Status.Ownership.Resources) == 0 || pg.Status.Ownership.ClusterUID == "" {
		t.Fatal("database was not checkpointed without branch CRD")
	}
}
