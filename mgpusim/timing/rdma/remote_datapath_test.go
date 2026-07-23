package rdma

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func attachTestRequestFilter(c *Comp) {
	filter := writeback.NewTypedCuckooFilter(writeback.TypedFilterConfig{
		Capacity:            4096,
		CriticalReserve:     2048,
		Mode:                writeback.TypedFilterCuckoo,
		LookupLatencyCycles: 0,
		LookupWidth:         64,
		UpdateLatencyCycles: 0,
		UpdateWidth:         64,
		Freq:                1 * sim.GHz,
	})
	c.SetRequestFilters([]*writeback.TypedCuckooFilter{filter}, 128)
}

func TestRemoteDataPathDefaultOffKeepsBaselineStateUnallocated(t *testing.T) {
	c := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		Build("RDMA")

	if c.RemoteDataPathStats.Enabled || c.remoteConfig.Enabled ||
		c.remoteBatches != nil || c.remoteLines != nil ||
		c.remoteFilterLookups != nil || c.remoteHintResults != nil ||
		c.remotePrefetcher != nil || c.remotePrefetchCandidates != nil ||
		c.remotePatternFilters != nil || c.remoteProbes != nil ||
		c.remoteSingleInflight != nil || c.remoteBitmapInflight != nil ||
		c.remoteFillInflight != nil || c.remoteEpochs != nil ||
		c.remoteOwnerSubReqs != nil || c.remoteOwnerBatches != nil {
		t.Fatal("default-off RDMA allocated remote mechanism state")
	}
}

func TestRemoteMetadataInterfaceModelsLatencyAndSharedPortWidth(t *testing.T) {
	c, _, _, _, remoteGPU := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 8, MaxBatches: 8})
	filter := writeback.NewTypedCuckooFilter(writeback.TypedFilterConfig{
		Capacity:            64,
		CriticalReserve:     32,
		Mode:                writeback.TypedFilterCuckoo,
		LookupLatencyCycles: 1,
		LookupWidth:         1,
		UpdateLatencyCycles: 1,
		UpdateWidth:         1,
		Freq:                1 * sim.GHz,
	})
	c.SetRequestFilters([]*writeback.TypedCuckooFilter{filter}, 128)
	a := remoteLineIdentity{
		ownerName: remoteGPU.Name(), pid: 1, lineAddr: 0x1000,
	}
	b := remoteLineIdentity{
		ownerName: remoteGPU.Name(), pid: 1, lineAddr: 0x1080,
	}

	if _, ready, progress := c.remotePendingMayContain(0, "lookup-a", a); ready || !progress {
		t.Fatal("first lookup did not reserve the modeled one-cycle port")
	}
	if _, ready, progress := c.remotePendingMayContain(0, "lookup-b", b); ready || progress {
		t.Fatal("second same-cycle lookup bypassed the width-one port")
	}
	if stats := filter.Stats(); stats.LookupPortStalls != 1 {
		t.Fatalf("lookup port stalls = %d, want 1", stats.LookupPortStalls)
	}
	if possible, ready, _ := c.remotePendingMayContain(
		1e-9, "lookup-a", a); !ready || possible {
		t.Fatal("first negative lookup did not complete after one cycle")
	}
	if _, ready, progress := c.remotePendingMayContain(
		1e-9, "lookup-b", b); ready || !progress {
		t.Fatal("blocked lookup did not reserve the next-cycle port")
	}
	if possible, ready, _ := c.remotePendingMayContain(
		2e-9, "lookup-b", b); !ready || possible {
		t.Fatal("second negative lookup did not complete after retry")
	}

	if ready, progress := c.remotePendingInsertReady(3e-9, "update-a", a); !ready || !progress {
		t.Fatal("first asynchronous update was not accepted")
	}
	if ready, progress := c.remotePendingInsertReady(3e-9, "update-b", b); !ready || !progress {
		t.Fatal("second asynchronous update was not queued")
	}
	if stats := filter.Stats(); stats.UpdatePortStalls != 1 {
		t.Fatalf("update port stalls = %d, want 1", stats.UpdatePortStalls)
	}
	if !filter.ExactContains(remoteTypedFilterKey(a, writeback.FilterPending)) ||
		!filter.ExactContains(remoteTypedFilterKey(b, writeback.FilterPending)) {
		t.Fatal("modeled updates did not commit exact PENDING metadata")
	}
}

