package writeback

import (
	"testing"

	cachepkg "github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func newAdaptivePairTestCache() (*Cache, sim.Buffer) {
	component := sim.NewTickingComponent("L2", nil, sim.GHz, nil)
	dramComponent := sim.NewTickingComponent("DRAM", nil, sim.GHz, nil)
	dramPort := sim.NewLimitNumMsgPort(dramComponent, 8, "DRAM.Top")
	bottomPort := sim.NewLimitNumMsgPort(component, 8, "L2.Bottom")
	sendBuffer := sim.NewBuffer("L2.Send", 8)
	writeBuffer := sim.NewBuffer("L2.WriteBuffer", 8)
	c := &Cache{
		TickingComponent:    component,
		bottomPort:          bottomPort,
		bottomSender:        sim.NewBufferedSender(bottomPort, sendBuffer),
		writeBufferBuffer:   writeBuffer,
		lowModuleFinder:     &mem.SingleLowModuleFinder{LowModule: dramPort},
		log2BlockSize:       6,
		adaptivePairEnabled: true,
		adaptivePairAdapter: newAdaptivePairAdapter(4),
		adaptivePairStats:   AdaptivePairStats{Enabled: true},
		directory: cachepkg.NewDirectory(
			4, 2, 64, cachepkg.NewLRUVictimFinder()),
		mshr:                   cachepkg.NewMSHR(8),
		residentFilterBlocks:   make(map[*cachepkg.Block]residentFilterKey),
		residentFilterReliable: true,
	}
	c.requestFilter = NewTypedCuckooFilter(TypedFilterConfig{
		Capacity: 32, Mode: TypedFilterCuckoo,
		LookupLatencyCycles: 1, LookupWidth: 8,
		UpdateLatencyCycles: 1, UpdateWidth: 8, Freq: sim.GHz,
	})
	c.residentFilter = newTypedFilterLineView(
		c.requestFilter, FilterResident)
	c.writeBuffer = &writeBufferStage{cache: c, maxInflightFetch: 8}
	return c, sendBuffer
}

func primeAdaptivePairTestLookup(
	c *Cache,
	trans *transaction,
	now sim.VTimeInSec,
) {
	c.primeAdaptivePairLookups(now, trans)
	if !trans.adaptivePairLookupSet[adaptivePairResidentLookup] ||
		!trans.adaptivePairLookupSet[adaptivePairPendingLookup] {
		panic("adaptive-pair test lookup was not accepted")
	}
}

func adaptivePairTestTransaction(pid vm.PID, address uint64) *transaction {
	read := mem.ReadReqBuilder{}.
		WithPID(pid).
		WithAddress(address).
		WithByteSize(64).
		Build()
	return &transaction{
		action: writeBufferFetch, read: read,
		fetchPID: pid, fetchAddress: address,
	}
}

func trainAdaptivePair(t *testing.T, c *Cache, pid vm.PID) {
	t.Helper()
	for _, address := range []uint64{0x1000, 0x1040, 0x1080, 0x10c0} {
		c.adaptivePairAdapter.observeDemand(c, pid, address)
	}
	if !c.adaptivePairAdapter.shouldPredict() {
		t.Fatalf("historical confidence policy did not train: %+v",
			c.adaptivePairStats)
	}
}

func TestAdaptivePairRestoresHistoricalAligned128BRead(
	t *testing.T,
) {
	c, sendBuffer := newAdaptivePairTestCache()
	pid := vm.PID(1)
	trainAdaptivePair(t, c, pid)

	first := adaptivePairTestTransaction(pid, 0x1100)
	c.writeBufferBuffer.Push(first)
	if !c.writeBuffer.processAdaptivePairFetch(5e-9, first) {
		t.Fatal("confident historical adapter did not issue")
	}
	if sendBuffer.Size() != 1 {
		t.Fatalf("adapter sent %d frontend messages, want one descriptor",
			sendBuffer.Size())
	}
	read, ok := sendBuffer.Peek().(*mem.ReadReq)
	if !ok {
		t.Fatalf("adapter sent %T, want ReadReq", sendBuffer.Peek())
	}
	if read.Address != 0x1100 || read.AccessByteSize != 128 {
		t.Fatalf("historical aligned read is %#x/%dB, want 0x1100/128B",
			read.Address, read.AccessByteSize)
	}

	second := adaptivePairTestTransaction(pid, 0x1140)
	c.writeBufferBuffer.Push(second)
	if !c.writeBuffer.processAdaptivePairFetch(6e-9, second) {
		t.Fatal("real sibling demand did not join the historical inflight entry")
	}
	if sendBuffer.Size() != 1 || second.fetchReadReq != read {
		t.Fatal("inflight sibling demand generated a duplicate DRAM request")
	}
	if c.adaptivePairStats.Predictions != 1 ||
		c.adaptivePairStats.InflightHits != 1 ||
		c.adaptivePairStats.Wide128BReads != 1 ||
		c.adaptivePairStats.CurrentPrefetchOnlyLines != 0 ||
		c.adaptivePairStats.PeakPrefetchOnlyLines != 1 {
		t.Fatalf("unexpected adapter accounting: %+v", c.adaptivePairStats)
	}
}

