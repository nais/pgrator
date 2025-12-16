package golden

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nais/pgrator/internal/synchronizer/object"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

type TestData[T object.NaisObject, P any] struct {
	// Name of this test case (set from filename)
	Name string

	// Prepared data to pass to the reconciler under test
	PreparedData P

	// The actual object to reconcile
	Object T

	// Use this to match exactly the expected actions.
	// All actions must match one element in this list if present.
	// This field is populated by parseExpectedData, which must be called from a BeforeEach closure
	ConsistOf []*Expected
	// Raw data for above field, parsed by parseExpectedData
	consistsOfData []map[string]interface{}

	// Use this to match that some expected actions are present.
	// Additional actions might be present, and will be ignored for this test case.
	// This field is populated by parseExpectedData, which must be called from a BeforeEach closure
	Contains []*Expected
	// Raw data for above field, parsed by parseExpectedData
	containsData []map[string]interface{}
}

func (t *TestData[T, P]) parseExpectedData(scheme *runtime.Scheme) error {
	for _, datum := range t.consistsOfData {
		expected, err := ParseExpected(scheme, datum)
		if err != nil {
			return err
		}

		t.ConsistOf = append(t.ConsistOf, expected)
	}

	for _, datum := range t.containsData {
		expected, err := ParseExpected(scheme, datum)
		if err != nil {
			return err
		}

		t.Contains = append(t.Contains, expected)
	}
	return nil
}

func (t *TestData[T, P]) loadExpectedData(path string) error {
	var err error
	t.containsData, err = t.loadExpectedFromDir(filepath.Join(path, "contains"))
	if err != nil {
		return err
	}
	t.consistsOfData, err = t.loadExpectedFromDir(filepath.Join(path, "consists_of"))
	if err != nil {
		return err
	}
	return nil
}

func (t *TestData[T, P]) loadExpectedFromDir(path string) ([]map[string]interface{}, error) {
	files, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	results := make([]map[string]interface{}, 0, len(files))

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		name := file.Name()
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}

		var data []byte
		data, err = readFile(filepath.Join(path, name))
		if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}
		expected := make(map[string]interface{})
		err = yaml.Unmarshal(data, &expected)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal Yaml: %w", err)
		}
		results = append(results, expected)
	}

	return results, nil
}