func TestRemoteParallelHintsMakeProgressWithWidthOne(t *testing.T) {
	c, toL1, _, _, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:         true,
			DisableDedup:    true,
			DisableBatching: true,
			MaxBatchLines:   1,
			MaxBatches:      8,
		})
	filter := writeback.NewTypedCuckooFilter(writeback.TypedFilterConfig{
		Capacity: 64, Mode: writeback.TypedFilterCuckoo,
		LookupLatencyCycles: 0, LookupWidth: 1,
		UpdateLatencyCycles: 1, UpdateWidth: 1,
		Freq: 1 * sim.GHz,
	})
	c.SetRequestFilters([]*writeback.TypedCuckooFilter{filter}, 128)
	c.SetRemoteCacheModuleFinder(&mem.SingleLowModuleFinder{
		LowModule: &remoteTestPort{name: "Requester.L2.Top"},
	})
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(&remoteTestPort{name: "L1"}, 0x1800))

	for cycle := 0; cycle < 4 && len(toL1.inbox) > 0; cycle++ {
		c.processFromL1(sim.VTimeInSec(cycle) * 1e-9)
	}
	if len(toL1.inbox) != 0 || len(c.remoteLines) != 1 {
		t.Fatal("serialized SEEN/RESIDENT hints livelocked the head request")
	}
	stats := c.GetRemoteDataPathStats()
	if stats.SeenQueries != 1 || stats.ResidentQueries != 1 {
		t.Fatalf("hint results were re-queried: %+v", stats)
	}
}

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
	attachTestRequestFilter(c)
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

func remoteTestWrite(src sim.Port, address uint64) *mem.WriteReq {
	return mem.WriteReqBuilder{}.
		WithSrc(src).
		WithAddress(address).
		WithData(make([]byte, remoteLineBytes)).
		Build()
}

func TestRemoteDataPathWorkConservingBatchUsesCurrentCycleOnly(t *testing.T) {
	c, toL1, toOutside := newPipelineTestComp(8, 0, 64,
		RemoteDataPathConfig{
			Enabled:            true,
			DisableRequesterL2: true,
			MaxBatchLines:      8,
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
	if stats.WorkConservingFlushes != 1 || stats.LogicalRemoteReads != 2 {
		t.Fatalf("unexpected work-conserving stats: %+v", stats)
	}
}

func TestRemoteFilterPrefetchPiggybacksDemandBatchOnly(t *testing.T) {
	c, toL1, _, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled: true, DisableRequesterL2: true,
			EnableFilterPrefetch: true,
			PrefetchEntries:      17,
			MaxBatchLines:        8, MaxBatches: 8,
		})
	if capacity := c.GetRemoteDataPathStats().PrefetchPredictor.Capacity; capacity != 17 {
		t.Fatalf("remote predictor capacity = %d, want 17", capacity)
	}
	l1 := &remoteTestPort{name: "L1"}
	for _, address := range []uint64{0x1000, 0x1040, 0x1080, 0x10c0} {
		toL1.inbox = append(toL1.inbox, remoteTestRead(l1, address))
	}
	if !c.Tick(1) {
		t.Fatal("remote demand stream made no progress")
	}
	if len(toOutside.sent) != 1 {
		t.Fatalf("packets = %d, want one piggybacked bitmap", len(toOutside.sent))
	}
	req, ok := toOutside.sent[0].(*BitmapReadReq)
	if !ok || req.LineBitmap != 0x1f {
		t.Fatalf("bitmap = %#v, want four demands plus one predicted line", req)
	}
	stats := c.GetRemoteDataPathStats()
	if stats.PrefetchPiggybackLines != 1 || stats.PrefetchWireLines != 1 ||
		stats.DemandWireLines != 4 || stats.LogicalRemoteReads != 4 {
		t.Fatalf("incorrect demand/speculation accounting: %+v", stats)
	}
}

func TestRemotePiggybackedPrefetchMergesLaterDemandWithoutSecondPacket(
	t *testing.T,
) {
	c, toL1, _, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled: true, DisableRequesterL2: true,
			EnableFilterPrefetch: true,
			MaxBatchLines:        8, MaxBatches: 8,
		})
	l1 := &remoteTestPort{name: "L1"}
	for _, address := range []uint64{0x1000, 0x1040, 0x1080, 0x10c0} {
		toL1.inbox = append(toL1.inbox, remoteTestRead(l1, address))
	}
	if !c.Tick(1) || len(toOutside.sent) != 1 {
		t.Fatal("demand batch with predicted line was not sent")
	}

	toL1.inbox = append(toL1.inbox, remoteTestRead(l1, 0x1100))
	if !c.Tick(2) {
		t.Fatal("later demand did not merge into the predicted line")
	}
	if len(toOutside.sent) != 1 {
		t.Fatalf("packets = %d, want no second packet", len(toOutside.sent))
	}
	stats := c.GetRemoteDataPathStats()
	if stats.PrefetchPiggybackLines != 1 || stats.PrefetchUseful != 1 {
		t.Fatalf("piggyback/useful accounting = %+v", stats)
	}
	if stats.CollectingMerges+stats.InflightMerges+stats.ReadyMerges != 1 {
		t.Fatalf("exact demand merges = %d/%d/%d, want one",
			stats.CollectingMerges, stats.InflightMerges, stats.ReadyMerges)
	}
}

