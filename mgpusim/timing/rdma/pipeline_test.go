package rdma

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

func TestRemoteMetadataLookupOverlapsFixedRDMAPipeline(t *testing.T) {
	c, toL1, _ := newPipelineTestComp(8, 10, 64,
		RemoteDataPathConfig{
			Enabled:            true,
			DisableRequesterL2: true,
			MaxBatchLines:      1,
			MaxBatches:         8,
		})
	filter := writeback.NewTypedCuckooFilter(writeback.TypedFilterConfig{
		Capacity: 64, Mode: writeback.TypedFilterCuckoo,
		LookupLatencyCycles: 1, LookupWidth: 8,
		UpdateLatencyCycles: 1, UpdateWidth: 8,
		Freq: 1 * sim.GHz,
	})
	c.SetRequestFilters([]*writeback.TypedCuckooFilter{filter}, 128)
	read := remoteTestRead(&remoteTestPort{name: "L1"}, 0x1000)
	read.RecvTime = 0
	toL1.inbox = append(toL1.inbox, read)

	if !c.processFromL1(1e-9) || len(c.remoteFilterLookups) != 1 {
		t.Fatal("metadata lookup was not launched inside the fixed pipeline")
	}
	if len(c.remoteLines) != 0 || len(toL1.inbox) != 1 {
		t.Fatal("remote request was consumed before metadata completed")
	}
	if !c.processFromL1(2e-9) || len(toL1.inbox) != 1 {
		t.Fatal("metadata completion bypassed the fixed RDMA pipeline")
	}
	if !c.processFromL1(10e-9) || len(toL1.inbox) != 0 ||
		len(c.remoteLines) != 1 || len(c.remoteFilterLookups) != 0 {
		t.Fatal("primed metadata was not consumed after pipeline completion")
	}
	if !c.processRemotePendingBatches(10e-9) {
		t.Fatal("request did not enter the remote batch")
	}
	if !c.processRemoteBatches(10e-9, false) ||
		len(c.ToOutside.(*remoteTestPort).sent) != 1 {
		t.Fatal("batch did not leave after pipeline completion")
	}
	if filter.Stats().ByType[writeback.FilterPending].Queries != 1 {
		t.Fatal("overlapped PENDING lookup was not completed exactly once")
	}
}

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
	attachTestRequestFilter(c)
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

func TestRDMABitmapUsesOneOwnerOutstandingDescriptor(t *testing.T) {
	c, _, toOutside := newPipelineTestComp(8, 0, 1,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 8, MaxBatches: 8})
	localL2 := &remoteTestPort{name: "Owner.L2"}
	c.localModules = &mem.SingleLowModuleFinder{LowModule: localL2}
	requester := &remoteTestPort{name: "Requester.RDMA"}
	first := &BitmapReadReq{
		MsgMeta: sim.MsgMeta{
			ID:  "bitmap-1",
			Src: requester,
			Dst: toOutside,
		},
		PagePAddr:  0x6000,
		LineBitmap: 0xff,
	}
	toOutside.inbox = append(toOutside.inbox, first)

	if !c.processFromOutside(1) {
		t.Fatal("owner rejected a bounded bitmap at empty capacity")
	}
	stats := c.GetPipelineStats()
	if len(c.remoteOwnerBatches) != 1 || len(c.remoteOwnerSubReqs) != 8 {
		t.Fatalf("unexpected bitmap state: batches=%d children=%d",
			len(c.remoteOwnerBatches), len(c.remoteOwnerSubReqs))
	}
	if stats.OwnerPeakOutstanding != 1 {
		t.Fatalf("bitmap charged %d outstanding slots, want 1",
			stats.OwnerPeakOutstanding)
	}
	if c.RemoteDataPathStats.OwnerPeakChildLines != 8 {
		t.Fatalf("owner child-line peak = %d, want 8",
			c.RemoteDataPathStats.OwnerPeakChildLines)
	}
}

func TestRDMABitmapUsesOneRequesterOutstandingDescriptor(t *testing.T) {
	c, toL1, toOutside := newPipelineTestComp(8, 0, 1,
		RemoteDataPathConfig{
			Enabled:            true,
			DisableRequesterL2: true,
			MaxBatchLines:      8,
			MaxBatches:         8,
		})
	l1 := &remoteTestPort{name: "L1"}
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(l1, 0x6000),
		remoteTestRead(l1, 0x6040),
	)
	c.processFromL1(1)
	c.processRemotePendingBatches(2)
	c.processRemotePendingBatches(2)
	c.processRemoteBatches(2, false)

	stats := c.GetPipelineStats()
	if len(toOutside.sent) != 1 || len(c.remoteBitmapInflight) != 1 {
		t.Fatal("two coalesced lines did not issue as one bitmap transaction")
	}
	if stats.RequesterPeakOutstanding != 1 {
		t.Fatalf("bitmap charged %d requester slots, want 1",
			stats.RequesterPeakOutstanding)
	}
	if c.RemoteDataPathStats.PeakLineEntries != 2 ||
		c.RemoteDataPathStats.LineEntryCapacity < 2 {
		t.Fatalf("line-state accounting is invalid: %+v",
			c.RemoteDataPathStats)
	}

	// A different page may enter the bounded coalescing table, but it cannot
	// issue a second network transaction until the one-entry RDMA table frees.
	toL1.inbox = append(toL1.inbox, remoteTestRead(l1, 0x7000))
	c.processFromL1(3)
	c.processRemotePendingBatches(4)
	c.processRemoteBatches(4, false)
	stats = c.GetPipelineStats()
	if len(toOutside.sent) != 1 || stats.RequesterOutstandingFullStalls == 0 {
		t.Fatal("packet-granular requester outstanding limit was not enforced")
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

func TestRemoteLogicalReadCounterExcludesOutstandingRetries(t *testing.T) {
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
		remoteTestRead(l1, 0x2000),
	)

	c.processFromL1(1)
	c.processFromL1(2)
	c.processFromL1(3)
	if c.RemoteDataPathStats.ObservedRemoteReads != 2 {
		t.Fatalf("observed remote reads = %d, want two unique arrivals",
			c.RemoteDataPathStats.ObservedRemoteReads)
	}
	if c.RemoteDataPathStats.LogicalRemoteReads != 1 {
		t.Fatalf("logical reads = %d, want one accepted request",
			c.RemoteDataPathStats.LogicalRemoteReads)
	}
	if len(toL1.inbox) != 1 || c.requesterFullStalls == 0 {
		t.Fatal("non-batched request ignored outstanding backpressure")
	}
}

func TestBaselineRemoteWriteCounterExcludesOutstandingRetries(t *testing.T) {
	c, toL1, _ := newPipelineTestComp(8, 0, 1,
		RemoteDataPathConfig{})
	l1 := &remoteTestPort{name: "L1"}
	toL1.inbox = append(toL1.inbox,
		remoteTestWrite(l1, 0x3000),
		remoteTestWrite(l1, 0x4000),
	)

	c.processFromL1(1)
	c.processFromL1(2)
	c.processFromL1(3)
	if c.RemoteDataPathStats.ObservedRemoteWrites != 2 {
		t.Fatalf("observed remote writes = %d, want two unique arrivals",
			c.RemoteDataPathStats.ObservedRemoteWrites)
	}
	if c.RemoteDataPathStats.ObservedRemoteReads != 0 {
		t.Fatalf("observed remote reads = %d, want zero",
			c.RemoteDataPathStats.ObservedRemoteReads)
	}
	if len(toL1.inbox) != 1 || c.requesterFullStalls == 0 {
		t.Fatal("baseline write ignored outstanding backpressure")
	}
}
