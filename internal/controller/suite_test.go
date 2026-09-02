package controller

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/nais/pgrator/internal/config"
	"github.com/nais/pgrator/internal/golden"
	"github.com/nais/pgrator/internal/initscheme"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/nais/pgrator/internal/synchronizer/events"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	v1 "github.com/nais/pgrator/pkg/api/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	kevents "k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/yaml"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
	recorder  events.Recorder
)

func TestMain(m *testing.M) {
	logf.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	if err := setupTestEnvironment(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "setup test environment: %v\n", err)
		os.Exit(1)
	}

	exitCode := m.Run()

	if err := tearDownTestEnvironment(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "tear down test environment: %v\n", err)
		os.Exit(1)
	}

	os.Exit(exitCode)
}

func setupTestEnvironment() error {
	ctx, cancel = context.WithCancel(context.Background())

	initscheme.InitScheme(scheme.Scheme)

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			"../../config/crd/bases",
			"./testdata/external-crds",
		},
		ErrorIfCRDPathMissing: true,
	}

	envTestBinaryDir := getEnvTestBinaryDir()
	if envTestBinaryDir != "" {
		testEnv.BinaryAssetsDirectory = envTestBinaryDir
	}

	testEnv.ControlPlane.GetAPIServer().Configure().Set("advertise-address", "127.0.0.1")

	var err error
	cfg, err = testEnv.Start()
	if err != nil {
		return fmt.Errorf("start envtest: %w", err)
	}
	if cfg == nil {
		return errors.New("envtest returned nil rest config")
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return fmt.Errorf("create k8s client: %w", err)
	}

	recorder = events.NewRecorder(kevents.NewFakeRecorder(1000))
	if recorder == nil {
		return errors.New("events recorder is nil")
	}

	return nil
}

func tearDownTestEnvironment() error {
	if cancel != nil {
		cancel()
	}
	if testEnv != nil {
		if err := testEnv.Stop(); err != nil {
			return fmt.Errorf("stop envtest: %w", err)
		}
	}
	return nil
}

func TestGoldenPostgres(t *testing.T) {
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

	runGoldenTestsForResource[*v1.Postgres, PostgresPreparedData, v1.Postgres](
		t,
		postgresReconciler,
		"postgres",
		postgresConfig,
		func(cfg config.Config) { *postgresReconciler.Config = cfg },
	)
}

func TestGoldenPostgresBinding(t *testing.T) {
	postgresBindingReconciler := &PostgresBindingReconciler{
		Recorder: recorder,
		Scheme:   scheme.Scheme,
	}

	runGoldenTestsForResource[*v1.PostgresBinding, PostgresBindingPreparedData, v1.PostgresBinding](
		t,
		postgresBindingReconciler,
		"postgresbinding",
		config.Config{},
		func(config.Config) {},
	)
}

func TestGoldenValkey(t *testing.T) {
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

	defaultCfg := config.Config{
		Aiven: config.Aiven{
			Project:                      "test-project",
			ProjectVPCID:                 "test-vpc-id",
			MetricsDestinationEndpointID: "test-metrics-service",
		},
		Tenant: config.Tenant{Name: "test-tenant"},
	}

	runGoldenTestsForResource[*v1.Valkey, ValkeyPreparedData, v1.Valkey](
		t,
		valkeyReconciler,
		"valkey",
		defaultCfg,
		func(cfg config.Config) {
			valkeyReconciler.Aiven = cfg.Aiven
			valkeyReconciler.Tenant = cfg.Tenant
		},
	)
}

func TestGoldenOpenSearch(t *testing.T) {
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

	defaultCfg := config.Config{
		Aiven: config.Aiven{
			Project:                      "test-project",
			ProjectVPCID:                 "test-vpc-id",
			MetricsDestinationEndpointID: "test-metrics-service",
		},
		Tenant: config.Tenant{Name: "test-tenant"},
	}

	runGoldenTestsForResource[*v1.OpenSearch, OpenSearchPreparedData, v1.OpenSearch](
		t,
		opensearchReconciler,
		"opensearch",
		defaultCfg,
		func(cfg config.Config) {
			opensearchReconciler.Aiven = cfg.Aiven
			opensearchReconciler.Tenant = cfg.Tenant
		},
	)
}

type goldenCompareKey struct {
	Action    string
	Kind      string
	Name      string
	Namespace string
}