func TestRemoteFilterPrefetchNeverCreatesStandaloneBatch(t *testing.T) {
	c, _, _, _, remoteGPU := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled: true, EnableFilterPrefetch: true,
			MaxBatchLines: 8, MaxBatches: 8,
		})
	identity := remoteLineIdentity{
		ownerName: remoteGPU.Name(), pid: 1, lineAddr: 0x1400,
	}
	entry := c.newRemoteDemandEntry(
		remoteLineKey{remoteLineIdentity: identity}, remoteGPU, nil)
	entry.speculative = true
	entry.state = remoteLinePendingBatch
	c.insertRemoteLine(entry.key, entry)
	added, progress := c.tryAddRemoteEntryToBatch(1, entry)
	if !added || !progress || len(c.remoteBatchOrder) != 0 ||
		c.remoteLines[entry.key] != nil {
		t.Fatal("standalone speculative line was not dropped immediately")
	}
	if c.GetRemoteDataPathStats().PrefetchStandalonePrevented != 1 {
		t.Fatal("standalone prevention was not accounted")
	}
	filter := c.requestFilterForAddress(identity.lineAddr)
	if filter.ExactContains(remoteTypedFilterKey(
		identity, writeback.FilterSeen)) {
		t.Fatal("speculative-only access incorrectly established SEEN")
	}
}

func TestRemoteDemandTakesOverUnsentPrefetchWithoutLosingWaiter(t *testing.T) {
	c, _, _, _, remoteGPU := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled: true, EnableFilterPrefetch: true,
			MaxBatchLines: 8, MaxBatches: 8,
		})
	identity := remoteLineIdentity{
		ownerName: remoteGPU.Name(), pid: 1, lineAddr: 0x1400,
	}
	entry := c.newRemoteDemandEntry(
		remoteLineKey{remoteLineIdentity: identity}, remoteGPU, nil)
	entry.speculative = true
	entry.state = remoteLinePendingBatch
	c.insertRemoteLine(entry.key, entry)

	read := remoteTestRead(&remoteTestPort{name: "L1"}, identity.lineAddr)
	c.addRemoteWaiter(entry, read, 1, 1, 0)
	if entry.speculative || !entry.speculativeUseful || !entry.admit {
		t.Fatalf("demand did not take ownership of prediction: %#v", entry)
	}
	if len(entry.waiters) != 1 || entry.waiters[0].req != read {
		t.Fatal("demand waiter was not retained by the promoted entry")
	}

	added, progress := c.tryAddRemoteEntryToBatch(1, entry)
	if !added || !progress || len(c.remoteBatchOrder) != 1 ||
		c.remoteLines[entry.key] != entry || entry.batch == nil {
		t.Fatal("promoted demand was discarded instead of entering a demand batch")
	}
	stats := c.GetRemoteDataPathStats()
	if stats.PrefetchUseful != 1 || stats.PrefetchStandalonePrevented != 0 {
		t.Fatalf("incorrect takeover accounting: %+v", stats)
	}
}

