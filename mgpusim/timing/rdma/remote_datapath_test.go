package rdma

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

type remoteTestPort struct {
	sim.HookableBase
	name    string
	inbox   []sim.Msg
	sent    []sim.Msg
	blocked bool
}

func (p *remoteTestPort) Name() string                   { return p.name }
func (p *remoteTestPort) SetConnection(sim.Connection)   {}
func (p *remoteTestPort) Component() sim.Component       { return nil }
func (p *remoteTestPort) NotifyAvailable(sim.VTimeInSec) {}
func (p *remoteTestPort) CanSend() bool                  { return !p.blocked }
func (p *remoteTestPort) Recv(msg sim.Msg) *sim.SendError {
	p.inbox = append(p.inbox, msg)
	return nil
}
func (p *remoteTestPort) Send(msg sim.Msg) *sim.SendError {
	if p.blocked {
		return sim.NewSendError()
	}
	p.sent = append(p.sent, msg)
	return nil
}
func (p *remoteTestPort) Retrieve(sim.VTimeInSec) sim.Msg {
	if len(p.inbox) == 0 {
		return nil
	}
	msg := p.inbox[0]
	p.inbox = p.inbox[1:]
	return msg
}
func (p *remoteTestPort) Peek() sim.Msg {
	if len(p.inbox) == 0 {
		return nil
	}
	return p.inbox[0]
}

func newRemoteDataPathTestComp(
	t *testing.T,
	config RemoteDataPathConfig,
) (*Comp, *remoteTestPort, *remoteTestPort, *remoteTestPort, *remoteTestPort) {
	t.Helper()
	remoteGPU := &remoteTestPort{name: "Remote.RDMA"}
	remoteFinder := &mem.SingleLowModuleFinder{LowModule: remoteGPU}
	c := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithRemoteModules(remoteFinder).
		WithRemoteDataPath(config).
		Build("Requester.RDMA")
	toL1 := &remoteTestPort{name: "Requester.ToL1"}
	toL2 := &remoteTestPort{name: "Requester.ToL2"}
	toOutside := &remoteTestPort{name: "Requester.ToOutside"}
	c.ToL1 = toL1
	c.ToL2 = toL2
	c.ToOutside = toOutside
	return c, toL1, toL2, toOutside, remoteGPU
}

func remoteTestRead(src sim.Port, address uint64) *mem.ReadReq {
	return mem.ReadReqBuilder{}.
		WithSrc(src).
		WithAddress(address).
		WithByteSize(remoteLineBytes).
		Build()
}

func TestRemoteDataPathWorkConservingBatchUsesCurrentCycleOnly(t *testing.T) {
	c, toL1, toOutside := newPipelineTestComp(8, 0, 64,
		RemoteDataPathConfig{
			Enabled:            true,
			DisableRequesterL2: true,
			MaxBatchLines:      8,
			MaxWaitNS:          999,
			MaxBatches:         8,
		})
	l1 := &remoteTestPort{name: "L1"}
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(l1, 0x1000),
		remoteTestRead(l1, 0x1040),
	)

	if !c.Tick(1) {
		t.Fatal("work-conserving RDMA made no progress")
	}
	if len(toOutside.sent) != 1 {
		t.Fatalf("network packets = %d, want one same-cycle batch",
			len(toOutside.sent))
	}
	req, ok := toOutside.sent[0].(*BitmapReadReq)
	if !ok || req.LineBitmap != 0x3 {
		t.Fatalf("same-cycle bitmap request = %#v, want lines 0 and 1", req)
	}
	stats := c.GetRemoteDataPathStats()
	if stats.MaxWaitNS != 0 || stats.WorkConservingFlushes != 1 ||
		stats.TimeoutFlushes != 0 || stats.LogicalRemoteReads != 2 {
		t.Fatalf("unexpected work-conserving stats: %+v", stats)
	}
}

