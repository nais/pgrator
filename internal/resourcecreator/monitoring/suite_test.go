package monitoring

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

func TestMonitoringResourceCreator(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Monitoring Resource Creator Suite")
}
