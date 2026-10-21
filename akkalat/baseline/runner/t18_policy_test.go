package runner

import (
	"testing"

	"github.com/sarchlab/mgpusim/v3/timing/rdma"
)

func TestResolveT18CreditPolicy(t *testing.T) {
	ok := []struct {
		name               string
		dist, distSet, nfs bool
		want               rdma.T18CreditPolicy
	}{
		{"", false, false, false, rdma.T18CreditShared}, // defaults: unchanged
		{"", true, true, false, rdma.T18CreditRTT},      // old -t18-dist-credits=true
		{"", false, true, false, rdma.T18CreditShared},
		{"shared", false, false, false, rdma.T18CreditShared},
		{"rtt", false, false, false, rdma.T18CreditRTT},
		{"near", false, false, true, rdma.T18CreditNear},
		{"rtt", true, true, false, rdma.T18CreditRTT},
		{"near", true, true, false, rdma.T18CreditNear},
		{"shared", false, true, false, rdma.T18CreditShared},
	}
	for _, c := range ok {
		got, err := resolveT18CreditPolicy(c.name, c.dist, c.distSet, c.nfs)
		if err != nil || got != c.want {
			t.Errorf("%+v: got %v err %v", c, got, err)
		}
	}
	bad := []struct {
		name               string
		dist, distSet, nfs bool
	}{
		{"shared", true, true, false}, // contradiction
		{"rtt", false, true, false},   // contradiction
		{"near", false, true, false},  // contradiction
		{"rtt", false, false, true},   // near flags without near
		{"", true, true, true},        // near flags with rtt via dist-credits
		{"dist", false, false, false}, // unknown policy
	}
	for _, c := range bad {
		if _, err := resolveT18CreditPolicy(c.name, c.dist, c.distSet, c.nfs); err == nil {
			t.Errorf("%+v: accepted", c)
		}
	}
}