func TestRemoteFirstTouchPrefetchFillRequiresInvalidVictim(t *testing.T) {
	c, _, toL2, _, remoteGPU := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled: true, EnableFilterPrefetch: true,
			MaxBatchLines: 8, MaxBatches: 8,
		})
	c.SetRemoteCacheModuleFinder(&mem.SingleLowModuleFinder{
		LowModule: &remoteTestPort{name: "Requester.L2.Top"},
	})
	identity := remoteLineIdentity{
		ownerName: remoteGPU.Name(), pid: 1, lineAddr: 0x1800,
	}
	entry := c.newRemoteDemandEntry(
		remoteLineKey{remoteLineIdentity: identity}, remoteGPU, nil)
	entry.speculative = true
	entry.admit = true
	entry.fromRemote = true
	entry.data = make([]byte, remoteLineBytes)
	entry.patternKey = writeback.TypedFilterKey{
		PID: 1, Owner: 7, Address: 9, Type: writeback.FilterPattern,
	}
	c.insertRemoteLine(entry.key, entry)
	c.queueRemoteReady(entry)
	if !c.processRemoteReady(1) || len(toL2.sent) != 1 {
		t.Fatal("first-touch prefetch response did not attempt requester-L2 fill")
	}
	fill := toL2.sent[0].(*mem.RemoteDataFill)
	if !fill.HasPattern || !fill.RequireInvalidVictim {
		t.Fatalf("first-touch fill policy = %#v", fill)
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
	c, toL1, toL2, toOutside, remoteGPU := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:       true,
			MaxBatchLines: 8,
			MaxBatches:    8,
		})
	c.SetRemoteCacheModuleFinder(&mem.SingleLowModuleFinder{
		LowModule: &remoteTestPort{name: "Requester.L2.Top"},
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
		if len(entry.waiters) != 2 || !entry.admit ||
			!entry.multipleDemandAdmission {
			t.Fatalf("collecting entry has %d waiters, admit=%v, multi=%v",
				len(entry.waiters), entry.admit,
				entry.multipleDemandAdmission)
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
	filterStats := c.GetRemoteDataPathStats()
	if filterStats.InflightFilterQueries != 3 ||
		filterStats.InflightFilterNegatives != 1 ||
		filterStats.InflightFilterPositives != 2 ||
		filterStats.ExactTableLookupsAvoided != 1 ||
		filterStats.ExactTableLookups != 2 {
		t.Fatalf("unexpected inflight-filter stats: %+v", filterStats)
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

	if !c.processRemoteReady(40) {
		t.Fatal("completed coalesced line did not issue a clean fill")
	}
	if got := len(toL2.sent); got != 1 {
		t.Fatalf("same-epoch duplicates issued %d clean fills, want 1", got)
	}
	fill := toL2.sent[0].(*mem.RemoteDataFill)
	toL2.inbox = append(toL2.inbox, mem.RemoteDataFillRspBuilder{}.
		WithRspTo(fill.ID).
		WithInstalled(true).
		Build())
	if !c.processFromL2(41) {
		t.Fatal("clean-fill response was not consumed")
	}
	if len(c.remoteLines) != 0 {
		t.Fatal("line entry was not retired after the clean fill")
	}
	if c.RemoteDataPathStats.TwoTouchCandidates != 1 ||
		c.RemoteDataPathStats.MultipleDemandAdmissions != 1 {
		t.Fatalf("same-epoch duplicates created invalid admission counters: %+v",
			c.RemoteDataPathStats)
	}
	identity := c.remoteIdentity(first, remoteGPU)
	filter := c.requestFilterForAddress(identity.lineAddr)
	if filter.ExactContains(remoteTypedFilterKey(identity, writeback.FilterSeen)) ||
		filter.ExactContains(remoteTypedFilterKey(identity, writeback.FilterPending)) {
		t.Fatal("installed requester-L2 fill left stale SEEN or PENDING metadata")
	}
}

func TestRemoteInflightFilterInsertFailureFallsBackToExactTable(t *testing.T) {
	c, toL1, _, _, remoteGPU := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:            true,
			DisableRequesterL2: true,
			MaxBatchLines:      1,
			MaxBatches:         1,
		})
	// Force a tiny approximate structure, then populate exact line state until
	// a bounded kick fails. insertRemoteLine must keep the exact entry and make
	// every later query fail open to that table.
	tiny := writeback.NewTypedCuckooFilter(writeback.TypedFilterConfig{
		Capacity: 1, Mode: writeback.TypedFilterCuckoo,
		LookupWidth: 64, UpdateWidth: 64, Freq: 1 * sim.GHz,
	})
	c.SetRequestFilters([]*writeback.TypedCuckooFilter{tiny}, 128)
	var failedEntry *remoteLineEntry
	for i := 0; i < 10000 && failedEntry == nil; i++ {
		identity := remoteLineIdentity{
			ownerName: remoteGPU.Name(),
			pid:       vm.PID(i%7 + 1),
			lineAddr:  uint64(i+1) * remoteLineBytes,
		}
		key := remoteLineKey{remoteLineIdentity: identity}
		entry := c.newRemoteDemandEntry(key, remoteGPU, nil)
		c.insertRemoteLine(key, entry)
		if c.RemoteDataPathStats.InflightFilterInsertFailures > 0 {
			failedEntry = entry
		}
	}
	if failedEntry == nil ||
		c.RemoteDataPathStats.InflightFilterInsertFailures != 1 {
		t.Fatal("could not force an inflight-filter insertion failure")
	}

	l1 := &remoteTestPort{name: "L1"}
	read := mem.ReadReqBuilder{}.
		WithSrc(l1).
		WithPID(failedEntry.key.pid).
		WithAddress(failedEntry.key.lineAddr).
		WithByteSize(remoteLineBytes).
		Build()
	toL1.inbox = append(toL1.inbox, read)
	handled, progress := c.tryProcessRemoteReqFromL1(
		1, read, remoteGPU, 1,
	)
	if !handled || !progress || len(toL1.inbox) != 0 {
		t.Fatal("fail-open exact lookup did not consume matching remote read")
	}
	if len(failedEntry.waiters) != 1 ||
		c.RemoteDataPathStats.DuplicateReads != 1 ||
		c.RemoteDataPathStats.ExactTableLookups != 1 {
		t.Fatalf("fail-open exact merge stats are invalid: %+v",
			c.RemoteDataPathStats)
	}
}

func TestRemoteDataPathSecondCompletedTransactionAdmitsFill(t *testing.T) {
	c, toL1, toL2, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:         true,
			DisableBatching: true,
			MaxBatchLines:   8,
			MaxBatches:      8,
		})
	l2Top := &remoteTestPort{name: "Requester.L2[0].Top"}
	c.SetRemoteCacheModuleFinder(
		&mem.SingleLowModuleFinder{LowModule: l2Top})
	l1 := &remoteTestPort{name: "L1"}

	first := remoteTestRead(l1, 0x2400)
	toL1.inbox = append(toL1.inbox, first)
	c.processFromL1(1)
	c.processRemotePendingBatches(2)
	firstWire := toOutside.sent[0].(*mem.ReadReq)
	toOutside.inbox = append(toOutside.inbox, mem.DataReadyRspBuilder{}.
		WithRspTo(firstWire.ID).
		WithData(make([]byte, remoteLineBytes)).
		Build())
	c.processFromOutside(3)
	c.processRemoteReady(4)
	c.processRemoteReady(5)
	if len(toL2.sent) != 0 || len(c.remoteLines) != 0 {
		t.Fatal("first completed transaction unexpectedly filled requester L2")
	}
	identity := c.remoteIdentity(first, firstWire.Dst)
	filter := c.requestFilterForAddress(identity.lineAddr)
	if !filter.ExactContains(remoteTypedFilterKey(identity, writeback.FilterSeen)) {
		t.Fatal("first completed remote transaction did not set SEEN")
	}

	second := remoteTestRead(l1, 0x2400)
	toL1.inbox = append(toL1.inbox, second)
	c.processFromL1(6)
	if len(toL2.sent) != 1 {
		t.Fatalf("second transaction probes = %d, want 1", len(toL2.sent))
	}
	probe := toL2.sent[0].(*mem.ReadReq)
	toL2.inbox = append(toL2.inbox, mem.CacheLookupRspBuilder{}.
		WithRspTo(probe.ID).
		WithHit(false).
		Build())
	c.processFromL2(7)
	c.processRemotePendingBatches(8)
	secondWire := toOutside.sent[1].(*mem.ReadReq)
	toOutside.inbox = append(toOutside.inbox, mem.DataReadyRspBuilder{}.
		WithRspTo(secondWire.ID).
		WithData(make([]byte, remoteLineBytes)).
		Build())
	c.processFromOutside(9)
	c.processRemoteReady(10)
	c.processRemoteReady(11)
	if len(toL2.sent) != 2 {
		t.Fatalf("second transaction L2 messages = %d, want probe + fill",
			len(toL2.sent))
	}
	fill := toL2.sent[1].(*mem.RemoteDataFill)
	toL2.inbox = append(toL2.inbox, mem.RemoteDataFillRspBuilder{}.
		WithRspTo(fill.ID).
		WithInstalled(true).
		Build())
	c.processFromL2(12)
	if len(c.remoteLines) != 0 {
		t.Fatal("temporal line entry was not retired after fill")
	}
	stats := c.GetRemoteDataPathStats()
	if stats.TwoTouchCandidates != 1 ||
		stats.TwoTouchFillAttempts != 1 ||
		stats.TwoTouchInstalledFills != 1 ||
		stats.FirstTouchRemoteLines != 1 ||
		stats.SecondTouchAdmissions != 1 {
		t.Fatalf("unexpected SEEN admission stats: %+v", stats)
	}
	if filter.ExactContains(remoteTypedFilterKey(identity, writeback.FilterSeen)) {
		t.Fatal("successful second-touch fill left stale SEEN metadata")
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

func TestRemoteSeenPositiveStillRequiresExactL2Lookup(t *testing.T) {
	c, toL1, toL2, toOutside, remoteGPU := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:         true,
			DisableBatching: true,
			MaxBatchLines:   8,
			MaxBatches:      8,
		})
	l2Top := &remoteTestPort{name: "Requester.L2.Top"}
	c.SetRemoteCacheModuleFinder(&mem.SingleLowModuleFinder{LowModule: l2Top})
	l1 := &remoteTestPort{name: "L1"}
	read := remoteTestRead(l1, 0x3c80)
	identity := c.remoteIdentity(read, remoteGPU)
	c.markRemoteSeen(identity)
	toL1.inbox = append(toL1.inbox, read)

	c.processFromL1(1)
	if len(toL2.sent) != 1 || len(toOutside.sent) != 0 {
		t.Fatal("SEEN positive did not perform exact requester-L2 lookup first")
	}
	probe := toL2.sent[0].(*mem.ReadReq)
	toL2.inbox = append(toL2.inbox, mem.CacheLookupRspBuilder{}.
		WithRspTo(probe.ID).
		WithHit(false).
		Build())
	c.processFromL2(2)
	c.processRemotePendingBatches(3)
	if len(toOutside.sent) != 1 {
		t.Fatal("exact requester-L2 miss did not fall back to remote memory")
	}
}

