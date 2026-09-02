package golden

import (
	"fmt"
	"reflect"

	"github.com/nais/pgrator/internal/golden/matchers"
	"github.com/nais/pgrator/internal/synchronizer/action"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Expected struct {
	Action  string        `json:"action"`
	Matcher string        `json:"matcher"`
	Object  client.Object `json:"object"`

	lastDiff string
}

func (e *Expected) Match(actual any) (bool, error) {
	a, ok := actual.(action.Action)
	if !ok {
		return false, fmt.Errorf("expected action.Action, got %T", actual)
	}
	if actualType := typeName(a); actualType != e.Action {
		e.lastDiff = fmt.Sprintf("action type = %q, want %q", actualType, e.Action)
		return false, nil
	}

	diff, err := matchers.Diff(a.GetObject(), e.Object, matchers.MatchType(e.Matcher))
	if err != nil {
		return false, err
	}
	e.lastDiff = diff
	return diff == "", nil
}

func (e *Expected) FailureMessage(any) string {
	if e.lastDiff == "" {
		return "values did not match"
	}
	return "object mismatch (-actual +expected):\n" + e.lastDiff
}

func ParseExpected(scheme *runtime.Scheme, datum map[string]any, testCaseName string) (*Expected, error) {
	objectData, ok := datum["object"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("test case %q: object must be a map", testCaseName)
	}

	obj, err := ParseObject(scheme, objectData)
	if err != nil {
		return nil, err
	}

	matcher, ok := datum["matcher"].(string)
	if !ok {
		return nil, fmt.Errorf("test case %q: matcher must be a string", testCaseName)
	}
	if _, err := matchers.Diff(nil, nil, matchers.MatchType(matcher)); err != nil {
		return nil, fmt.Errorf("test case %q: %w", testCaseName, err)
	}

	actionName, ok := datum["action"].(string)
	if !ok {
		return nil, fmt.Errorf("test case %q: action must be a string", testCaseName)
	}

	return &Expected{
		Matcher: matcher,
		Action:  actionName,
		Object:  obj.(client.Object),
	}, nil
}

func ParseObject(scheme *runtime.Scheme, objectData map[string]any) (runtime.Object, error) {
	apiVersion, ok := objectData["apiVersion"].(string)
	if !ok {
		return nil, fmt.Errorf("apiVersion must be a string")
	}
	groupVersion, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil, err
	}
	kind, ok := objectData["kind"].(string)
	if !ok {
		return nil, fmt.Errorf("kind must be a string")
	}

	obj, err := scheme.New(groupVersion.WithKind(kind))
	if err != nil {
		return nil, err
	}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(objectData, obj); err != nil {
		return nil, err
	}
	return obj, nil
}

func typeName(value any) string {
	t := reflect.TypeOf(value)
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}
