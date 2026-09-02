package matchers

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type MatchType string

const (
	MatchEqual  MatchType = "Equal"
	MatchSubset MatchType = "Subset"

	RegexpMarker = "regexp:"
)

var MatchTypes = []MatchType{MatchEqual, MatchSubset}

// acceptMissingFromExpected assumes that the right-hand side is the expected object.
// A zero value or missing map entry on that side is ignored for subset comparisons.
func acceptMissingFromExpected(path cmp.Path) bool {
	step := path.Last()
	actual, expected := step.Values()
	switch step.(type) {
	case cmp.StructField:
		return actual.IsValid() && expected.IsValid() && !actual.IsZero() && expected.IsZero()
	case cmp.MapIndex:
		return actual.IsValid() && !expected.IsValid()
	default:
		return false
	}
}

func stringRegexEqual(a, b string) bool {
	if strings.HasPrefix(a, RegexpMarker) {
		pattern := regexp.MustCompile(strings.TrimSpace(strings.TrimPrefix(a, RegexpMarker)))
		return pattern.MatchString(b)
	}
	if strings.HasPrefix(b, RegexpMarker) {
		pattern := regexp.MustCompile(strings.TrimSpace(strings.TrimPrefix(b, RegexpMarker)))
		return pattern.MatchString(a)
	}
	return a == b
}

// Diff compares actual with expected according to matchType. An empty diff means
// the values match.
func Diff(actual, expected any, matchType MatchType) (string, error) {
	if !slices.Contains(MatchTypes, matchType) {
		return "", fmt.Errorf("invalid MatchType: %v", matchType)
	}

	opts := []cmp.Option{
		cmpopts.EquateEmpty(),
		cmpopts.EquateApproxTime(3 * time.Second),
		cmp.Comparer(stringRegexEqual),
	}
	if matchType == MatchSubset {
		opts = append(opts, cmp.FilterPath(acceptMissingFromExpected, cmp.Ignore()))
	}

	return cmp.Diff(actual, expected, opts...), nil
}
