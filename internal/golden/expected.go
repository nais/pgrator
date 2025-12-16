package golden

import (
	"reflect"

	"github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type Expected struct {
	Action  string         `json:"action"`
	Matcher string         `json:"matcher"`
	Object  runtime.Object `json:"object"`
}

var _ types.GomegaMatcher = &Expected{}

func (e *Expected) Match(actual any) (success bool, err error) {
	return e.makeMatcher().Match(actual)
}

func (e *Expected) FailureMessage(actual any) (message string) {
	return e.makeMatcher().FailureMessage(actual)
}

func (e *Expected) NegatedFailureMessage(actual any) (message string) {
	return e.makeMatcher().NegatedFailureMessage(actual)
}

func (e *Expected) makeMatcher() types.GomegaMatcher {
	return gomega.SatisfyAll(
		gomega.WithTransform(func(actual any) string {
			actualType := reflect.TypeOf(actual)
			actualTypeName := actualType.Name() // TODO: This returns nothing
			return actualTypeName
		}, gomega.Equal(e.Action)),
	)
}

func ParseExpected(scheme *runtime.Scheme, datum map[string]interface{}) (*Expected, error) {
	e := &Expected{}
	e.Matcher = datum["matcher"].(string)
	e.Action = datum["action"].(string)

	objectData := datum["object"].(map[string]interface{})

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
	e.Object = obj
	return e, nil
}