func TestRemoteDataPathBatchEgressRespectsPipelineWidth(t *testing.T) {
	c, toL1, toOutside := newPipelineTestComp(2, 0, 64,
		RemoteDataPathConfig{
			Enabled:            true,
			DisableRequesterL2: true,
			MaxBatchLines:      8,
			MaxBatches:         8,
		})
	l1 := &remoteTestPort{name: "L1"}
	for _, address := range []uint64{0x1000, 0x2000, 0x3000, 0x4000} {
		toL1.inbox = append(toL1.inbox, remoteTestRead(l1, address))
	}
	c.processFromL1(1)
	c.processFromL1(2)
	for i := 0; i < 4; i++ {
		if !c.processRemotePendingBatches(3) {
			t.Fatal("request did not enter a collecting batch")
		}
	}

	c.processRemoteBatches(3, false)
	if len(toOutside.sent) != 2 || len(c.remoteBatchOrder) != 2 {
		t.Fatalf("first egress cycle sent %d packets and left %d batches; want 2 and 2",
			len(toOutside.sent), len(c.remoteBatchOrder))
	}
	c.processRemoteBatches(4, false)
	if len(toOutside.sent) != 4 || len(c.remoteBatchOrder) != 0 {
		t.Fatalf("second egress cycle sent %d packets and left %d batches; want 4 and 0",
			len(toOutside.sent), len(c.remoteBatchOrder))
	}
}

func TestRemoteDataPathMergesCollectingAndInflightReads(t *testing.T) {
	c, toL1, toL2, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:           true,
			MaxBatchLines:     8,
			MaxWaitNS:         10, // Deprecated input must not delay issue.
			MaxBatches:        8,
			ReuseTableEntries: 32,
		})
	l1A := &remoteTestPort{name: "L1.A"}
	l1B := &remoteTestPort{name: "L1.B"}
	first := remoteTestRead(l1A, 0x2000)
	second := remoteTestRead(l1B, 0x2000)
	toL1.inbox = append(toL1.inbox, first, second)

	if !c.processFromL1(1) {
		t.Fatal("requester did not accept the reads")
	}
	if got := len(c.remoteLines); got != 1 {
		t.Fatalf("remote line entries = %d, want 1", got)
	}
	for _, entry := range c.remoteLines {
		if len(entry.waiters) != 2 || !entry.admit {
			t.Fatalf("collecting entry has %d waiters, admit=%v", len(entry.waiters), entry.admit)
		}
	}
	if !c.processRemotePendingBatches(2) {
		t.Fatal("pending line was not added to a batch")
	}
	if !c.processRemoteBatches(2, false) {
		t.Fatal("batch was not flushed")
	}
	if got := len(toOutside.sent); got != 1 {
		t.Fatalf("network requests = %d, want 1", got)
	}
	leader := toOutside.sent[0].(*mem.ReadReq)

	third := remoteTestRead(l1A, 0x2000)
	toL1.inbox = append(toL1.inbox, third)
	c.processFromL1(21)
	if got := len(toOutside.sent); got != 1 {
		t.Fatalf("inflight duplicate emitted a second network request; got %d", got)
	}
	if c.RemoteDataPathStats.InflightMerges != 1 {
		t.Fatalf("inflight merges = %d, want 1", c.RemoteDataPathStats.InflightMerges)
	}

	data := make([]byte, remoteLineBytes)
	data[0] = 7
	rsp := mem.DataReadyRspBuilder{}.
		WithSrc(&remoteTestPort{name: "Remote"}).
		WithDst(toOutside).
		WithRspTo(leader.ID).
		WithData(data).
		Build()
	toOutside.inbox = append(toOutside.inbox, rsp)
	c.processFromOutside(30)
	for i := 0; i < 3; i++ {
		if !c.processRemoteReady(31 + sim.VTimeInSec(i)) {
			t.Fatal("response fanout stalled")
		}
	}
	if got := len(toL1.sent); got != 3 {
		t.Fatalf("fanout responses = %d, want 3", got)
	}
	firstRsp := toL1.sent[0].(*mem.DataReadyRsp)
	secondRsp := toL1.sent[1].(*mem.DataReadyRsp)
	firstRsp.Data[0] = 99
	if secondRsp.Data[0] != 7 {
		t.Fatal("fanout responses share a mutable data slice")
	}

	l2Top := &remoteTestPort{name: "Requester.L2[0].Top"}
	c.SetRemoteCacheModuleFinder(&mem.SingleLowModuleFinder{LowModule: l2Top})
	if !c.processRemoteReady(40) {
		t.Fatal("two-touch clean fill was not issued")
	}
	if got := len(toL2.sent); got != 1 {
		t.Fatalf("clean fills = %d, want 1", got)
	}
	fill := toL2.sent[0].(*mem.RemoteDataFill)
	if fill.Address != 0x2000 {
		t.Fatalf("fill address = %#x, want %#x", fill.Address, uint64(0x2000))
	}
	fillRsp := mem.RemoteDataFillRspBuilder{}.
		WithRspTo(fill.ID).
		WithInstalled(true).
		Build()
	toL2.inbox = append(toL2.inbox, fillRsp)
	c.processFromL2(41)
	if len(c.remoteLines) != 0 {
		t.Fatal("line entry was not retired after fill")
	}
}

