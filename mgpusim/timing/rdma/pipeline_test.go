package rdma

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

func newPipelineTestComp(
	width, latency, maxOutstanding int,
	config RemoteDataPathConfig,
) (*Comp, *remoteTestPort, *remoteTestPort) {
	remoteGPU := &remoteTestPort{name: "Remote.RDMA"}
	c := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithRemoteModules(&mem.SingleLowModuleFinder{LowModule: remoteGPU}).
		WithPipelineWidth(width).
		WithPipelineLatency(latency).
		WithMaxOutstanding(maxOutstanding).
		WithRemoteDataPath(config).
		Build("Requester.RDMA")
	toL1 := &remoteTestPort{name: "Requester.ToL1"}
	toOutside := &remoteTestPort{name: "Requester.ToOutside"}
	c.ToL1 = toL1
	c.ToL2 = &remoteTestPort{name: "Requester.ToL2"}
	c.ToOutside = toOutside
	return c, toL1, toOutside
}

func TestRDMAPipelineAppliesFixedLatency(t *testing.T) {
	c, toL1, toOutside := newPipelineTestComp(8, 10, 64,
		RemoteDataPathConfig{})
	read := remoteTestRead(&remoteTestPort{name: "L1"}, 0x1000)
	read.RecvTime = sim.VTimeInSec(1e-9)
	toL1.inbox = append(toL1.inbox, read)

	if !c.processFromL1(sim.VTimeInSec(5e-9)) {
		t.Fatal("RDMA did not stay active while the request was in its pipeline")
	}
	if len(toOutside.sent) != 0 || len(toL1.inbox) != 1 {
		t.Fatal("RDMA request escaped before the fixed pipeline latency")
	}

	c.processFromL1(sim.VTimeInSec(11e-9))
	if len(toOutside.sent) != 1 || len(toL1.inbox) != 0 {
		t.Fatal("RDMA request did not leave after the fixed pipeline latency")
	}
}

func TestRDMAPipelineLimitsWidth(t *testing.T) {
	c, toL1, toOutside := newPipelineTestComp(1, 0, 64,
		RemoteDataPathConfig{})
	l1 := &remoteTestPort{name: "L1"}
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(l1, 0x1000),
		remoteTestRead(l1, 0x2000),
	)

	c.processFromL1(1)
	if len(toOutside.sent) != 1 || len(toL1.inbox) != 1 {
		t.Fatal("RDMA processed more than its configured width")
	}
}

func TestRDMAPipelineBackpressuresAtOutstandingCapacity(t *testing.T) {
	c, toL1, toOutside := newPipelineTestComp(8, 0, 1,
		RemoteDataPathConfig{})
	l1 := &remoteTestPort{name: "L1"}
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(l1, 0x1000),
		remoteTestRead(l1, 0x2000),
	)

	c.processFromL1(1)
	stats := c.GetPipelineStats()
	if len(toOutside.sent) != 1 || len(toL1.inbox) != 1 {
		t.Fatal("RDMA did not backpressure a new operation at capacity")
	}
	if stats.PeakOutstanding != 1 || stats.OutstandingFullStalls == 0 {
		t.Fatalf("unexpected outstanding stats: %+v", stats)
	}
}

func TestRDMAPipelineRequesterCapacityDoesNotBlockOwner(t *testing.T) {
	c, toL1, toOutside := newPipelineTestComp(8, 0, 1,
		RemoteDataPathConfig{})
	localL2 := &remoteTestPort{name: "Owner.L2"}
	c.localModules = &mem.SingleLowModuleFinder{LowModule: localL2}
	remoteL1 := &remoteTestPort{name: "Remote.L1"}

	toL1.inbox = append(toL1.inbox, remoteTestRead(remoteL1, 0x1000))
	if !c.processFromL1(1) || len(c.transactionsFromInside) != 1 {
		t.Fatal("requester direction did not reach its capacity")
	}

	incoming := remoteTestRead(remoteL1, 0x2000)
	toOutside.inbox = append(toOutside.inbox, incoming)
	if !c.processFromOutside(2) {
		t.Fatal("full requester direction blocked an incoming owner request")
	}
	if len(c.transactionsFromOutside) != 1 || len(c.ToL2.(*remoteTestPort).sent) != 1 {
		t.Fatal("owner direction did not independently accept the request")
	}

	stats := c.GetPipelineStats()
	if stats.RequesterPeakOutstanding != 1 ||
		stats.OwnerPeakOutstanding != 1 {
		t.Fatalf("unexpected directional peaks: %+v", stats)
	}
}

func TestRDMADedupCanMergeWhenOutstandingTableIsFull(t *testing.T) {
	c, toL1, _ := newPipelineTestComp(8, 0, 1,
		RemoteDataPathConfig{
			Enabled:            true,
			DisableBatching:    true,
			DisableRequesterL2: true,
			MaxBatchLines:      8,
			MaxBatches:         8,
		})
	l1 := &remoteTestPort{name: "L1"}
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(l1, 0x1000),
		remoteTestRead(l1, 0x1000),
	)

	c.processFromL1(1)
	if len(toL1.inbox) != 0 || len(c.remoteLines) != 1 {
		t.Fatal("full outstanding table rejected a deduplicatable request")
	}
	for _, entry := range c.remoteLines {
		if len(entry.waiters) != 2 {
			t.Fatalf("dedup waiters = %d, want 2", len(entry.waiters))
		}
	}
}
