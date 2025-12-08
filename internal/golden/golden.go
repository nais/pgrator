package golden

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	. "github.com/onsi/gomega"    //nolint:staticcheck
	"github.com/onsi/gomega/types"
	"sigs.k8s.io/yaml"

	"github.com/nais/pgrator/internal/synchronizer/object"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
)

type Expected struct {
	Action  string      `json:"action"`
	Matcher string      `json:"matcher"`
	Object  interface{} `json:"object"`
}

var _ types.GomegaMatcher = &Expected{}

func (e *Expected) Match(actual any) (success bool, err error) {
	// TODO implement me
	panic("implement me")
}

func (e *Expected) FailureMessage(actual any) (message string) {
	// TODO implement me
	panic("implement me")
}

func (e *Expected) NegatedFailureMessage(actual any) (message string) {
	// TODO implement me
	panic("implement me")
}

type TestData[T object.NaisObject, P any] struct {
	// Name of this test case (set from filename)
	Name string

	// Prepared data to pass to the reconciler under test
	PreparedData P

	// The actual object to reconcile
	Object T

	// Use this to match exactly the expected actions.
	// All actions must match one element in this list if present.
	ConsistOf []*Expected

	// Use this to match that some expected actions are present.
	// Additional actions might be present, and will be ignored for this test case.
	Contains []*Expected
}

type Golden[T object.NaisObject, P any] struct {
	reconciler reconciler.Reconciler[T, P]
	testCases  []*TestData[T, P]
}

func NewGolden[T interface {
	object.NaisObject
	*O
}, P any, O any](t *testing.T, r reconciler.Reconciler[T, P], testDataDir string) *Golden[T, P] {
	gomega := NewGomegaWithT(t)

	files, err := os.ReadDir(testDataDir)
	gomega.Expect(err).NotTo(HaveOccurred())

	testCases := make([]*TestData[T, P], 0, len(files))
	for _, file := range files {
		if !file.IsDir() {
			continue
		}
		name := file.Name()
		path := filepath.Join(testDataDir, name)
		testData := &TestData[T, P]{
			Name: name,
		}

		objectPath := filepath.Join(path, "object.yaml")
		obj := new(O)
		err = unmarshalObject(objectPath, obj)
		gomega.Expect(err).NotTo(HaveOccurred())
		testData.Object = obj

		preparedDataPath := filepath.Join(path, "prepared_data.yaml")
		preparedData := new(P)
		err = unmarshalObject(preparedDataPath, preparedData)
		if err == nil {
			testData.PreparedData = *preparedData
		} else if !os.IsNotExist(err) {
			gomega.Expect(err).NotTo(HaveOccurred())
		}

		testData.Contains, err = loadExpectedFromDir(gomega, filepath.Join(path, "contains"))
		gomega.Expect(err).NotTo(HaveOccurred())

		testData.ConsistOf, err = loadExpectedFromDir(gomega, filepath.Join(path, "consists_of"))
		gomega.Expect(err).NotTo(HaveOccurred())

		testCases = append(testCases, testData)
	}

	return &Golden[T, P]{
		reconciler: r,
		testCases:  testCases,
	}
}

func loadExpectedFromDir(gomega *WithT, path string) ([]*Expected, error) {
	files, err := os.ReadDir(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	gomega.Expect(err).NotTo(HaveOccurred())

	results := make([]*Expected, 0, len(files))

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
		gomega.Expect(err).NotTo(HaveOccurred())
		expected := &Expected{}
		err = yaml.Unmarshal(data, expected)
		gomega.Expect(err).NotTo(HaveOccurred())
		results = append(results, expected)
	}

	return results, nil
}

func unmarshalObject(path string, target interface{}) error {
	data, err := readFile(path)
	if err != nil {
		return err
	}

	err = yaml.Unmarshal(data, target)
	if err != nil {
		return err
	}

	return nil
}

func (g *Golden[T, P]) DefineTests() {
	Describe(fmt.Sprintf("Golden tests for %s", g.reconciler.Name()), func() {
		It("should have tests", func() {
			Expect(g.testCases).ToNot(BeEmpty())
		})

		for _, testCase := range g.testCases {
			It(testCase.Name, func() {
				actions, _, err := g.reconciler.Update(testCase.Object, testCase.PreparedData)
				Expect(err).NotTo(HaveOccurred())
				if len(testCase.ConsistOf) > 0 {
					Expect(actions).To(ConsistOf(testCase.ConsistOf))
				}
				Expect(actions).To(ContainElements(testCase.Contains))
			})
		}
	})
}

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}
