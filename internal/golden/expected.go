package golden

import (
	"fmt"
	"reflect"

	"github.com/nais/pgrator/internal/golden/matchers"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Expected struct {
	Action  string        `json:"action"`
	Matcher string        `json:"matcher"`
	Object  client.Object `json:"object"`

	matcher      types.GomegaMatcher
	testCaseName string
}

var _ types.GomegaMatcher = &Expected{}

func (e *Expected) Match(actual any) (success bool, err error) {
	return e.matcher.Match(actual)
}

func (e *Expected) FailureMessage(actual any) (message string) {
	return e.matcher.FailureMessage(actual)
}

func (e *Expected) NegatedFailureMessage(actual any) (message string) {
	return e.matcher.NegatedFailureMessage(actual)
}

func (e *Expected) compareKey() compareKey {
	obj := e.Object
	return compareKey{
		Action:    e.Action,
		Kind:      obj.GetObjectKind().GroupVersionKind().Kind,
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}
}

func (e *Expected) makeMatcher() {
	objectMatcher, err := matchers.MakeMatcher(matchers.MatchType(e.Matcher))
	if err != nil {
		panic(fmt.Sprintf("Programmer error in test %s: %v", e.testCaseName, err))
	}

	e.matcher = gomega.SatisfyAll(
		gomega.WithTransform(getTypeName, gomega.Equal(e.Action)),
		gomega.WithTransform(getActionObject, objectMatcher(e.Object)),
	)
}

func ParseExpected(scheme *runtime.Scheme, datum map[string]any, testCaseName string) (*Expected, error) {
	objectData := datum["object"].(map[string]any)

	apiVersion := objectData["apiVersion"]
	groupVersion, err := schema.ParseGroupVersion(apiVersion.(string))
	if err != nil {
		return nil, err
	}
	kind := objectData["kind"]
	gvk := groupVersion.WithKind(kind.(string))

	obj, err := scheme.New(gvk)
	if err != nil {
		return nil, err
	}

	err = runtime.DefaultUnstructuredConverter.FromUnstructured(objectData, obj)
	if err != nil {
		return nil, err
	}

	e := &Expected{
		Matcher:      datum["matcher"].(string),
		Action:       datum["action"].(string),
		Object:       obj.(client.Object),
		testCaseName: testCaseName,
	}
	e.makeMatcher()

	return e, nil
}

func getTypeName(actual any) string {
	t := reflect.TypeOf(actual)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}

func getActionObject(actual any) client.Object {
	a := actual.(action.Action)
	return a.GetObject()
}
