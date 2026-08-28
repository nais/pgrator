package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/golden"
	"github.com/nais/pgrator/internal/initscheme"
	"github.com/nais/pgrator/internal/synchronizer/events"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	kevents "k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
	recorder  events.Recorder

	// Golden test instances
	postgresGolden        *golden.Golden[*v1.Postgres, PostgresPreparedData]
	postgresBindingGolden *golden.Golden[*v1.PostgresBinding, PostgresBindingPreparedData]
	valkeyGolden          *golden.Golden[*v1.Valkey, ValkeyPreparedData]
	opensearchGolden      *golden.Golden[*v1.OpenSearch, OpenSearchPreparedData]
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	postgresConfig := config.Config{
		APIServerIP: "172.16.0.2/32",
		CNPG: config.CNPG{
			ImageCatalogName: "postgresql",
			StorageClass:     "hyperdisk-balanced",
		},
	}
	postgresReconciler := &PostgresReconciler{
		Config:   &postgresConfig,
		Recorder: recorder,
		Scheme:   scheme.Scheme,
	}

	postgresBindingReconciler := &PostgresBindingReconciler{
		Recorder: recorder,
		Scheme:   scheme.Scheme,
	}

	valkeyReconciler := &ValkeyReconciler{
		Aiven: config.Aiven{
			Project:                      "test-project",
			ProjectVPCID:                 "test-vpc-id",
			MetricsDestinationEndpointID: "test-metrics-service",
		},
		Tenant:   config.Tenant{Name: "test-tenant"},
		Recorder: recorder,
		Scheme:   scheme.Scheme,
	}

	opensearchReconciler := &OpenSearchReconciler{
		Aiven: config.Aiven{
			Project:                      "test-project",
			ProjectVPCID:                 "test-vpc-id",
			MetricsDestinationEndpointID: "test-metrics-service",
		},
		Tenant:   config.Tenant{Name: "test-tenant"},
		Recorder: recorder,
		Scheme:   scheme.Scheme,
	}

	_, filename, _, _ := runtime.Caller(0)
	testDataDir := filepath.Clean(filepath.Join(filepath.Dir(filename), "testdata/"))
	postgresTestDataDir := filepath.Join(testDataDir, "postgres")
	valkeyTestDataDir := filepath.Join(testDataDir, "valkey")
	opensearchTestDataDir := filepath.Join(testDataDir, "opensearch")

	postgresGolden = golden.NewGolden(t, postgresReconciler, postgresTestDataDir,
		postgresConfig,
		func(cfg config.Config) { *postgresReconciler.Config = cfg },
	)
	postgresGolden.DefineTests()

	postgresBindingGolden = golden.NewGolden(t, postgresBindingReconciler,
		filepath.Join(testDataDir, "postgresbinding"),
		config.Config{},
		func(config.Config) {},
	)
	postgresBindingGolden.DefineTests()

	valkeyGolden = golden.NewGolden(t, valkeyReconciler, valkeyTestDataDir,
		config.Config{
			Aiven: config.Aiven{
				Project:                      "test-project",
				ProjectVPCID:                 "test-vpc-id",
				MetricsDestinationEndpointID: "test-metrics-service",
			},
			Tenant: config.Tenant{Name: "test-tenant"},
		},
		func(cfg config.Config) {
			valkeyReconciler.Aiven = cfg.Aiven
			valkeyReconciler.Tenant = cfg.Tenant
		},
	)
	valkeyGolden.DefineTests()

	opensearchGolden = golden.NewGolden(t, opensearchReconciler, opensearchTestDataDir,
		config.Config{
			Aiven: config.Aiven{
				Project:                      "test-project",
				ProjectVPCID:                 "test-vpc-id",
				MetricsDestinationEndpointID: "test-metrics-service",
			},
			Tenant: config.Tenant{Name: "test-tenant"},
		},
		func(cfg config.Config) {
			opensearchReconciler.Aiven = cfg.Aiven
			opensearchReconciler.Tenant = cfg.Tenant
		},
	)
	opensearchGolden.DefineTests()

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.Background())

	initscheme.InitScheme(scheme.Scheme)

	// +kubebuilder:scaffold:scheme

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			"../../config/crd/bases",
			"./testdata/external-crds",
		},
		ErrorIfCRDPathMissing: true,
	}

	// Retrieve the first found binary directory to allow running tests from IDEs
	envTestBinaryDir := getEnvTestBinaryDir()
	if envTestBinaryDir != "" {
		testEnv.BinaryAssetsDirectory = envTestBinaryDir
	}

	// Explicitly set advertise-address to avoid reading /proc/net/route, which
	// may be restricted in some sandbox environments.
	testEnv.ControlPlane.GetAPIServer().Configure().Set("advertise-address", "127.0.0.1")

	// cfg is defined in this file globally.
	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	recorder = events.NewRecorder(kevents.NewFakeRecorder(1000))
	Expect(recorder).NotTo(BeNil())

	err = postgresGolden.ParseData(k8sClient.Scheme())
	Expect(err).NotTo(HaveOccurred())

	err = postgresBindingGolden.ParseData(k8sClient.Scheme())
	Expect(err).NotTo(HaveOccurred())

	err = valkeyGolden.ParseData(k8sClient.Scheme())
	Expect(err).NotTo(HaveOccurred())

	err = opensearchGolden.ParseData(k8sClient.Scheme())
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// getEnvTestBinaryDir locates the first binary in the specified path.
// ENVTEST-based tests depend on specific binaries, usually located in paths set by
// controller-runtime. When running tests directly (e.g., via an IDE) without using
// build scripts to set things up for us, the 'BinaryAssetsDirectory' must be
// explicitly configured.
//
// This function streamlines the process by finding the required binaries, similar to
// setting the 'KUBEBUILDER_ASSETS' environment variable. To ensure the binaries are
// properly set up, run 'setup-envtest' beforehand.
func getEnvTestBinaryDir() string {
	assetDir := os.Getenv("KUBEBUILDER_ASSETS")
	if assetDir != "" {
		return assetDir
	}

	envtestK8sVersion := os.Getenv("ENVTEST_K8S_VERSION")

	storeDir, err := defaultStoreDir()
	if err != nil {
		logf.Log.Error(err, "Failed to get default directory for envtest, looking locally")
	}
	candidates := []string{storeDir, filepath.Join("..", "..", "bin")}
	for _, candidate := range candidates {
		basePath := filepath.Join(candidate, "k8s")
		entries, err := os.ReadDir(basePath)
		if err != nil {
			logf.Log.Error(err, "Failed to read directory", "path", basePath)
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() && strings.Contains(entry.Name(), envtestK8sVersion) {
				return filepath.Join(basePath, entry.Name())
			}
		}
	}
	return ""
}

// defaultStoreDir returns the default location for the store.
//
// - Windows: %LocalAppData%\kubebuilder-envtest
// - OSX: ~/Library/Application Support/io.kubebuilder.envtest
// - Others: ${XDG_DATA_HOME:-~/.local/share}/kubebuilder-envtest
func defaultStoreDir() (string, error) {
	var baseDir string

	// find the base data directory
	switch runtime.GOOS {
	case "darwin", "ios":
		homeDir := os.Getenv("HOME")
		if homeDir == "" {
			return "", errors.New("$HOME is not defined")
		}
		return filepath.Join(homeDir, "Library/Application Support/io.kubebuilder.envtest"), nil
	case "windows":
		baseDir = os.Getenv("LocalAppData")
		if baseDir == "" {
			return "", errors.New("%LocalAppData% is not defined")
		}
	default:
		baseDir = os.Getenv("XDG_DATA_HOME")
		if baseDir == "" {
			homeDir := os.Getenv("HOME")
			if homeDir == "" {
				return "", errors.New("neither $XDG_DATA_HOME nor $HOME are defined")
			}
			baseDir = filepath.Join(homeDir, ".local/share")
		}
	}

	return filepath.Join(baseDir, "kubebuilder-envtest"), nil
}
