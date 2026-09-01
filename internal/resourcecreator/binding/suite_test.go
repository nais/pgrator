package binding

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBinding(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Binding Resource Creator Suite")
}