func TestRemoteDisabledFilterFailsOpenToExactRequesterL2Probe(t *testing.T) {
	c, toL1, toL2, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:         true,
			DisableBatching: true,
			MaxBatchLines:   8,
			MaxBatches:      8,
		})
	disabled := writeback.NewTypedCuckooFilter(writeback.TypedFilterConfig{
		Capacity: 64, Mode: writeback.TypedFilterDisabled,
	})
	c.SetRequestFilters([]*writeback.TypedCuckooFilter{disabled}, 128)
	l2Top := &remoteTestPort{name: "Requester.L2.Top"}
	c.SetRemoteCacheModuleFinder(&mem.SingleLowModuleFinder{LowModule: l2Top})
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(&remoteTestPort{name: "L1"}, 0x3d00))

	c.processFromL1(1)
	if len(toL2.sent) != 1 || len(toOutside.sent) != 0 {
		t.Fatal("disabled Filter did not fail open to an exact requester-L2 probe")
	}
	probe := toL2.sent[0].(*mem.ReadReq)
	toL2.inbox = append(toL2.inbox, mem.CacheLookupRspBuilder{}.
		WithRspTo(probe.ID).
		WithHit(false).
		Build())
	c.processFromL2(2)
	c.processRemotePendingBatches(3)
	if len(toOutside.sent) != 1 {
		t.Fatal("fail-open requester-L2 miss did not continue to remote memory")
	}
	if got := c.GetRemoteDataPathStats().L2OneTouchProbeBypasses; got != 0 {
		t.Fatalf("disabled Filter bypassed %d exact requester-L2 probes", got)
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
	if len(toL2.sent) != 0 {
		t.Fatalf("concurrent first transactions probed requester L2 %d times",
			len(toL2.sent))
	}
	c.processRemotePendingBatches(2)
	c.processRemotePendingBatches(3)
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
	if stats.L2OneTouchProbeBypasses != 2 {
		t.Fatalf("pre-admission probe bypasses = %d, want 2",
			stats.L2OneTouchProbeBypasses)
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
	identity := c.remoteIdentity(readBefore, toOutside.sent[0].Meta().Dst)
	c.markRemoteSeen(identity)
	c.processFromL1(4)
	if len(toOutside.sent) != 2 {
		t.Fatal("write was not sent after the older read")
	}
	filter := c.requestFilterForAddress(identity.lineAddr)
	if !c.remoteUncacheable[identity] ||
		filter.ExactContains(remoteTypedFilterKey(identity, writeback.FilterSeen)) {
		t.Fatal("remote write did not invalidate requester-L2 eligibility and SEEN")
	}

	readAfter := remoteTestRead(l1, 0x5000)
	toL1.inbox = append(toL1.inbox, readAfter)
	c.processFromL1(5)
	if len(c.remoteLines) != 2 {
		t.Fatal("post-write read merged into the pre-write inflight read")
	}
	if c.remoteEpochs[identity] != 1 {
		t.Fatalf("read epoch = %d, want 1", c.remoteEpochs[identity])
	}
}

func TestWriteUncacheableLineDoesNotPolluteRemoteReuseHistory(t *testing.T) {
	c, _, _, _, remoteGPU := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:       true,
			MaxBatchLines: 8,
			MaxBatches:    8,
		})
	read := remoteTestRead(&remoteTestPort{name: "L1"}, 0x5800)
	identity := c.remoteIdentity(read, remoteGPU)
	c.remoteUncacheable[identity] = true
	entry := c.newRemoteDemandEntry(remoteLineKey{
		remoteLineIdentity: identity,
		epoch:              1,
	}, remoteGPU, read.Info)
	c.insertRemoteLine(entry.key, entry)

	c.removeRemoteLine(entry)
	filter := c.requestFilterForAddress(identity.lineAddr)
	if filter.ExactContains(remoteTypedFilterKey(identity, writeback.FilterSeen)) {
		t.Fatal("write-uncacheable line created SEEN history")
	}
	if c.RemoteDataPathStats.ReuseWriteUncacheableSkips != 1 {
		t.Fatal("write-uncacheable reuse-history skip was not recorded")
	}
	if len(c.remoteLines) != 0 ||
		c.remoteInflightMayContain(identity) {
		t.Fatal("retired write-uncacheable line remained in inflight state")
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

func TestBitmapOwnerReturnsReadyLinesWithoutWaitingForStraggler(t *testing.T) {
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
	toOutside.inbox = append(toOutside.inbox, bitmap)
	c.processFromOutside(1)
	c.processRemoteOwnerPendingReqs(2)
	c.processRemoteOwnerPendingReqs(3)

	firstRead := toL2.sent[0].(*mem.ReadReq)
	firstData := make([]byte, remoteLineBytes)
	firstData[0] = 1
	firstRsp := mem.DataReadyRspBuilder{}.
		WithRspTo(firstRead.ID).
		WithData(firstData).
		Build()
	toL2.inbox = append(toL2.inbox, firstRsp)
	if !c.processRemoteOwnerSubRsp(4, firstRsp) ||
		!c.processRemoteOwnerPendingRsps(5) {
		t.Fatal("first ready line was not returned")
	}
	if len(toOutside.sent) != 1 {
		t.Fatalf("partial responses = %d, want 1", len(toOutside.sent))
	}
	partial := toOutside.sent[0].(*BitmapReadRsp)
	if len(partial.LineData) != 1 || partial.LineData[0][0] != 1 {
		t.Fatalf("first partial response is invalid: %#v", partial.LineData)
	}
	if len(c.remoteOwnerSubReqs) != 1 {
		t.Fatal("first response incorrectly waited for or removed the straggler")
	}
	if c.RemoteDataPathStats.EarlyBitmapResponses != 1 {
		t.Fatal("early response was not recorded")
	}

	secondRead := toL2.sent[1].(*mem.ReadReq)
	secondData := make([]byte, remoteLineBytes)
	secondData[0] = 2
	secondRsp := mem.DataReadyRspBuilder{}.
		WithRspTo(secondRead.ID).
		WithData(secondData).
		Build()
	toL2.inbox = append(toL2.inbox, secondRsp)
	c.processRemoteOwnerSubRsp(6, secondRsp)
	c.processRemoteOwnerPendingRsps(7)
	if len(toOutside.sent) != 2 ||
		toOutside.sent[1].(*BitmapReadRsp).LineData[1][0] != 2 {
		t.Fatal("straggler line was not returned in the later response")
	}
	if len(c.remoteOwnerBatches) != 0 {
		t.Fatal("completed bitmap retained its RDMA outstanding descriptor")
	}
}

func TestBitmapOwnerRetainsDescriptorAcrossResponseBackpressure(t *testing.T) {
	c, _, toL2, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 8, MaxBatches: 8})
	c.localModules = &mem.SingleLowModuleFinder{
		LowModule: &remoteTestPort{name: "Owner.L2.Top"},
	}
	requester := &remoteTestPort{name: "Requester.RDMA"}
	bitmap := &BitmapReadReq{
		MsgMeta:    sim.MsgMeta{Src: requester, Dst: toOutside},
		PagePAddr:  0x6800,
		LineBitmap: 0x3,
	}
	toOutside.inbox = append(toOutside.inbox, bitmap)
	c.processFromOutside(1)
	c.processRemoteOwnerPendingReqs(2)
	c.processRemoteOwnerPendingReqs(3)

	for _, msg := range toL2.sent {
		read := msg.(*mem.ReadReq)
		rsp := mem.DataReadyRspBuilder{}.
			WithRspTo(read.ID).
			WithData(make([]byte, remoteLineBytes)).
			Build()
		toL2.inbox = append(toL2.inbox, rsp)
		c.processRemoteOwnerSubRsp(4, rsp)
	}
	if len(c.remoteOwnerPendingRsp) != 1 ||
		len(c.remoteOwnerBatches) != 1 ||
		len(c.remoteOwnerSubReqs) != 0 {
		t.Fatal("completed children did not retain exactly one pending packet")
	}

	toOutside.blocked = true
	if c.processRemoteOwnerPendingRsps(5) {
		t.Fatal("blocked response unexpectedly made progress")
	}
	if len(c.remoteOwnerPendingRsp) != 1 ||
		len(c.remoteOwnerBatches) != 1 ||
		c.ownerOutstandingCount() != 1 {
		t.Fatal("response backpressure released the owner descriptor early")
	}

	toOutside.blocked = false
	if !c.processRemoteOwnerPendingRsps(6) {
		t.Fatal("unblocked final response did not make progress")
	}
	if len(c.remoteOwnerPendingRsp) != 0 ||
		len(c.remoteOwnerBatches) != 0 ||
		c.ownerOutstandingCount() != 0 {
		t.Fatal("successful final response retained the owner descriptor")
	}
}

