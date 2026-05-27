package emu

import (
	"log"
	//	"reflect"
	//    "fmt"
	"encoding/binary"
	//"github.com/rs/xid"
	"github.com/sarchlab/akita/v3/sim"
	//	"github.com/sarchlab/akita/v3/tracing"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	//"github.com/sarchlab/mgpusim/v3/
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
	"github.com/sarchlab/mgpusim/v3/profiler"
	"github.com/sarchlab/mgpusim/v3/protocol"
	//	"github.com/sarchlab/mgpusim/v3/timing/wavefront"
	//	"github.com/sarchlab/mgpusim/v3/virtualdevice/virtualcu"
	"github.com/sarchlab/mgpusim/v3/samples/sampledrunner"
	//"fmt"
)

type SampledComputeUnit struct {
	freq               sim.Freq
	decoder            Decoder
	scratchpadPreparer ScratchpadPreparer
	alu                ALU
	storageAccessor    *storageAccessor
	wfs                map[*kernels.WorkGroup][]*Wavefront
	LDSStorage         []byte
	GlobalMemStorage   *mem.Storage
	//for debugging
	bbvset map[profiler.BBL]uint32
}

func (cu *SampledComputeUnit) runWfUntilBarrier(
	wf *Wavefront,
	inited bool,
	branchEngine *sampledrunner.BranchSampledEngine,
) sim.VTimeInSec {
	if inited {
		wf.start_pc = wf.PC
		wf.last_bbl_pc = 0
		wf.last_ins_num = 0
		wf.current_ins_num = 0
	}
	ret := sim.VTimeInSec(0)
	continue_execute := true
	loopBackedgeCounts := make(map[uint64]int)
	for continue_execute {
		instPC := wf.PC
		instBuf := cu.storageAccessor.Read(wf.PID(), wf.PC, 8)

		inst, _ := cu.decoder.Decode(instBuf)
		inst.PC = instPC
		inswidth := uint64(inst.InstWidth())
		//		wf.inst = inst
		wf.SetInst(inst)
		if wf.is_last_inst_branch {
			bbl := profiler.BBL{
				PC:     wf.last_bbl_pc,
				InsNum: wf.current_ins_num - wf.last_ins_num,
			}
			wf.last_bbl_pc = wf.PC - wf.start_pc
			wf.last_ins_num = wf.current_ins_num
			//            bbl.Print()
			//            fmt.Printf(" next bbl pc : %d \n",wf.last_bbl_pc)
			ret += branchEngine.Predict(wf.UID, bbl)
			wf.is_last_inst_branch = false
		}

		wf.current_ins_num += inswidth
		if inst.FormatType == insts.SOPP {

			switch inst.Opcode {
			case 10:
				wf.is_last_inst_branch = true
				wf.AtBarrier = true
				continue_execute = false
			case 1:
				wf.Completed = true
				continue_execute = false
			case 2, 4, 5, 6, 7, 8, 9:
				wf.is_last_inst_branch = true
			default:
			}
		}
		wf.PC += uint64(inst.ByteSize)
		if continue_execute {
			cu.executeInst(wf)
			completedIters := loopBackedgeCounts[inst.PC] + 1
			if pred, target, fallthroughPC, skippedIters, ok :=
				branchEngine.PredictStableLoop(inst, completedIters); ok && wf.PC == target {
				loopBackedgeCounts[inst.PC] = completedIters + skippedIters
				ret += pred
				sampledrunner.PhotonDebugf(
					"LoopSampledCU",
					"loop sampled fast-forward wfid=%s branchPC=%#x targetPC=%#x fallthroughPC=%#x completedIters=%d skippedIters=%d pred=%.3fns",
					wf.UID,
					inst.PC,
					target,
					fallthroughPC,
					completedIters,
					skippedIters,
					pred*1e9)
				wf.PC = fallthroughPC
			} else if ok && wf.PC != target {
				sampledrunner.PhotonVerbosef(
					"LoopSampledCU",
					"loop sampled prediction ignored wfid=%s branchPC=%#x targetPC=%#x actualPC=%#x",
					wf.UID,
					inst.PC,
					target,
					wf.PC)
			}
		}
	}
	if wf.Completed { //process ending bbl
		bbl := profiler.BBL{
			PC:     wf.last_bbl_pc,
			InsNum: wf.current_ins_num - wf.last_ins_num,
		}

		ret += branchEngine.Predict(wf.UID, bbl)

	} else if wf.AtBarrier {

	}
	//fmt.Printf("%d %s\n",wf.PC,wf.UID)
	return ret
}
func (cu *SampledComputeUnit) executeInst(wf *Wavefront) {
	cu.scratchpadPreparer.Prepare(wf, wf)
	cu.alu.Run(wf)
	cu.scratchpadPreparer.Commit(wf, wf)
}
func (cu *SampledComputeUnit) initWfs(
	wg *kernels.WorkGroup,
	req *protocol.MapWGReq,
) error {
	lds := cu.initLDS(wg, req)

	for _, wf := range wg.Wavefronts {
		managedWf := NewWavefront(wf)
		managedWf.LDS = lds
		managedWf.SetPID(req.PID)
		cu.wfs[wg] = append(cu.wfs[wg], managedWf)
	}

	for _, managedWf := range cu.wfs[wg] {
		cu.initWfRegs(managedWf)
	}

	return nil
}