func TestRemoteDataPathDedupOnlySendsImmediatelyWithoutBatchingOrL2(t *testing.T) {
	c, toL1, toL2, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:            true,
			DisableBatching:    true,
			DisableRequesterL2: true,
			MaxBatchLines:      8,
			MaxBatches:         8,
		})
	l1A := &remoteTestPort{name: "L1.A"}
	l1B := &remoteTestPort{name: "L1.B"}
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(l1A, 0x2800),
		remoteTestRead(l1B, 0x2800),
	)

	c.processFromL1(1)
	if len(c.remoteLines) != 1 || c.RemoteDataPathStats.DuplicateReads != 1 {
		t.Fatal("dedup-only mode did not merge the same-line requests")
	}
	if !c.processRemotePendingBatches(2) {
		t.Fatal("dedup-only request was not sent")
	}
	if len(toOutside.sent) != 1 {
		t.Fatalf("network requests = %d, want 1", len(toOutside.sent))
	}
	if len(toL2.sent) != 0 {
		t.Fatal("dedup-only mode unexpectedly probed requester L2")
	}
	stats := c.GetRemoteDataPathStats()
	if stats.BatchingEnabled || stats.RequesterL2Enabled {
		t.Fatal("dedup-only stats report a disabled mechanism as enabled")
	}
	if stats.SingleReadPackets != 1 || stats.BitmapPackets != 0 ||
		stats.BatchQueueWaitSamples != 0 {
		t.Fatalf("dedup-only packet stats = single %d, bitmap %d, batch samples %d",
			stats.SingleReadPackets, stats.BitmapPackets,
			stats.BatchQueueWaitSamples)
	}
}

func TestRemoteDataPathRequesterL2OnlyDoesNotDedupOrBatch(t *testing.T) {
	c, toL1, toL2, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:            true,
			DisableDedup:       true,
			DisableBatching:    true,
			DisableRequesterL2: false,
			MaxBatchLines:      8,
			MaxBatches:         8,
			ReuseTableEntries:  32,
		})
	l2Top := &remoteTestPort{name: "Requester.L2.Top"}
	c.SetRemoteCacheModuleFinder(
		&mem.SingleLowModuleFinder{LowModule: l2Top})
	l1A := &remoteTestPort{name: "L1.A"}
	l1B := &remoteTestPort{name: "L1.B"}
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(l1A, 0x2c00),
		remoteTestRead(l1B, 0x2c00),
	)

	c.processFromL1(1)
	if len(c.remoteLines) != 2 || c.RemoteDataPathStats.DuplicateReads != 0 {
		t.Fatalf("L2-only mode merged requests: entries=%d duplicates=%d",
			len(c.remoteLines), c.RemoteDataPathStats.DuplicateReads)
	}
	if len(toL2.sent) != 2 {
		t.Fatalf("requester L2 probes = %d, want 2", len(toL2.sent))
	}
	for _, message := range toL2.sent {
		probe := message.(*mem.ReadReq)
		toL2.inbox = append(toL2.inbox, mem.CacheLookupRspBuilder{}.
			WithRspTo(probe.ID).
			WithHit(false).
			Build())
	}
	c.processFromL2(2)
	c.processRemotePendingBatches(3)
	c.processRemotePendingBatches(4)
	if len(toOutside.sent) != 2 {
		t.Fatalf("direct remote requests = %d, want 2", len(toOutside.sent))
	}
	stats := c.GetRemoteDataPathStats()
	if stats.DedupEnabled || stats.BatchingEnabled ||
		!stats.RequesterL2Enabled {
		t.Fatalf("unexpected L2-only feature state: %+v", stats)
	}
	if stats.SingleReadPackets != 2 || stats.BitmapPackets != 0 {
		t.Fatalf("L2-only packets = single %d, bitmap %d",
			stats.SingleReadPackets, stats.BitmapPackets)
	}
}

