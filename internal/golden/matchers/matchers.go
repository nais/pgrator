package matchers

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
)

type MatchType string

const (
	MatchEqual  MatchType = "Equal"
	MatchSubset MatchType = "Subset"

	RegexpMarker = "regexp:"
)

var MatchTypes = []MatchType{MatchEqual, MatchSubset}

// acceptMissingFromExpected assumes that the "right-hand" side is the expected object.
// If the value on the right is the zero value, while both sides are valid and the left has a value, we ignore the field.
// This might give rise to weird non-failures when the expected value is a zero value,
// but since we can't tell the difference in Go, it's the best we can do.
func acceptMissingFromExpected(path cmp.Path) bool {
	step := path.Last()
	actual, expected := step.Values()
	var ok bool
	if _, ok = step.(cmp.StructField); ok {
		if actual.IsValid() && expected.IsValid() {
			if !actual.IsZero() && expected.IsZero() {
				ginkgo.GinkgoWriter.Printf("Ignoring %v, because expected is zero\n", path.GoString())
				return true
			}
		}
	} else if _, ok = step.(cmp.MapIndex); ok {
		if actual.IsValid() && !expected.IsValid() {
			ginkgo.GinkgoWriter.Printf("Ignoring %v, because expected is invalid\n", path.GoString())
			return true
		}
	}
	return false
}

func stringRegexMatcher(a, b string) bool {
	var pattern *regexp.Regexp
	var toMatch string
	if strings.HasPrefix(a, RegexpMarker) {
		pattern = regexp.MustCompile(a[len(RegexpMarker):])
		toMatch = b
	} else if strings.HasPrefix(b, RegexpMarker) {
		pattern = regexp.MustCompile(b[len(RegexpMarker):])
		toMatch = a
	} else {
		pattern = regexp.MustCompile(a)
		toMatch = b
	}
	return pattern.MatchString(toMatch)
}

func MakeMatcher(matchType MatchType) (func(any) types.GomegaMatcher, error) {
	if !slices.Contains(MatchTypes, matchType) {
		return nil, fmt.Errorf("invalid MatchType: %v", matchType)
	}
	return func(expected any) types.GomegaMatcher {
		opts := []cmp.Option{
			cmpopts.EquateEmpty(),
			cmpopts.EquateApproxTime(3 * time.Second),
			cmp.Comparer(stringRegexMatcher),
		}
		if matchType == MatchSubset {
			opts = append(opts, cmp.FilterPath(acceptMissingFromExpected, cmp.Ignore()))
		}
		return gomega.BeComparableTo(expected, opts...)
	}, nil
}