func (cu *SampledComputeUnit) initLDS(wg *kernels.WorkGroup, req *protocol.MapWGReq) []byte {
	ldsSize := req.WorkGroup.Packet.GroupSegmentSize
	lds := make([]byte, ldsSize)
	return lds
}
func (cu *SampledComputeUnit) initWfRegs(wf *Wavefront) {
	co := wf.CodeObject
	pkt := wf.Packet

	wf.PC = pkt.KernelObject + co.KernelCodeEntryByteOffset
	//    log.Printf("%d\n",wf.PC)
	wf.Exec = wf.InitExecMask

	SGPRPtr := 0
	if co.EnableSgprPrivateSegmentBuffer() {
		// log.Printf("EnableSgprPrivateSegmentBuffer is not supported")
		//fmt.Printf("s%d SGPRPrivateSegmentBuffer\n", SGPRPtr/4)
		SGPRPtr += 16
	}

	if co.EnableSgprDispatchPtr() {
		binary.LittleEndian.PutUint64(wf.SRegFile[SGPRPtr:SGPRPtr+8], wf.PacketAddress)
		//fmt.Printf("s%d SGPRDispatchPtr\n", SGPRPtr/4)
		SGPRPtr += 8
	}

	if co.EnableSgprQueuePtr() {
		log.Printf("EnableSgprQueuePtr is not supported")
		//fmt.Printf("s%d SGPRQueuePtr\n", SGPRPtr/4)
		SGPRPtr += 8
	}

	if co.EnableSgprKernelArgSegmentPtr() {
		binary.LittleEndian.PutUint64(wf.SRegFile[SGPRPtr:SGPRPtr+8], pkt.KernargAddress)
		//fmt.Printf("s%d SGPRKernelArgSegmentPtr\n", SGPRPtr/4)
		SGPRPtr += 8
	}

	if co.EnableSgprDispatchID() {
		log.Printf("EnableSgprDispatchID is not supported")
		//fmt.Printf("s%d SGPRDispatchID\n", SGPRPtr/4)
		SGPRPtr += 8
	}

	if co.EnableSgprFlatScratchInit() {
		log.Printf("EnableSgprFlatScratchInit is not supported")
		//fmt.Printf("s%d SGPRFlatScratchInit\n", SGPRPtr/4)
		SGPRPtr += 8
	}

	if co.EnableSgprPrivateSegementSize() {
		log.Printf("EnableSgprPrivateSegmentSize is not supported")
		//fmt.Printf("s%d SGPRPrivateSegmentSize\n", SGPRPtr/4)
		SGPRPtr += 4
	}

	if co.EnableSgprGridWorkGroupCountX() {
		binary.LittleEndian.PutUint32(wf.SRegFile[SGPRPtr:SGPRPtr+4],
			(pkt.GridSizeX+uint32(pkt.WorkgroupSizeX)-1)/uint32(pkt.WorkgroupSizeX))
		//fmt.Printf("s%d WorkGroupCountX\n", SGPRPtr/4)
		SGPRPtr += 4
	}

	if co.EnableSgprGridWorkGroupCountY() {
		binary.LittleEndian.PutUint32(wf.SRegFile[SGPRPtr:SGPRPtr+4],
			(pkt.GridSizeY+uint32(pkt.WorkgroupSizeY)-1)/uint32(pkt.WorkgroupSizeY))
		//fmt.Printf("s%d WorkGroupCountY\n", SGPRPtr/4)
		SGPRPtr += 4
	}

	if co.EnableSgprGridWorkGroupCountZ() {
		binary.LittleEndian.PutUint32(wf.SRegFile[SGPRPtr:SGPRPtr+4],
			(pkt.GridSizeZ+uint32(pkt.WorkgroupSizeZ)-1)/uint32(pkt.WorkgroupSizeZ))
		//fmt.Printf("s%d WorkGroupCountZ\n", SGPRPtr/4)
		SGPRPtr += 4
	}

	if co.EnableSgprWorkGroupIDX() {
		binary.LittleEndian.PutUint32(wf.SRegFile[SGPRPtr:SGPRPtr+4],
			uint32(wf.WG.IDX))
		//fmt.Printf("s%d WorkGroupIdX\n", SGPRPtr/4)
		SGPRPtr += 4
	}

	if co.EnableSgprWorkGroupIDY() {
		binary.LittleEndian.PutUint32(wf.SRegFile[SGPRPtr:SGPRPtr+4],
			uint32(wf.WG.IDY))
		//fmt.Printf("s%d WorkGroupIdY\n", SGPRPtr/4)
		SGPRPtr += 4
	}

	if co.EnableSgprWorkGroupIDZ() {
		binary.LittleEndian.PutUint32(wf.SRegFile[SGPRPtr:SGPRPtr+4],
			uint32(wf.WG.IDZ))
		//fmt.Printf("s%d WorkGroupIdZ\n", SGPRPtr/4)
		SGPRPtr += 4
	}

	if co.EnableSgprWorkGroupInfo() {
		log.Printf("EnableSgprPrivateSegmentSize is not supported")
		SGPRPtr += 4
	}

	if co.EnableSgprPrivateSegmentWaveByteOffset() {
		log.Printf("EnableSgprPrivateSegentWaveByteOffset is not supported")
		SGPRPtr += 4
	}

	var x, y, z int
	for i := wf.FirstWiFlatID; i < wf.FirstWiFlatID+64; i++ {
		z = i / (wf.WG.SizeX * wf.WG.SizeY)
		y = i % (wf.WG.SizeX * wf.WG.SizeY) / wf.WG.SizeX
		x = i % (wf.WG.SizeX * wf.WG.SizeY) % wf.WG.SizeX
		laneID := i - wf.FirstWiFlatID

		wf.WriteReg(insts.VReg(0), 1, laneID, insts.Uint32ToBytes(uint32(x)))

		if co.EnableVgprWorkItemID() > 0 {
			wf.WriteReg(insts.VReg(1), 1, laneID, insts.Uint32ToBytes(uint32(y)))
		}

		if co.EnableVgprWorkItemID() > 1 {
			wf.WriteReg(insts.VReg(2), 1, laneID, insts.Uint32ToBytes(uint32(z)))
		}
	}
}