func TestBitmapRequesterAcceptsPartialResponses(t *testing.T) {
	c, toL1, _, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:            true,
			DisableRequesterL2: true,
			MaxBatchLines:      8,
			MaxBatches:         8,
		})
	l1 := &remoteTestPort{name: "L1"}
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(l1, 0x6000), remoteTestRead(l1, 0x6040))
	c.processFromL1(1)
	c.processRemotePendingBatches(2)
	c.processRemotePendingBatches(2)
	c.processRemoteBatches(2, false)
	req := toOutside.sent[0].(*BitmapReadReq)

	first := &BitmapReadRsp{
		MsgMeta:   sim.MsgMeta{Dst: toOutside},
		RespondTo: req.ID,
		LineData:  map[uint64][]byte{0: make([]byte, remoteLineBytes)},
	}
	toOutside.inbox = append(toOutside.inbox, first)
	if !c.processBitmapRspFromOutside(3, first) {
		t.Fatal("first partial response was rejected")
	}
	if c.remoteBitmapInflight[req.ID] == nil || len(c.remoteReady) != 1 {
		t.Fatal("requester retired the bitmap before all lines returned")
	}
	if c.requesterOutstandingCount() != 1 {
		t.Fatal("partial bitmap response freed its RDMA descriptor too early")
	}

	second := &BitmapReadRsp{
		MsgMeta:   sim.MsgMeta{Dst: toOutside},
		RespondTo: req.ID,
		LineData:  map[uint64][]byte{1: make([]byte, remoteLineBytes)},
	}
	toOutside.inbox = append(toOutside.inbox, second)
	c.processBitmapRspFromOutside(4, second)
	if c.remoteBitmapInflight[req.ID] != nil || len(c.remoteReady) != 2 {
		t.Fatal("requester did not retire the fully returned bitmap")
	}
	if c.requesterOutstandingCount() != 0 {
		t.Fatal("complete bitmap response retained its RDMA descriptor")
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
	if c.RemoteDataPathStats.LineEntryFullStalls != 1 {
		t.Fatalf("line-entry full stalls = %d, want 1",
			c.RemoteDataPathStats.LineEntryFullStalls)
	}
	queries := c.RemoteDataPathStats.InflightFilterQueries
	if c.processFromL1(2) {
		t.Fatal("capacity-blocked retry unexpectedly made progress")
	}
	if c.RemoteDataPathStats.InflightFilterQueries != queries {
		t.Fatalf("blocked-head retry repeated inflight-filter query: %d -> %d",
			queries, c.RemoteDataPathStats.InflightFilterQueries)
	}
	if c.RemoteDataPathStats.LineEntryFullStalls != 2 {
		t.Fatalf("line-entry full stalls after retry = %d, want 2",
			c.RemoteDataPathStats.LineEntryFullStalls)
	}
}