func TestRemoteDataPathSendFailureKeepsBatch(t *testing.T) {
	c, toL1, _, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 8, MaxBatches: 8})
	toL1.inbox = append(toL1.inbox, remoteTestRead(
		&remoteTestPort{name: "L1"}, 0x3000))
	c.processFromL1(1)
	c.processRemotePendingBatches(2)
	toOutside.blocked = true
	if c.flushRemoteBatch(3, c.remoteBatches[c.remoteBatchOrder[0]], flushReasonIssue) {
		t.Fatal("blocked send unexpectedly succeeded")
	}
	if len(c.remoteBatchOrder) != 1 || len(c.remoteSingleInflight) != 0 {
		t.Fatal("failed send lost or committed the collecting batch")
	}
	toOutside.blocked = false
	if !c.flushRemoteBatch(4, c.remoteBatches[c.remoteBatchOrder[0]], flushReasonIssue) {
		t.Fatal("retry did not flush the preserved batch")
	}
	if len(c.remoteBatchOrder) != 0 || len(c.remoteSingleInflight) != 1 {
		t.Fatal("successful retry did not commit exactly once")
	}
	if c.RemoteDataPathStats.BatchSizeHistogram[1] != 1 {
		t.Fatalf("one-line histogram count = %d, want 1",
			c.RemoteDataPathStats.BatchSizeHistogram[1])
	}
	if c.RemoteDataPathStats.NetworkRequestBytes != 12 {
		t.Fatalf("request bytes = %d, want 12",
			c.RemoteDataPathStats.NetworkRequestBytes)
	}
}

func TestRemoteDataPathPrefetchFillsOnlyThePrefetchedMate(t *testing.T) {
	c, toL1, toL2, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:       true,
			AUPrefetch:    true,
			MaxBatchLines: 8,
			MaxBatches:    8,
		})
	l2Top := &remoteTestPort{name: "Requester.L2.Top"}
	c.SetRemoteCacheModuleFinder(nil)
	toL1.inbox = append(toL1.inbox, remoteTestRead(
		&remoteTestPort{name: "L1"}, 0x4000))
	c.processFromL1(1)
	c.processRemotePendingBatches(2)
	if !c.processRemoteBatches(3, true) {
		t.Fatal("prefetch batch did not flush")
	}
	if len(toOutside.sent) != 1 {
		t.Fatalf("bitmap packets = %d, want 1", len(toOutside.sent))
	}
	request := toOutside.sent[0].(*BitmapReadReq)
	if request.LineBitmap != 0x3 {
		t.Fatalf("line bitmap = %#x, want 0x3", request.LineBitmap)
	}
	response := &BitmapReadRsp{
		RespondTo: request.ID,
		LineData: map[uint64][]byte{
			0: make([]byte, remoteLineBytes),
			1: make([]byte, remoteLineBytes),
		},
	}
	toOutside.inbox = append(toOutside.inbox, response)
	c.processFromOutside(4)
	c.SetRemoteCacheModuleFinder(&mem.SingleLowModuleFinder{LowModule: l2Top})
	for c.processRemoteReady(5) {
	}
	if len(toL1.sent) != 1 {
		t.Fatalf("demand responses = %d, want 1", len(toL1.sent))
	}
	if len(toL2.sent) != 1 {
		t.Fatalf("prefetch fills = %d, want 1", len(toL2.sent))
	}
	fill := toL2.sent[0].(*mem.RemoteDataFill)
	if fill.Address != 0x4040 {
		t.Fatalf("prefetch fill address = %#x, want 0x4040", fill.Address)
	}
	if !fill.Prefetch {
		t.Fatal("AU-prefetched line lost its fill origin")
	}
	if c.RemoteDataPathStats.WireLines != 2 ||
		c.RemoteDataPathStats.DemandWireLines != 1 ||
		c.RemoteDataPathStats.PrefetchWireLines != 1 {
		t.Fatalf("wire line split = (%d, %d, %d), want (2, 1, 1)",
			c.RemoteDataPathStats.WireLines,
			c.RemoteDataPathStats.DemandWireLines,
			c.RemoteDataPathStats.PrefetchWireLines)
	}
	fillRsp := mem.RemoteDataFillRspBuilder{}.
		WithRspTo(fill.ID).
		WithInstalled(true).
		Build()
	toL2.inbox = append(toL2.inbox, fillRsp)
	c.processFromL2(6)
}

