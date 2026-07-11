package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

type dramBatchCaptureSender struct {
	messages []sim.Msg
}

type dramBatchAddressFinder struct {
	lowAddressModule  sim.Port
	highAddressModule sim.Port
}

func (f *dramBatchAddressFinder) Find(address uint64) sim.Port {
	if address&0x40 == 0 {
		return f.lowAddressModule
	}
	return f.highAddressModule
}

func (s *dramBatchCaptureSender) CanSend(count int) bool {
	return true
}

func (s *dramBatchCaptureSender) Send(msg sim.Msg) {
	s.messages = append(s.messages, msg)
}

func (s *dramBatchCaptureSender) Clear() {
	s.messages = nil
}

func (s *dramBatchCaptureSender) Tick(now sim.VTimeInSec) bool {
	return false
}

func newDRAMBatchTestCache(
	t *testing.T,
	config DRAMBatchConfig,
) (*Cache, *dramBatchCaptureSender, sim.Port, sim.Port) {
	t.Helper()

	cacheModule := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithDRAMBatchConfig(config).
		Build("L2")
	lowModule := sim.NewLimitNumMsgPort(cacheModule, 4, "DRAM.Top")
	remoteSource := sim.NewLimitNumMsgPort(
		cacheModule, 4, "GPU.RDMA.ToL2")
	cacheModule.SetLowModuleFinder(&mem.SingleLowModuleFinder{
		LowModule: lowModule,
	})
	sender := &dramBatchCaptureSender{}
	cacheModule.bottomSender = sender

	return cacheModule, sender, lowModule, remoteSource
}

func newDRAMBatchTestTransaction(
	cacheModule *Cache,
	source sim.Port,
	pid vm.PID,
	address uint64,
) *transaction {
	read := mem.ReadReqBuilder{}.
		WithSrc(source).
		WithDst(cacheModule.topPort).
		WithPID(pid).
		WithAddress(address).
		WithByteSize(64).
		Build()
	block := &cache.Block{}
	mshrEntry := cacheModule.mshr.Add(pid, address)
	mshrEntry.Block = block
	trans := &transaction{
		action:       writeBufferFetch,
		read:         read,
		block:        block,
		fetchPID:     pid,
		fetchAddress: address,
		mshrEntry:    mshrEntry,
	}
	mshrEntry.Requests = append(mshrEntry.Requests, trans)
	return trans
}

func TestDRAMBatchDefaultsToDisabled(t *testing.T) {
	cacheModule := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		Build("L2")

	if cacheModule.GetDRAMBatchStats().Enabled {
		t.Fatal("DRAM batching must be disabled by default")
	}
}

func TestDRAMBatchCombinesAdjacentRemoteReadMisses(t *testing.T) {
	config := DRAMBatchConfig{
		Enabled:     true,
		MaxEntries:  4,
		MaxLines:    2,
		MaxWaitNS:   25,
		WindowLines: 2,
	}
	cacheModule, sender, lowModule, remoteSource :=
		newDRAMBatchTestCache(t, config)
	pid := vm.PID(3)
	first := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, pid, 0x1000)
	second := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, pid, 0x1040)

	if !cacheModule.enqueueDRAMBatchMiss(10e-9, first) {
		t.Fatal("first L2 miss was not accepted")
	}
	if !cacheModule.enqueueDRAMBatchMiss(12e-9, second) {
		t.Fatal("second L2 miss was not accepted")
	}

	if len(sender.messages) != 1 {
		t.Fatalf("sent %d low-module requests, want 1", len(sender.messages))
	}
	read, ok := sender.messages[0].(*mem.ReadReq)
	if !ok {
		t.Fatalf("sent %T, want *mem.ReadReq", sender.messages[0])
	}
	if read.Address != 0x1000 || read.AccessByteSize != 128 {
		t.Fatalf("combined read is [0x%x, %dB], want [0x1000, 128B]",
			read.Address, read.AccessByteSize)
	}
	if read.Dst != lowModule || first.fetchReadReq != read ||
		second.fetchReadReq != read {
		t.Fatal("the two misses do not share the expected low-module request")
	}

	stats := cacheModule.GetDRAMBatchStats()
	if stats.MissLinesSeen != 2 || stats.BatchesCreated != 1 ||
		stats.BatchesDrained != 1 || stats.LinesInBatches != 2 ||
		stats.FullDrains != 1 || stats.MultiLineReads != 1 ||
		stats.MaxLinesPerBatch != 2 {
		t.Fatalf("unexpected batching stats: %+v", stats)
	}
}