type goldenCase[T any, P any] struct {
	name           string
	cfg            config.Config
	object         T
	preparedData   P
	relatedObjects *relatedobjectsmap.RelatedObjectsMap
	contains       []*golden.Expected
	consistsOf     []*golden.Expected
}

func runGoldenTestsForResource[T interface {
	client.Object
	*TObj
}, P any, TObj any](
	t *testing.T,
	r reconciler.Reconciler[T, P],
	resourceDir string,
	defaultCfg config.Config,
	applyCfg func(config.Config),
) {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve test file path")
	}

	testDataDir := filepath.Clean(filepath.Join(filepath.Dir(filename), "testdata", resourceDir))
	cases := loadGoldenCases[T, P, TObj](t, testDataDir, defaultCfg)
	if len(cases) == 0 {
		t.Fatalf("no golden cases found in %q", testDataDir)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			applyCfg(tc.cfg)

			actions, _, err := r.Update(tc.object, tc.preparedData, tc.relatedObjects)
			if err != nil {
				t.Fatalf("update: %v", err)
			}

			actualByKey := make(map[goldenCompareKey]action.Action, len(actions))
			for _, a := range actions {
				k := actionKey(a)
				if _, exists := actualByKey[k]; exists {
					t.Fatalf("duplicate action key: %+v", k)
				}
				actualByKey[k] = a
			}

			if len(tc.consistsOf) > 0 {
				wantKeys := make([]goldenCompareKey, 0, len(tc.consistsOf))
				for _, expected := range tc.consistsOf {
					wantKeys = append(wantKeys, expectedKey(expected))
				}

				gotKeys := slices.Collect(maps.Keys(actualByKey))
				slices.SortFunc(gotKeys, compareGoldenCompareKey)
				slices.SortFunc(wantKeys, compareGoldenCompareKey)
				if !slices.Equal(gotKeys, wantKeys) {
					t.Fatalf("action keys mismatch\n got:  %+v\n want: %+v", gotKeys, wantKeys)
				}

				for _, expected := range tc.consistsOf {
					k := expectedKey(expected)
					actual := actualByKey[k]
					ok, err := expected.Match(actual)
					if err != nil {
						t.Fatalf("matching action %+v: %v", k, err)
					}
					if !ok {
						t.Fatalf("action %+v mismatch: %s", k, expected.FailureMessage(actual))
					}
				}
				return
			}

			for _, expected := range tc.contains {
				k := expectedKey(expected)
				actual, found := actualByKey[k]
				if !found {
					t.Fatalf("missing expected action key: %+v", k)
				}
				ok, err := expected.Match(actual)
				if err != nil {
					t.Fatalf("matching action %+v: %v", k, err)
				}
				if !ok {
					t.Fatalf("action %+v mismatch: %s", k, expected.FailureMessage(actual))
				}
			}
		})
	}
}

func loadGoldenCases[T interface {
	client.Object
	*TObj
}, P any, TObj any](t *testing.T, testDataDir string, defaultCfg config.Config) []goldenCase[T, P] {
	t.Helper()

	entries, err := os.ReadDir(testDataDir)
	if err != nil {
		t.Fatalf("read test data dir %q: %v", testDataDir, err)
	}

	cases := make([]goldenCase[T, P], 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.Contains(entry.Name(), "external-crds") {
			continue
		}

		caseName := entry.Name()
		caseDir := filepath.Join(testDataDir, caseName)

		obj := new(TObj)
		mustUnmarshalYAMLFile(t, filepath.Join(caseDir, "object.yaml"), obj)

		var preparedData P
		preparedPath := filepath.Join(caseDir, "prepared_data.yaml")
		if data, readErr := os.ReadFile(preparedPath); readErr == nil {
			if err := yaml.Unmarshal(data, &preparedData); err != nil {
				t.Fatalf("unmarshal prepared data in %q: %v", preparedPath, err)
			}
		} else if !os.IsNotExist(readErr) {
			t.Fatalf("read prepared data in %q: %v", preparedPath, readErr)
		}

		cfg := defaultCfg
		configPath := filepath.Join(caseDir, "config.yaml")
		if data, readErr := os.ReadFile(configPath); readErr == nil {
			if err := yaml.UnmarshalStrict(data, &cfg); err != nil {
				t.Fatalf("unmarshal strict config in %q: %v", configPath, err)
			}
		} else if !os.IsNotExist(readErr) {
			t.Fatalf("read config in %q: %v", configPath, readErr)
		}

		contains := loadExpectedList(t, filepath.Join(caseDir, "contains"), caseName)
		consistsOf := loadExpectedList(t, filepath.Join(caseDir, "consists_of"), caseName)
		relatedObjects := loadRelatedObjects(t, filepath.Join(caseDir, "related_objects"))

		cases = append(cases, goldenCase[T, P]{
			name:           caseName,
			cfg:            cfg,
			object:         any(obj).(T),
			preparedData:   preparedData,
			relatedObjects: relatedObjects,
			contains:       contains,
			consistsOf:     consistsOf,
		})
	}

	return cases
}

