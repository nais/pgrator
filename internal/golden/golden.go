package golden

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/nais/pgrator/internal/synchronizer/action"
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

type compareKey struct {
	Action    string
	Kind      string
	Name      string
	Namespace string
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
		It("should exist", func() {
			Expect(g.testCases).ToNot(BeEmpty())
		})

		for _, testCase := range g.testCases {
			Context(testCase.Name, Ordered, func() {
				var compareActions map[compareKey]action.Action

				BeforeAll(func() {
					actions, _, err := g.reconciler.Update(testCase.Object, testCase.PreparedData)
					Expect(err).NotTo(HaveOccurred())

					compareActions = makeCompareMap(actions, makeActionListKey)
				})

				if len(testCase.consistsOfData) > 0 {
					When("actions are exactly as specified", func() {
						It("all expected keys are in the list", func() {
							consistsOf := makeCompareList(testCase.ConsistOf, func(e *Expected) compareKey {
								return e.compareKey()
							})
							Expect(maps.Keys(compareActions)).To(ConsistOf(consistsOf))
						})

						It("each action should match expected object", func() {
							for _, expected := range testCase.ConsistOf {
								key := expected.compareKey()
								By(fmt.Sprintf("matching %v", key))
								Expect(compareActions[key]).To(expected)
							}
						})
					})
				} else {
					When("actions contains at least the specified actions", func() {
						It("all expected keys are in the list", func() {
							contains := makeCompareList(testCase.Contains, func(e *Expected) compareKey {
								return e.compareKey()
							})
							Expect(maps.Keys(compareActions)).To(ContainElements(contains))
						})

						It("each action should match expected object", func() {
							for _, expected := range testCase.Contains {
								key := expected.compareKey()
								By(fmt.Sprintf("matching %v", key))
								Expect(compareActions[key]).To(expected)
							}
						})
					})
				}
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

func makeCompareMap[T any](items []T, keyFunc func(T) compareKey) map[compareKey]T {
	mapping := make(map[compareKey]T, len(items))
	for _, item := range items {
		key := keyFunc(item)
		if _, exists := mapping[key]; exists {
			panic(fmt.Sprintf("duplicate compareKey in makeCompareMap: %+v", key))
		}
		mapping[key] = item
	}
	return mapping
}

func makeCompareList[T any](items []T, keyFunc func(T) compareKey) []compareKey {
	list := make([]compareKey, 0, len(items))
	for _, item := range items {
		list = append(list, keyFunc(item))
	}
	return list
}

func makeActionListKey(a action.Action) compareKey {
	obj := a.GetObject()
	return compareKey{
		Action:    getTypeName(a),
		Kind:      obj.GetObjectKind().GroupVersionKind().Kind,
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}
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