func TestRemoteDataPathConvertsUnsentPrefetchToDemand(t *testing.T) {
	c, toL1, _, _, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:       true,
			AUPrefetch:    true,
			MaxBatchLines: 8,
			MaxBatches:    8,
		})
	l1 := &remoteTestPort{name: "L1"}
	toL1.inbox = append(toL1.inbox, remoteTestRead(l1, 0x9000))
	c.processFromL1(1)
	c.processRemotePendingBatches(2)

	toL1.inbox = append(toL1.inbox, remoteTestRead(l1, 0x9040))
	c.processFromL1(3)
	if !c.processRemoteBatches(4, true) {
		t.Fatal("converted demand batch did not flush")
	}
	stats := c.GetRemoteDataPathStats()
	if stats.AUPrefetchConvertedDemand != 1 ||
		stats.PrefetchWireLines != 0 || stats.DemandWireLines != 2 {
		t.Fatalf("converted prefetch stats = converted %d, prefetch wire %d, demand wire %d",
			stats.AUPrefetchConvertedDemand,
			stats.PrefetchWireLines, stats.DemandWireLines)
	}
}

func TestRemoteDataPathWriteStartsANewReadEpoch(t *testing.T) {
	c, toL1, _, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 8, MaxBatches: 8})
	l1 := &remoteTestPort{name: "L1"}
	readBefore := remoteTestRead(l1, 0x5000)
	write := mem.WriteReqBuilder{}.
		WithSrc(l1).
		WithAddress(0x5000).
		WithData(make([]byte, remoteLineBytes)).
		Build()
	toL1.inbox = append(toL1.inbox, readBefore, write)

	c.processFromL1(1)
	if len(toL1.inbox) != 1 {
		t.Fatal("write overtook a read that was still waiting for batching")
	}
	c.processRemotePendingBatches(2)
	c.processRemoteBatches(3, true)
	if len(toOutside.sent) != 1 {
		t.Fatal("pre-write read was not sent first")
	}
	c.processFromL1(4)
	if len(toOutside.sent) != 2 {
		t.Fatal("write was not sent after the older read")
	}

	readAfter := remoteTestRead(l1, 0x5000)
	toL1.inbox = append(toL1.inbox, readAfter)
	c.processFromL1(5)
	if len(c.remoteLines) != 2 {
		t.Fatal("post-write read merged into the pre-write inflight read")
	}
	identity := c.remoteIdentity(readAfter, toOutside.sent[0].Meta().Dst)
	if c.remoteEpochs[identity] != 1 {
		t.Fatalf("read epoch = %d, want 1", c.remoteEpochs[identity])
	}
}