func TestDRAMBatchResponseIsSplitIntoOriginalCacheLines(t *testing.T) {
	config := DRAMBatchConfig{Enabled: true}
	cacheModule, sender, lowModule, remoteSource :=
		newDRAMBatchTestCache(t, config)
	pid := vm.PID(7)
	first := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, pid, 0x2000)
	second := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, pid, 0x2040)
	cacheModule.enqueueDRAMBatchMiss(1e-9, first)
	cacheModule.enqueueDRAMBatchMiss(2e-9, second)
	read := sender.messages[0].(*mem.ReadReq)

	data := make([]byte, 128)
	for i := 0; i < 64; i++ {
		data[i] = 0x11
		data[64+i] = 0x22
	}
	rsp := mem.DataReadyRspBuilder{}.
		WithSendTime(20e-9).
		WithSrc(lowModule).
		WithDst(cacheModule.bottomPort).
		WithRspTo(read.ID).
		WithData(data).
		Build()
	if err := cacheModule.bottomPort.Recv(rsp); err != nil {
		t.Fatal("could not inject the 128-byte DRAM response")
	}

	if !cacheModule.writeBuffer.processReturnRsp(30e-9) {
		t.Fatal("combined DRAM response did not complete")
	}
	if len(first.fetchedData) != 64 || first.fetchedData[0] != 0x11 ||
		len(second.fetchedData) != 64 || second.fetchedData[0] != 0x22 {
		t.Fatalf("response split incorrectly: first=%v second=%v",
			first.fetchedData, second.fetchedData)
	}
	if cacheModule.mshr.Query(pid, 0x2000) != nil ||
		cacheModule.mshr.Query(pid, 0x2040) != nil {
		t.Fatal("one or more original MSHR transactions were not completed")
	}
	if cacheModule.writeBufferToBankBuffers[0].Size() != 2 {
		t.Fatal("both original cache-line fills were not sent to the bank")
	}
	first.fetchedData[0] = 0xff
	if data[0] != 0x11 || second.fetchedData[0] != 0x22 {
		t.Fatal("split cache lines alias each other or the 128-byte response")
	}
}

func TestDRAMBatchZeroWaitDrainsImmediately(t *testing.T) {
	config := DRAMBatchConfig{
		Enabled:     true,
		MaxEntries:  4,
		MaxLines:    2,
		MaxWaitNS:   0,
		WindowLines: 2,
	}
	cacheModule, sender, _, remoteSource :=
		newDRAMBatchTestCache(t, config)
	trans := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, vm.PID(1), 0x2800)
	cacheModule.enqueueDRAMBatchMiss(10e-9, trans)

	if !cacheModule.processDRAMBatches(
		10e-9, false, dramBatchDrainManual) {
		t.Fatal("MaxWaitNS=0 did not immediately drain the batch")
	}
	if len(sender.messages) != 1 ||
		cacheModule.GetDRAMBatchStats().TimeoutDrains != 1 {
		t.Fatalf("immediate drain was not issued and recorded: %+v",
			cacheModule.GetDRAMBatchStats())
	}
}