func loadExpectedList(t *testing.T, dir string, testCaseName string) []*golden.Expected {
	t.Helper()

	docs := loadYAMLMaps(t, dir)
	results := make([]*golden.Expected, 0, len(docs))
	for _, doc := range docs {
		expected, err := golden.ParseExpected(scheme.Scheme, doc, testCaseName)
		if err != nil {
			t.Fatalf("parse expected in %q (%s): %v", dir, testCaseName, err)
		}
		results = append(results, expected)
	}

	return results
}

func loadRelatedObjects(t *testing.T, dir string) *relatedobjectsmap.RelatedObjectsMap {
	t.Helper()

	rom := relatedobjectsmap.NewRelatedObjectsMap(scheme.Scheme)
	docs := loadYAMLMaps(t, dir)
	for _, doc := range docs {
		obj, err := golden.ParseObject(scheme.Scheme, doc)
		if err != nil {
			t.Fatalf("parse related object in %q: %v", dir, err)
		}
		rom.Insert(obj.(client.Object))
	}
	return rom
}

func loadYAMLMaps(t *testing.T, dir string) []map[string]any {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read dir %q: %v", dir, err)
	}

	docs := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read file %q: %v", path, err)
		}
		doc := make(map[string]any)
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatalf("unmarshal yaml in %q: %v", path, err)
		}
		docs = append(docs, doc)
	}
	return docs
}

func mustUnmarshalYAMLFile(t *testing.T, path string, target any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read yaml file %q: %v", path, err)
	}
	if err := yaml.Unmarshal(data, target); err != nil {
		t.Fatalf("unmarshal yaml file %q: %v", path, err)
	}
}

func expectedKey(e *golden.Expected) goldenCompareKey {
	gvk := e.Object.GetObjectKind().GroupVersionKind()
	return goldenCompareKey{
		Action:    e.Action,
		Kind:      gvk.Kind,
		Name:      e.Object.GetName(),
		Namespace: e.Object.GetNamespace(),
	}
}

func actionKey(a action.Action) goldenCompareKey {
	obj := a.GetObject()
	gvk := obj.GetObjectKind().GroupVersionKind()
	return goldenCompareKey{
		Action:    getTypeName(a),
		Kind:      gvk.Kind,
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}
}

func getTypeName(actual any) string {
	t := reflect.TypeOf(actual)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}

func compareGoldenCompareKey(a goldenCompareKey, b goldenCompareKey) int {
	if c := strings.Compare(a.Action, b.Action); c != 0 {
		return c
	}
	if c := strings.Compare(a.Kind, b.Kind); c != 0 {
		return c
	}
	if c := strings.Compare(a.Namespace, b.Namespace); c != 0 {
		return c
	}
	return strings.Compare(a.Name, b.Name)
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireErrorContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got %q", substr, err.Error())
	}
}

func requireEqual[T comparable](t *testing.T, got T, want T, message string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v, want %v", message, got, want)
	}
}

func requireTrue(t *testing.T, condition bool, message string) {
	t.Helper()
	if !condition {
		t.Fatalf("expected true: %s", message)
	}
}

func requireFalse(t *testing.T, condition bool, message string) {
	t.Helper()
	if condition {
		t.Fatalf("expected false: %s", message)
	}
}

func requireNotNil(t *testing.T, value any, message string) {
	t.Helper()
	if isNil(value) {
		t.Fatalf("expected non-nil: %s", message)
	}
}

func requireNil(t *testing.T, value any, message string) {
	t.Helper()
	if !isNil(value) {
		t.Fatalf("expected nil: %s", message)
	}
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func requireSliceContains(t *testing.T, values []string, want string) {
	t.Helper()
	for _, v := range values {
		if v == want {
			return
		}
	}
	t.Fatalf("expected slice %v to contain %q", values, want)
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
