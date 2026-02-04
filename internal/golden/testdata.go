package golden

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nais/pgrator/internal/synchronizer/relatedobjectsmap"
	"github.com/nais/pgrator/pkg/api"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

type TestData[T api.NaisObject, P any] struct {
	// Name of this test case (set from filename)
	Name string

	// Prepared data to pass to the reconciler under test
	PreparedData P

	// Related objects to pass to the reconciler under test
	RelatedObjects *relatedobjectsmap.RelatedObjectsMap
	// Raw data for above field, parsed by parseExpectedData
	relatedObjectsData []map[string]any

	// The actual object to reconcile
	Object T

	// Use this to match exactly the expected actions.
	// All actions must match one element in this list if present.
	// This field is populated by parseExpectedData, which must be called from a BeforeEach closure
	ConsistOf []*Expected
	// Raw data for above field, parsed by parseExpectedData
	consistsOfData []map[string]any

	// Use this to match that some expected actions are present.
	// Additional actions might be present, and will be ignored for this test case.
	// This field is populated by parseExpectedData, which must be called from a BeforeEach closure
	Contains []*Expected
	// Raw data for above field, parsed by parseExpectedData
	containsData []map[string]any
}

func (t *TestData[T, P]) parseExpectedData(scheme *runtime.Scheme) error {
	for _, datum := range t.consistsOfData {
		expected, err := ParseExpected(scheme, datum, t.Name)
		if err != nil {
			return err
		}

		t.ConsistOf = append(t.ConsistOf, expected)
	}

	for _, datum := range t.containsData {
		expected, err := ParseExpected(scheme, datum, t.Name)
		if err != nil {
			return err
		}

		t.Contains = append(t.Contains, expected)
	}

	t.RelatedObjects = relatedobjectsmap.NewRelatedObjectsMap(scheme)
	for _, datum := range t.relatedObjectsData {
		expected, err := ParseObject(scheme, datum)
		if err != nil {
			return err
		}
		t.RelatedObjects.Insert(expected.(client.Object))
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
	t.relatedObjectsData, err = t.loadExpectedFromDir(filepath.Join(path, "related_objects"))
	if err != nil {
		return err
	}
	return nil
}

func (t *TestData[T, P]) loadExpectedFromDir(path string) ([]map[string]any, error) {
	files, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	results := make([]map[string]any, 0, len(files))

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
		expected := make(map[string]any)
		err = yaml.Unmarshal(data, &expected)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal Yaml: %w", err)
		}
		results = append(results, expected)
	}

	return results, nil
}