func (cu *SampledComputeUnit) isAllWfCompleted(wg *kernels.WorkGroup) bool {
	for _, wf := range cu.wfs[wg] {
		if !wf.Completed {
			return false
		}
	}
	return true
}

func (cu *SampledComputeUnit) resolveBarrier(wg *kernels.WorkGroup) {
	if cu.isAllWfCompleted(wg) {
		return
	}

	for _, wf := range cu.wfs[wg] {
		if !wf.AtBarrier {
			log.Panic("not all wavefronts at barrier")
		}
		wf.AtBarrier = false
	}
}

func (cu *SampledComputeUnit) RunWG(
	req *protocol.MapWGReq,
	now sim.VTimeInSec,
) []sim.VTimeInSec {
	return cu.RunWGWithBranchEngine(
		req, now, sampledrunner.Branchsampledengine)
}

func (cu *SampledComputeUnit) RunWGWithBranchEngine(
	req *protocol.MapWGReq,
	now sim.VTimeInSec,
	branchEngine *sampledrunner.BranchSampledEngine,
) []sim.VTimeInSec {
	if branchEngine == nil {
		if *sampledrunner.LoopSampledFlag {
			sampledrunner.PhotonDebugf(
				"LoopSampledCU",
				"loop sampled requested but branch engine is nil")
		}
		return make([]sim.VTimeInSec, len(req.Wavefronts))
	}

	wg := req.WorkGroup
	cu.initWfs(wg, req)
	wfs := cu.wfs[wg]
	//    wfs := wg.Wavefronts
	wfstime := make([]sim.VTimeInSec, len(wfs))
	beginandendtime := branchEngine.StartTime() + branchEngine.EndTime()
	if *sampledrunner.LoopSampledFlag {
		sampledrunner.PhotonDebugf(
			"LoopSampledCU",
			"run sampled compute unit with loop sampling wg=%d wfCount=%d basePred=%.3fns",
			req.WorkGroup.IDX,
			len(wfs),
			beginandendtime*1e9)
	}
	//    beginandendtime := sim.VTimeInSec(0)
	for i, _ := range wfstime {
		//        wfstime[i] = sim.VTimeInSec( cu.freq.NextTick(sim.VTimeInSec(0) ))
		wfstime[i] = beginandendtime
	}
	for _, wf := range wfs {
		wf.is_last_inst_branch = false

	}
	inited := true
	for !cu.isAllWfCompleted(wg) {
		for i, wf := range wfs {
			cu.alu.SetLDS(wf.LDS)
			predicttime := cu.runWfUntilBarrier(wf, inited, branchEngine)
			wfstime[i] += predicttime
		}
		inited = false
		cu.resolveBarrier(wg)
	}

	delete(cu.wfs, wg)
	return wfstime
}

