package cu

import (
	"log"

	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/pipelining"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/timing/wavefront"
)

type vectorMemInst struct {
	wavefront *wavefront.Wavefront
}

func (i vectorMemInst) TaskID() string {
	return i.wavefront.DynamicInst().ID
}

// A VectorMemoryUnit is the block in a compute unit that can performs vector
// memory operations.
type VectorMemoryUnit struct {
	cu *ComputeUnit

	scratchpadPreparer ScratchpadPreparer
	coalescer          coalescer

	numInstInFlight         uint64
	numTransactionInFlight  uint64
	maxInstructionsInFlight uint64

	instructionPipeline           pipelining.Pipeline
	postInstructionPipelineBuffer sim.Buffer
	transactionsWaiting           []VectorMemAccessInfo
	transactionPipeline           pipelining.Pipeline
	postTransactionPipelineBuffer sim.Buffer

	isIdle bool
}

// NewVectorMemoryUnit creates a new Vector Memory Unit.
func NewVectorMemoryUnit(
	cu *ComputeUnit,
	scratchpadPreparer ScratchpadPreparer,
	coalescer coalescer,
) *VectorMemoryUnit {
	u := new(VectorMemoryUnit)
	u.cu = cu

	u.scratchpadPreparer = scratchpadPreparer
	u.coalescer = coalescer

	return u
}

// CanAcceptWave checks if the buffer of the read stage is occupied or not
func (u *VectorMemoryUnit) CanAcceptWave() bool {
	return u.instructionPipeline.CanAccept()
}

// AcceptWave moves one wavefront into the read buffer of the Scalar unit
func (u *VectorMemoryUnit) AcceptWave(
	wave *wavefront.Wavefront,
	now sim.VTimeInSec,
) {
	u.instructionPipeline.Accept(now, vectorMemInst{wavefront: wave})
	u.numInstInFlight++
}

// IsIdle moves one wavefront into the read buffer of the Scalar unit
func (u *VectorMemoryUnit) IsIdle() bool {
	u.isIdle = (u.numInstInFlight == 0) && (u.numTransactionInFlight == 0)
	return u.isIdle
}

// Run executes three pipeline stages that are controlled by the
// VectorMemoryUnit
func (u *VectorMemoryUnit) Run(now sim.VTimeInSec) bool {
	madeProgress := false
	madeProgress = u.sendRequest(now) || madeProgress
	madeProgress = u.transactionPipeline.Tick(now) || madeProgress
	madeProgress = u.instToTransaction(now) || madeProgress
	madeProgress = u.instructionPipeline.Tick(now) || madeProgress
	return madeProgress
}

func (u *VectorMemoryUnit) instToTransaction(
	now sim.VTimeInSec,
) bool {
	if len(u.transactionsWaiting) > 0 {
		return u.insertTransactionToPipeline(now)
	}

	return u.execute(now)
}

func (u *VectorMemoryUnit) insertTransactionToPipeline(
	now sim.VTimeInSec,
) bool {
	if !u.transactionPipeline.CanAccept() {
		return false
	}

	u.transactionPipeline.Accept(now, u.transactionsWaiting[0])
	u.transactionsWaiting = u.transactionsWaiting[1:]

	return true
}

func (u *VectorMemoryUnit) execute(now sim.VTimeInSec) (madeProgress bool) {
	item := u.postInstructionPipelineBuffer.Peek()
	if item == nil {
		return false
	}

	wave := item.(vectorMemInst).wavefront
	inst := wave.Inst()
	switch inst.FormatType {
	case insts.FLAT:
		ok := u.executeFlatInsts(now, wave)
		if !ok {
			return false
		}
	default:
		log.Panicf("running inst %s in vector memory unit is not supported", inst.String(nil))
	}

	u.postInstructionPipelineBuffer.Pop()
	u.cu.UpdatePCAndSetReady(wave)
	u.numInstInFlight--

	return true
}

func (u *VectorMemoryUnit) executeFlatInsts(
	now sim.VTimeInSec,
	wavefront *wavefront.Wavefront,
) bool {
	inst := wavefront.DynamicInst()
	switch inst.Opcode {
	case 16, 17, 18, 19, 20, 21, 22, 23: // FLAT_LOAD_BYTE
		return u.executeFlatLoad(now, wavefront)
	case 24, 25, 26, 27, 28, 29, 30, 31:
		return u.executeFlatStore(now, wavefront)
	default:
		log.Panicf("Opcode %d for format FLAT is not supported.", inst.Opcode)
	}

	panic("never")
}