func TestDRAMAdapterLearnsAndImmediatelyPrefetches(t *testing.T) {
	config := DRAMBatchConfig{Enabled: true, MaxEntries: 4}
	cacheModule, sender, _, remoteSource :=
		newDRAMBatchTestCache(t, config)
	pid := vm.PID(1)

	addresses := []uint64{0x1000, 0x1040, 0x1080, 0x10c0}
	for i, address := range addresses {
		trans := newDRAMBatchTestTransaction(
			cacheModule, remoteSource, pid, address)
		cacheModule.writeBufferBuffer.Push(trans)
		if !cacheModule.writeBuffer.processAdaptiveDRAMFetch(
			sim.VTimeInSec(i+1)*1e-9, trans,
		) {
			t.Fatalf("training miss 0x%x was not issued", address)
		}
	}
	if len(sender.messages) != 4 {
		t.Fatalf("training sent %d reads, want 4", len(sender.messages))
	}
	if cacheModule.dramAdapter.confidence < dramAdapterPredictionThreshold {
		t.Fatalf("adapter did not learn adjacent accesses: %+v",
			cacheModule.GetDRAMBatchStats())
	}

	first := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, pid, 0x1100)
	cacheModule.writeBufferBuffer.Push(first)
	if !cacheModule.writeBuffer.processAdaptiveDRAMFetch(5e-9, first) {
		t.Fatal("predicted miss was not issued")
	}
	read := sender.messages[len(sender.messages)-1].(*mem.ReadReq)
	if read.Address != 0x1100 || read.AccessByteSize != 128 {
		t.Fatalf("adaptive read is [0x%x, %dB], want [0x1100, 128B]",
			read.Address, read.AccessByteSize)
	}

	second := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, pid, 0x1140)
	cacheModule.writeBufferBuffer.Push(second)
	if !cacheModule.writeBuffer.processAdaptiveDRAMFetch(6e-9, second) {
		t.Fatal("sibling miss did not join the predicted request")
	}
	if len(sender.messages) != 5 || second.fetchReadReq != read {
		t.Fatal("sibling generated a duplicate DRAM request")
	}
	stats := cacheModule.GetDRAMBatchStats()
	if stats.AdapterPredictions != 1 || stats.AdapterInflightHits != 1 {
		t.Fatalf("unexpected adapter stats: %+v", stats)
	}
}

func TestDRAMBatchSeparatesPIDAndLowModule(t *testing.T) {
	config := DRAMBatchConfig{
		Enabled:     true,
		MaxEntries:  8,
		MaxLines:    2,
		MaxWaitNS:   25,
		WindowLines: 2,
	}
	cacheModule, sender, firstLowModule, remoteSource :=
		newDRAMBatchTestCache(t, config)
	secondLowModule := sim.NewLimitNumMsgPort(
		cacheModule, 4, "DRAM2.Top")

	firstPID := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, vm.PID(1), 0x5000)
	secondPID := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, vm.PID(2), 0x5040)
	cacheModule.enqueueDRAMBatchMiss(1e-9, firstPID)
	cacheModule.enqueueDRAMBatchMiss(2e-9, secondPID)
	if len(cacheModule.dramBatchOrder) != 2 || len(sender.messages) != 0 {
		t.Fatal("adjacent lines from different PIDs were combined")
	}

	cacheModule.writeBuffer.Reset(3e-9)
	cacheModule.SetLowModuleFinder(&dramBatchAddressFinder{
		lowAddressModule:  firstLowModule,
		highAddressModule: secondLowModule,
	})
	firstModule := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, vm.PID(3), 0x6000)
	secondModule := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, vm.PID(3), 0x6040)
	cacheModule.enqueueDRAMBatchMiss(4e-9, firstModule)
	cacheModule.enqueueDRAMBatchMiss(5e-9, secondModule)
	if len(cacheModule.dramBatchOrder) != 2 || len(sender.messages) != 0 {
		t.Fatal("adjacent lines targeting different low modules were combined")
	}
}