func TestBitmapOwnerDoesNotLetYoungerWritePassQueuedReads(t *testing.T) {
	c, _, toL2, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 8, MaxBatches: 8})
	l2Top := &remoteTestPort{name: "Owner.L2.Top"}
	c.localModules = &mem.SingleLowModuleFinder{LowModule: l2Top}
	requester := &remoteTestPort{name: "Requester.RDMA"}
	bitmap := &BitmapReadReq{
		MsgMeta:    sim.MsgMeta{Src: requester, Dst: toOutside},
		PagePAddr:  0x6000,
		LineBitmap: 0x3,
	}
	write := mem.WriteReqBuilder{}.
		WithSrc(requester).
		WithDst(toOutside).
		WithAddress(0x6000).
		WithData(make([]byte, remoteLineBytes)).
		Build()
	toOutside.inbox = append(toOutside.inbox, bitmap, write)

	c.processFromOutside(1)
	if len(toOutside.inbox) != 1 || len(toL2.sent) != 0 {
		t.Fatal("owner accepted the younger write before unpacking bitmap reads")
	}
	c.processRemoteOwnerPendingReqs(2)
	c.processRemoteOwnerPendingReqs(3)
	c.processFromOutside(4)
	if len(toL2.sent) != 3 {
		t.Fatalf("owner L2 requests = %d, want 3", len(toL2.sent))
	}
	if _, ok := toL2.sent[0].(*mem.ReadReq); !ok {
		t.Fatal("first owner request is not a bitmap child read")
	}
	if _, ok := toL2.sent[1].(*mem.ReadReq); !ok {
		t.Fatal("second owner request is not a bitmap child read")
	}
	if _, ok := toL2.sent[2].(*mem.WriteReq); !ok {
		t.Fatal("younger write was not issued after both bitmap child reads")
	}
}

func TestRemoteDataPathAppliesBackpressureAtOutstandingLimit(t *testing.T) {
	c, toL1, _, _, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 1, MaxBatches: 1})
	l1 := &remoteTestPort{name: "L1"}
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(l1, 0x7000),
		remoteTestRead(l1, 0x7040),
		remoteTestRead(l1, 0x7080),
	)
	c.processFromL1(1)
	if c.remoteOutstandingReads != 2 {
		t.Fatalf("accepted reads = %d, want bounded capacity 2", c.remoteOutstandingReads)
	}
	if len(toL1.inbox) != 1 {
		t.Fatalf("upstream requests left = %d, want 1", len(toL1.inbox))
	}
}

func TestBitmapOwnerRejectsPacketBeyondOutstandingLimit(t *testing.T) {
	c, _, _, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 1, MaxBatches: 1})
	for i := 0; i < c.remoteOwnerOutstandingCapacity(); i++ {
		c.remoteOwnerSubReqs[string(rune(i+1))] = &remoteOwnerSubReq{}
	}
	request := &BitmapReadReq{
		MsgMeta:    sim.MsgMeta{Src: &remoteTestPort{name: "Requester"}},
		PagePAddr:  0x8000,
		LineBitmap: 0x7,
	}
	toOutside.inbox = append(toOutside.inbox, request)
	if c.processFromOutside(1) {
		t.Fatal("oversized owner packet unexpectedly made progress")
	}
	if len(toOutside.inbox) != 1 ||
		len(c.remoteOwnerSubReqs) != c.remoteOwnerOutstandingCapacity() {
		t.Fatal("owner consumed a packet that exceeded its bounded capacity")
	}
}

func TestRemoteDataPathDrainIncludesBatchAndFanoutState(t *testing.T) {
	c, toL1, _, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 8, MaxBatches: 8})
	toL1.inbox = append(toL1.inbox, remoteTestRead(
		&remoteTestPort{name: "L1"}, 0x9000))
	c.processFromL1(1)
	if c.fullyDrained() {
		t.Fatal("pending remote read was omitted from drain state")
	}
	c.processRemotePendingBatches(2)
	c.processRemoteBatches(3, true)
	leader := toOutside.sent[0].(*mem.ReadReq)
	response := mem.DataReadyRspBuilder{}.
		WithRspTo(leader.ID).
		WithData(make([]byte, remoteLineBytes)).
		Build()
	toOutside.inbox = append(toOutside.inbox, response)
	c.processFromOutside(4)
	if c.fullyDrained() {
		t.Fatal("pending response fanout was omitted from drain state")
	}
	c.processRemoteReady(5)
	c.processRemoteReady(6)
	if !c.fullyDrained() {
		t.Fatal("completed remote read prevented RDMA drain")
	}
}
