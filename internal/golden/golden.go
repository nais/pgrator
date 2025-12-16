package golden

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2" //nolint:staticcheck
	. "github.com/onsi/gomega"    //nolint:staticcheck
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	"github.com/nais/pgrator/internal/synchronizer/object"
	"github.com/nais/pgrator/internal/synchronizer/reconciler"
)

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

		err = testData.loadExpectedData(path)
		gomega.Expect(err).NotTo(HaveOccurred())

		testCases = append(testCases, testData)
	}

	return &Golden[T, P]{
		reconciler: r,
		testCases:  testCases,
	}
}

// DefineTests creates the golden file tests for this instance of Golden
// Should be called from the suite_test file before calling `RunSpecs`.
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

// ParseData parses the loaded data using the given scheme to look up registered GroupVersionKind types
// Should be called in a BeforeSuite or BeforeAll closure, after a suitable Scheme has been created.
func (g *Golden[T, P]) ParseData(scheme *runtime.Scheme) error {
	for _, testCase := range g.testCases {
		if err := testCase.parseExpectedData(scheme); err != nil {
			return err
		}
	}
	return nil
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

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}