// NewComputeUnit creates a new ComputeUnit with the given name
func NewSampledComputeUnit(
	name string,
	freq sim.Freq,
	decoder Decoder,
	scratchpadPreparer ScratchpadPreparer,
	alu ALU,
	sAccessor *storageAccessor,
) *SampledComputeUnit {
	cu := new(SampledComputeUnit)
	cu.freq = freq

	cu.decoder = decoder
	cu.scratchpadPreparer = scratchpadPreparer
	cu.alu = alu
	cu.storageAccessor = sAccessor

	cu.wfs = make(map[*kernels.WorkGroup][]*Wavefront)

	cu.bbvset = make(map[profiler.BBL]uint32)

	return cu
}

func BuildSampledComputeUnit(
	name string,
	freq sim.Freq,
	decoder Decoder,
	pageTable vm.PageTable,
	log2PageSize uint64,
	storage *mem.Storage,
	addrConverter mem.AddressConverter,
) *SampledComputeUnit {
	name += "-sampled"
	scratchpadPreparer := NewScratchpadPreparerImpl()
	sAccessor := newStorageAccessor(
		storage, pageTable, log2PageSize, addrConverter)
	alu := NewALU(sAccessor)
	cu := NewSampledComputeUnit(name, freq, decoder,
		scratchpadPreparer, alu, sAccessor)
	return cu
}

var Sampledcomputeunit *SampledComputeUnit
var Sampledcomputeunits map[uint64]*SampledComputeUnit

func SampledComputeUnitForGPU(gpuID uint64) *SampledComputeUnit {
	if Sampledcomputeunits != nil {
		if cu := Sampledcomputeunits[gpuID]; cu != nil {
			return cu
		}
	}
	return Sampledcomputeunit
}

func CreateSampledComputeUnitForGPU(
	gpuID uint64,
	name string,
	freq sim.Freq,
	decoder Decoder,
	pageTable vm.PageTable,
	log2PageSize uint64,
	storage *mem.Storage,
	addrConverter mem.AddressConverter,
) {
	if Sampledcomputeunits == nil {
		Sampledcomputeunits = make(map[uint64]*SampledComputeUnit)
	}
	Sampledcomputeunits[gpuID] = BuildSampledComputeUnit(
		name,
		freq,
		decoder,
		pageTable,
		log2PageSize,
		storage,
		addrConverter,
	)
}

func CreateUniqSampledComputeUnit(
	name string,
	freq sim.Freq,
	decoder Decoder,
	pageTable vm.PageTable,
	log2PageSize uint64,
	storage *mem.Storage,
	addrConverter mem.AddressConverter,
) {
	Sampledcomputeunit = BuildSampledComputeUnit(
		name,
		freq,
		decoder,
		pageTable,
		log2PageSize,
		storage,
		addrConverter,
	)
}
