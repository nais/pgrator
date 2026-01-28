package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pov1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	apiextensions_v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/yaml"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/golden"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/pkg/api/datav1"
	aiven_v1alpha1 "github.com/nais/pgrator/pkg/api/thirdparty/aiven/v1alpha1"
	iam_cnrm_cloud_google_com_v1beta1 "github.com/nais/pgrator/pkg/api/thirdparty/google/v1beta1"
	v1 "github.com/nais/pgrator/pkg/api/v1"
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
	postgresGolden *golden.Golden[*datav1.Postgres, PreparedData]
	valkeyGolden   *golden.Golden[*v1.Valkey, ValkeyPreparedData]
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	postgresReconcilerConfig := config.Config{
		PrometheusRulesDisabled: true,
	}
	postgresReconciler := &PostgresReconciler{Config: &postgresReconcilerConfig, Recorder: recorder}

	valkeyReconciler := &ValkeyReconciler{
		Aiven: &config.Aiven{
			Project:                      "test-project",
			ProjectVPCID:                 "test-vpc-id",
			MetricsDestinationEndpointID: "test-metrics-service",
		},
		Tenant:   &config.Tenant{Name: "test-tenant"},
		Recorder: recorder,
		// TODO: scheme should be set up through a function for consistency with actual runtime use
		Scheme: scheme.Scheme,
	}

	_, filename, _, _ := runtime.Caller(0)
	testDataDir := filepath.Clean(filepath.Join(filepath.Dir(filename), "testdata/"))
	postgresTestDataDir := filepath.Join(testDataDir, "postgres")
	valkeyTestDataDir := filepath.Join(testDataDir, "valkey")

	postgresGolden = golden.NewGolden(t, postgresReconciler, postgresTestDataDir)
	postgresGolden.DefineTests()

	valkeyGolden = golden.NewGolden(t, valkeyReconciler, valkeyTestDataDir)
	valkeyGolden.DefineTests()

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.Background())

	var err error
	err = iam_cnrm_cloud_google_com_v1beta1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = datav1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = pov1.AddToScheme(scheme.Scheme)
	utilruntime.Must(err)

	err = acid_zalan_do_v1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = v1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = aiven_v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	pgCrd := acid_zalan_do_v1.PostgresCRD([]string{"all"})

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			"../../config/crd/bases",
			"./testdata/external-crds",
		},
		ErrorIfCRDPathMissing: true,
		CRDs:                  []*apiextensions_v1.CustomResourceDefinition{pgCrd},
	}

	// Retrieve the first found binary directory to allow running tests from IDEs
	envTestBinaryDir := getEnvTestBinaryDir()
	if envTestBinaryDir != "" {
		testEnv.BinaryAssetsDirectory = envTestBinaryDir
	}

	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Install ValidatingAdmissionPolicy for Valkey name validation
	err = installAdmissionPolicies(ctx, k8sClient)
	Expect(err).NotTo(HaveOccurred())
	recorder = events.NewRecorder(record.NewFakeRecorder(1000))
	Expect(recorder).NotTo(BeNil())

	err = postgresGolden.ParseData(k8sClient.Scheme())
	Expect(err).NotTo(HaveOccurred())

	err = valkeyGolden.ParseData(k8sClient.Scheme())
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// installAdmissionPolicies reads and installs ValidatingAdmissionPolicy resources from charts/admission
func installAdmissionPolicies(ctx context.Context, c client.Client) error {
	_, filename, _, _ := runtime.Caller(0)
	admissionDir := filepath.Join(filepath.Dir(filename), "../../charts/pgrator/templates/admission")

	entries, err := os.ReadDir(admissionDir)
	if err != nil {
		return err
	}

	// Regex to strip Helm template directives (lines containing {{ ... }})
	helmTemplateRegex := regexp.MustCompile(`(?m)^.*\{\{.*\}\}.*\n?`)

	for _, entry := range entries {
		if entry.IsDir() || (!strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml")) {
			continue
		}

		data, err := os.ReadFile(filepath.Join(admissionDir, entry.Name()))
		if err != nil {
			return err
		}

		// Strip Helm template directives before parsing
		cleanedData := helmTemplateRegex.ReplaceAll(data, nil)

		// Split YAML documents
		docs := strings.Split(string(cleanedData), "---")
		for _, doc := range docs {
			doc = strings.TrimSpace(doc)
			if doc == "" {
				continue
			}

			obj := &unstructured.Unstructured{}
			if err := yaml.Unmarshal([]byte(doc), obj); err != nil {
				return err
			}

			if err := c.Create(ctx, obj); err != nil {
				return err
			}
		}
	}

	return nil
}

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
