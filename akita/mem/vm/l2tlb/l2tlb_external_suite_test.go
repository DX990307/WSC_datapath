//go:build l2tlb_legacy_tests

// Ginkgo v1 suite with no specs; it clashes with the v2 suite in
// tlb_suite_test.go (both register the ginkgo.* flags), so it is kept behind
// the same build tag as tlb_test.go.

package l2tlb_test

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func TestL2TLBExternal(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "L2TLB External Suite")
}