func TestDRAMBatchTimeoutFallsBackToSingleLine(t *testing.T) {
	config := DRAMBatchConfig{
		Enabled:     true,
		MaxEntries:  4,
		MaxLines:    2,
		MaxWaitNS:   25,
		WindowLines: 2,
	}
	cacheModule, sender, _, remoteSource :=
		newDRAMBatchTestCache(t, config)
	trans := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, vm.PID(1), 0x3000)
	cacheModule.enqueueDRAMBatchMiss(10e-9, trans)

	if !cacheModule.processDRAMBatches(
		40e-9, false, dramBatchDrainManual) {
		t.Fatal("expired singleton batch was not drained")
	}
	read := sender.messages[0].(*mem.ReadReq)
	if read.Address != 0x3000 || read.AccessByteSize != 64 {
		t.Fatalf("fallback read is [0x%x, %dB], want [0x3000, 64B]",
			read.Address, read.AccessByteSize)
	}

	stats := cacheModule.GetDRAMBatchStats()
	if stats.TimeoutDrains != 1 || stats.SingletonFallbacks != 1 ||
		stats.SingleLineReads != 1 || stats.WaitSamples != 1 {
		t.Fatalf("unexpected singleton stats: %+v", stats)
	}
}

func TestDRAMBatchCapacityDrainAndReset(t *testing.T) {
	config := DRAMBatchConfig{
		Enabled:     true,
		MaxEntries:  1,
		MaxLines:    2,
		MaxWaitNS:   100,
		WindowLines: 2,
	}
	cacheModule, sender, _, remoteSource :=
		newDRAMBatchTestCache(t, config)
	first := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, vm.PID(1), 0x4000)
	second := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, vm.PID(1), 0x4080)
	cacheModule.enqueueDRAMBatchMiss(1e-9, first)

	if !cacheModule.enqueueDRAMBatchMiss(2e-9, second) {
		t.Fatal("capacity drain did not make room for a new batch")
	}
	if len(sender.messages) != 1 ||
		cacheModule.GetDRAMBatchStats().CapacityDrains != 1 {
		t.Fatalf("capacity drain was not recorded: %+v",
			cacheModule.GetDRAMBatchStats())
	}
	if !cacheModule.dramBatchHasEntries() {
		t.Fatal("new batch disappeared after capacity drain")
	}

	cacheModule.writeBuffer.Reset(3e-9)
	if cacheModule.dramBatchHasEntries() {
		t.Fatal("pending DRAM batches survived a cache reset")
	}
}

func TestDRAMBatchDoesNotBlockUnrelatedEviction(t *testing.T) {
	config := DRAMBatchConfig{
		Enabled:     true,
		MaxEntries:  4,
		MaxLines:    2,
		MaxWaitNS:   25,
		WindowLines: 2,
	}
	cacheModule, sender, _, remoteSource :=
		newDRAMBatchTestCache(t, config)
	pid := vm.PID(1)
	pendingRead := newDRAMBatchTestTransaction(
		cacheModule, remoteSource, pid, 0x2000)
	cacheModule.enqueueDRAMBatchMiss(1e-9, pendingRead)

	eviction := &transaction{
		action:            writeBufferEvictAndWrite,
		block:             &cache.Block{},
		evictingPID:       pid,
		evictingAddr:      0x2080,
		evictingData:      make([]byte, 64),
		evictingDirtyMask: make([]bool, 64),
	}
	cacheModule.writeBufferBuffer.Push(eviction)

	if !cacheModule.writeBuffer.processNewTransaction(2e-9) {
		t.Fatal("unrelated eviction was blocked by a pending DRAM batch")
	}
	if len(sender.messages) != 0 {
		t.Fatal("unrelated eviction forced the pending DRAM batch to drain")
	}
	if !cacheModule.dramBatchHasEntries() {
		t.Fatal("pending DRAM batch disappeared after unrelated eviction")
	}
	if cacheModule.writeBufferBuffer.Peek() != nil {
		t.Fatal("unrelated eviction was not removed from the input buffer")
	}
	if len(cacheModule.writeBuffer.pendingEvictions) != 1 ||
		cacheModule.writeBuffer.pendingEvictions[0] != eviction {
		t.Fatal("unrelated eviction did not enter the write buffer")
	}
}
