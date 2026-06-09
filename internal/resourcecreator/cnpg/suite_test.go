package cnpg

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

func TestCNPGResourceCreator(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "CNPG Resource Creator Suite")
}