func (u *VectorMemoryUnit) executeFlatLoad(
	now sim.VTimeInSec,
	wave *wavefront.Wavefront,
) bool {
	u.scratchpadPreparer.Prepare(wave, wave)
	transactions := u.coalescer.generateMemTransactions(wave)

	if len(transactions) == 0 {
		u.cu.logInstTask(
			now,
			wave,
			wave.DynamicInst(),
			true,
		)
		return true
	}

	if len(transactions)+len(u.cu.InFlightVectorMemAccess) >
		u.cu.InFlightVectorMemAccessLimit {
		return false
	}

	wave.OutstandingVectorMemAccess++
	wave.OutstandingScalarMemAccess++

	for i, t := range transactions {
		attachWGOriginToVectorRequest(t, wave, int(u.cu.GPUID))
		u.cu.InFlightVectorMemAccess = append(u.cu.InFlightVectorMemAccess, t)
		if i != len(transactions)-1 {
			t.Read.CanWaitForCoalesce = true
		}

		lowModule := u.cu.VectorMemModules.Find(t.Read.Address)
		t.Read.Dst = lowModule
		t.Read.Src = u.cu.ToVectorMem
		t.Read.PID = wave.PID()
		u.transactionsWaiting = append(u.transactionsWaiting, t)
	}

	return true
}

func (u *VectorMemoryUnit) executeFlatStore(
	now sim.VTimeInSec,
	wave *wavefront.Wavefront,
) bool {
	u.scratchpadPreparer.Prepare(wave, wave)
	transactions := u.coalescer.generateMemTransactions(wave)

	if len(transactions) == 0 {
		u.cu.logInstTask(
			now,
			wave,
			wave.DynamicInst(),
			true,
		)
		return true
	}

	if len(transactions)+len(u.cu.InFlightVectorMemAccess) >
		u.cu.InFlightVectorMemAccessLimit {
		return false
	}

	wave.OutstandingVectorMemAccess++
	wave.OutstandingScalarMemAccess++

	for i, t := range transactions {
		attachWGOriginToVectorRequest(t, wave, int(u.cu.GPUID))
		u.cu.InFlightVectorMemAccess = append(u.cu.InFlightVectorMemAccess, t)
		if i != len(transactions)-1 {
			t.Write.CanWaitForCoalesce = true
		}
		lowModule := u.cu.VectorMemModules.Find(t.Write.Address)
		t.Write.Dst = lowModule
		t.Write.Src = u.cu.ToVectorMem
		t.Write.PID = wave.PID()
		u.transactionsWaiting = append(u.transactionsWaiting, t)
	}

	return true
}

func attachWGOriginToVectorRequest(
	transaction VectorMemAccessInfo,
	wave *wavefront.Wavefront,
	requesterGPU int,
) {
	origin, ok := wgOriginForWave(wave, requesterGPU)
	if !ok {
		return
	}
	if transaction.Read != nil {
		transaction.Read.Info = memtrace.WithWGOriginInfo(
			transaction.Read.Info, origin)
	}
	if transaction.Write != nil {
		transaction.Write.Info = memtrace.WithWGOriginInfo(
			transaction.Write.Info, origin)
	}
}

func wgOriginForWave(
	wave *wavefront.Wavefront,
	requesterGPU int,
) (memtrace.WGOriginInfo, bool) {
	if !memtrace.RemoteOriginTraceEnabled() || wave == nil || wave.WG == nil ||
		wave.WG.WorkGroup == nil {
		return memtrace.WGOriginInfo{}, false
	}
	wg := wave.WG.WorkGroup
	flattened := uint64(wg.IDX)
	if packet := wg.Packet; packet != nil && packet.WorkgroupSizeX > 0 &&
		packet.WorkgroupSizeY > 0 {
		numX := (uint64(packet.GridSizeX)-1)/uint64(packet.WorkgroupSizeX) + 1
		numY := (uint64(packet.GridSizeY)-1)/uint64(packet.WorkgroupSizeY) + 1
		flattened = uint64(wg.IDZ)*numX*numY +
			uint64(wg.IDY)*numX + uint64(wg.IDX)
	}
	return memtrace.WGOriginInfo{
		PID:           uint64(wave.PID()),
		RequesterGPU:  requesterGPU,
		FlattenedWGID: flattened,
		WGX:           wg.IDX,
		WGY:           wg.IDY,
		WGZ:           wg.IDZ,
	}, true
}

func (u *VectorMemoryUnit) sendRequest(now sim.VTimeInSec) bool {
	item := u.postTransactionPipelineBuffer.Peek()
	if item == nil {
		return false
	}

	var req sim.Msg
	info := item.(VectorMemAccessInfo)
	if info.Read != nil {
		req = info.Read
	} else {
		req = info.Write
	}

	req.Meta().SendTime = now
	err := u.cu.ToVectorMem.Send(req)
	if err == nil {
		u.postTransactionPipelineBuffer.Pop()
		u.numTransactionInFlight--

		tracing.TraceReqInitiate(req, u.cu, info.Inst.ID)

		return true
	}

	return false
}

// Flush flushes
func (u *VectorMemoryUnit) Flush() {
	u.instructionPipeline.Clear()
	u.transactionPipeline.Clear()
	u.postInstructionPipelineBuffer.Clear()
	u.postTransactionPipelineBuffer.Clear()
	u.transactionsWaiting = nil
	u.numInstInFlight = 0
	u.numTransactionInFlight = 0
}
