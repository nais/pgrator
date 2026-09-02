package fqdnpolicy

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestFQDNPolicy(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "FQDN Policy Resource Creator Suite")
}
