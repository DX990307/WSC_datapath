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
