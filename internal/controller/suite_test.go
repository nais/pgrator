package controller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nais/liberator/pkg/crd"
	liberator_scheme "github.com/nais/liberator/pkg/scheme"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	pov1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	acid_zalan_do_v1 "github.com/zalando/postgres-operator/pkg/apis/acid.zalan.do/v1"
	apiextensions_v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	k8sevents "k8s.io/client-go/tools/events"
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
	recorder  *k8sevents.FakeRecorder
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	_, err = liberator_scheme.AddAll(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = pov1.AddToScheme(scheme.Scheme)
	utilruntime.Must(err)

	err = acid_zalan_do_v1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	pgCrd := acid_zalan_do_v1.PostgresCRD([]string{"all"})

	By("bootstrapping test environment")
	crdPath := crd.YamlDirectory()
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{crdPath},
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
	recorder = k8sevents.NewFakeRecorder(100)
	Expect(recorder).NotTo(BeNil())
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