func TestAdaptivePairBuffersIndependentSiblingResponse(t *testing.T) {
	c, sendBuffer := newAdaptivePairTestCache()
	pid := vm.PID(1)
	trainAdaptivePair(t, c, pid)
	first := adaptivePairTestTransaction(pid, 0x1100)
	c.writeBufferBuffer.Push(first)
	if !c.writeBuffer.processAdaptivePairFetch(5e-9, first) {
		t.Fatal("adapter did not issue")
	}
	read := sendBuffer.Peek().(*mem.ReadReq)
	data := make([]byte, 128)
	data[64] = 0x7b
	rsp := mem.DataReadyRspBuilder{}.
		WithRspTo(read.ID).
		WithData(data).
		Build()
	c.adaptivePairAdapter.captureResponse(c, rsp, read)
	line, ok := c.adaptivePairAdapter.consume(c, pid, 0x1140)
	if !ok || len(line) != 64 || line[0] != 0x7b {
		t.Fatal("later real demand could not consume the sibling buffer")
	}
	if c.adaptivePairStats.BufferHits != 1 ||
		c.adaptivePairStats.CurrentPrefetchOnlyLines != 0 ||
		c.adaptivePairStats.PeakPrefetchOnlyLines != 1 {
		t.Fatalf("buffered prefetch accounting is wrong: %+v",
			c.adaptivePairStats)
	}
}

func TestAdaptivePairCuckooNegativePreservesHistoricalWideRead(t *testing.T) {
	c, sendBuffer := newAdaptivePairTestCache()
	pid := vm.PID(2)
	trainAdaptivePair(t, c, pid)
	trans := adaptivePairTestTransaction(pid, 0x2100)
	primeAdaptivePairTestLookup(c, trans, 1e-9)
	c.writeBufferBuffer.Push(trans)
	if !c.writeBuffer.processAdaptivePairFetch(3e-9, trans) {
		t.Fatal("Filter-negative historical adapter did not issue")
	}
	read := sendBuffer.Peek().(*mem.ReadReq)
	if read.AccessByteSize != 128 {
		t.Fatalf("Filter-negative path issued %d B, want 128 B",
			read.AccessByteSize)
	}
	if c.adaptivePairStats.FilterLookups != 2 ||
		c.adaptivePairStats.Wide128BReads != 1 {
		t.Fatalf("unexpected Filter-negative accounting: %+v",
			c.adaptivePairStats)
	}
}

func TestAdaptivePairCuckooResidentPositiveNeedsExactConfirmation(
	t *testing.T,
) {
	c, sendBuffer := newAdaptivePairTestCache()
	pid := vm.PID(3)
	trainAdaptivePair(t, c, pid)
	sibling := uint64(0x3140)
	block := c.directory.FindVictim(sibling)
	block.PID = pid
	block.Tag = sibling
	block.IsValid = true
	if !c.requestFilter.Insert(TypedFilterKey{
		PID: pid, Address: sibling, Type: FilterResident,
	}) {
		t.Fatal("could not install resident Filter key")
	}

	trans := adaptivePairTestTransaction(pid, 0x3100)
	primeAdaptivePairTestLookup(c, trans, 1e-9)
	c.writeBufferBuffer.Push(trans)
	if !c.writeBuffer.processAdaptivePairFetch(3e-9, trans) {
		t.Fatal("resident suppression did not fall back to singleton")
	}
	read := sendBuffer.Peek().(*mem.ReadReq)
	if read.AccessByteSize != 64 {
		t.Fatalf("resident sibling issued %d B, want singleton 64 B",
			read.AccessByteSize)
	}
	if c.adaptivePairStats.ResidentExactSuppressions != 1 ||
		c.adaptivePairStats.Wide128BReads != 0 {
		t.Fatalf("unexpected resident suppression accounting: %+v",
			c.adaptivePairStats)
	}
}

func TestAdaptivePairCuckooPendingPositiveNeedsExactConfirmation(
	t *testing.T,
) {
	c, sendBuffer := newAdaptivePairTestCache()
	pid := vm.PID(4)
	trainAdaptivePair(t, c, pid)
	sibling := uint64(0x4140)
	c.mshr.Add(pid, sibling)
	if !c.requestFilter.Insert(TypedFilterKey{
		PID: pid, Address: sibling, Type: FilterGranularityPending,
	}) {
		t.Fatal("could not install pending Filter key")
	}

	trans := adaptivePairTestTransaction(pid, 0x4100)
	primeAdaptivePairTestLookup(c, trans, 1e-9)
	c.writeBufferBuffer.Push(trans)
	if !c.writeBuffer.processAdaptivePairFetch(3e-9, trans) {
		t.Fatal("pending suppression did not fall back to singleton")
	}
	read := sendBuffer.Peek().(*mem.ReadReq)
	if read.AccessByteSize != 64 {
		t.Fatalf("pending sibling issued %d B, want singleton 64 B",
			read.AccessByteSize)
	}
	if c.adaptivePairStats.PendingExactSuppressions != 1 ||
		c.adaptivePairStats.Wide128BReads != 0 {
		t.Fatalf("unexpected pending suppression accounting: %+v",
			c.adaptivePairStats)
	}
}
