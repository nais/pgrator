package valkey

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

func TestValkeyResourceCreator(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Valkey Resource Creator Suite")
}