func TestRemoteDataPathBoundsDuplicateWaiters(t *testing.T) {
	c, toL1, _, _, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 1, MaxBatches: 1})
	l1 := &remoteTestPort{name: "L1"}
	toL1.inbox = append(toL1.inbox,
		remoteTestRead(l1, 0x7800),
		remoteTestRead(l1, 0x7800),
		remoteTestRead(l1, 0x7800),
	)
	c.processFromL1(1)
	if c.remoteOutstandingReads != 2 || len(c.remoteLines) != 1 {
		t.Fatalf("bounded duplicate state = waiters %d, lines %d; want 2, 1",
			c.remoteOutstandingReads, len(c.remoteLines))
	}
	if len(toL1.inbox) != 1 {
		t.Fatalf("upstream duplicate requests left = %d, want 1",
			len(toL1.inbox))
	}
	stats := c.RemoteDataPathStats
	if stats.DuplicateReads != 1 || stats.WaiterEntryFullStalls != 1 ||
		stats.LineEntryFullStalls != 0 || stats.PeakWaiterEntries != 2 ||
		stats.WaiterEntryCapacity != 2 {
		t.Fatalf("duplicate waiter accounting = %+v", stats)
	}
}

func TestBitmapOwnerRejectsPacketBeyondOutstandingLimit(t *testing.T) {
	c, _, _, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 1, MaxBatches: 1})
	for i := 0; i < c.remoteConfig.MaxBatches; i++ {
		c.remoteOwnerBatches[string(rune(i+1))] = &remoteOwnerBatch{}
	}
	request := &BitmapReadReq{
		MsgMeta:    sim.MsgMeta{Src: &remoteTestPort{name: "Requester"}},
		PagePAddr:  0x8000,
		LineBitmap: 0x1,
	}
	toOutside.inbox = append(toOutside.inbox, request)
	if c.processFromOutside(1) {
		t.Fatal("oversized owner packet unexpectedly made progress")
	}
	if len(toOutside.inbox) != 1 ||
		len(c.remoteOwnerBatches) != c.remoteConfig.MaxBatches {
		t.Fatal("owner consumed a packet that exceeded its bounded capacity")
	}
}

func TestBitmapOwnerRejectsOversizedPacket(t *testing.T) {
	c, _, _, _, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 1, MaxBatches: 1})
	request := &BitmapReadReq{
		MsgMeta:    sim.MsgMeta{Src: &remoteTestPort{name: "Requester"}},
		PagePAddr:  0x8000,
		LineBitmap: 0x3,
	}
	defer func() {
		if recover() == nil {
			t.Fatal("owner accepted a bitmap larger than the protocol limit")
		}
	}()
	c.processBitmapReqFromOutside(1, request)
}

func TestBitmapOwnerBoundsExpandedChildLines(t *testing.T) {
	c, _, _, toOutside, _ := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{Enabled: true, MaxBatchLines: 1, MaxBatches: 1})
	c.remoteOwnerSubReqs["occupied"] = &remoteOwnerSubReq{}
	request := &BitmapReadReq{
		MsgMeta:    sim.MsgMeta{Src: &remoteTestPort{name: "Requester"}},
		PagePAddr:  0x8000,
		LineBitmap: 0x1,
	}
	toOutside.inbox = append(toOutside.inbox, request)
	if c.processFromOutside(1) {
		t.Fatal("owner consumed a packet without child-line capacity")
	}
	stats := c.RemoteDataPathStats
	if len(toOutside.inbox) != 1 || len(c.remoteOwnerBatches) != 0 ||
		stats.OwnerChildLineCapacity != 1 ||
		stats.OwnerChildLineFullStalls != 1 {
		t.Fatalf("owner child-line backpressure accounting = %+v", stats)
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
