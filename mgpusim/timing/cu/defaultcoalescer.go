package cu

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/timing/wavefront"
)

type defaultCoalescer struct {
	log2CacheLineSize uint64
}

func (c defaultCoalescer) generateMemTransactions(
	wf *wavefront.Wavefront,
) []VectorMemAccessInfo {
	c.mustBeAFlatLoadOrStore(wf)
	var transactions []VectorMemAccessInfo
	if c.isLoadInst(wf.Inst()) {
		reqs := c.generateReadReqs(wf)
		transactions = c.generateReadTransactions(wf, reqs)
	} else {
		reqs := c.generateWriteReqs(wf)
		transactions = c.generateWriteTransactions(wf, reqs)
	}
	return transactions
}

func (c defaultCoalescer) mustBeAFlatLoadOrStore(
	wf *wavefront.Wavefront,
) {
	if wf.Inst().FormatType != insts.FLAT {
		panic("must be a flat instruction")
	}

	if wf.Inst().Opcode < 16 || wf.Inst().Opcode > 31 {
		panic("must be a load or store instruction")
	}
}

// func (c defaultCoalescer) executionMaskMustNotBeAllZero(
// 	wf *wavefront.Wavefront,
// ) {
// 	sp := wf.Scratchpad().AsFlat()
// 	exec := sp.EXEC
// 	if exec == 0 {
// 		panic("execution mask is all 0")
// 	}
// }

func (c defaultCoalescer) generateReadReqs(
	wf *wavefront.Wavefront,
) []*mem.ReadReq {
	sp := wf.Scratchpad().AsFlat()
	exec := sp.EXEC
	addrs := sp.ADDR
	reqs := []*mem.ReadReq{}
	regCount := c.instRegCount(wf.Inst())

	for i := uint(0); i < 64; i++ {
		if !laneMasked(exec, i) {
			continue
		}

		addr := addrs[i]
		for j := 0; j < regCount; j++ {
			c.findOrCreateReadReq(&reqs, addr+uint64(4*j))
		}
	}

	return reqs
}

func (c defaultCoalescer) generateWriteReqs(
	wf *wavefront.Wavefront,
) []*mem.WriteReq {
	sp := wf.Scratchpad().AsFlat()
	exec := sp.EXEC
	addrs := sp.ADDR
	reqs := []*mem.WriteReq{}
	data := sp.DATA

	for i := uint(0); i < 64; i++ {
		if !laneMasked(exec, i) {
			continue
		}

		addr := addrs[i]
		regCount := uint(c.instRegCount(wf.Inst()))
		for j := uint(0); j < regCount; j++ {
			reqData := data[i*4+j]
			c.findOrCreateWriteReq(&reqs, addr+uint64(j*4),
				insts.Uint32ToBytes(reqData))
		}
	}

	return reqs
}

func (c defaultCoalescer) generateReadTransactions(
	wf *wavefront.Wavefront,
	reqs []*mem.ReadReq,
) []VectorMemAccessInfo {
	transactions := []VectorMemAccessInfo{}
	markLocalPairFollowers(reqs, c.log2CacheLineSize)
	for position, req := range reqs {
		// Keep cacheline positions of one coalesced load separate after they
		// converge at the shared L2. Treating every line emitted by one dynamic
		// vector instruction as a temporal stream merely learns the already
		// visible within-instruction footprint. Position streams instead learn
		// how the corresponding line moves across dynamic executions.
		if inst := wf.DynamicInst(); inst != nil && inst.Inst != nil {
			req.StreamID = memoryStreamID(inst.PC, position)
			req.LocalStreamID = localMemoryStreamID(wf, inst.PC, position)
		}
		transaction := VectorMemAccessInfo{
			Read:      req,
			Wavefront: wf,
			Inst:      wf.DynamicInst(),
		}

		c.addLaneInfo(&transaction, wf)

		transactions = append(transactions, transaction)
	}
	return transactions
}

// markLocalPairFollowers marks only the later-emitted member of each adjacent
// cacheline pair. The hint suppresses pointless M1 Filter probes; exact
// Local-Pending state remains authoritative at L2.
func markLocalPairFollowers(reqs []*mem.ReadReq, log2LineSize uint64) {
	if log2LineSize >= 64 {
		return
	}
	lineBytes := uint64(1) << log2LineSize
	seen := make(map[uint64]struct{}, len(reqs))
	for _, req := range reqs {
		if req == nil {
			continue
		}
		line := req.Address & ^(lineBytes - 1)
		sibling := line ^ lineBytes
		if _, ok := seen[sibling]; ok {
			req.LocalPairHint = true
		}
		seen[line] = struct{}{}
	}
}

func memoryStreamID(pc uint64, position int) uint64 {
	context := mixMemoryStreamWord(
		uint64(position) + 0x9e3779b97f4a7c15)
	return pc ^ context
}

func mixMemoryStreamWord(context uint64) uint64 {
	context ^= context >> 30
	context *= 0xbf58476d1ce4e5b9
	context ^= context >> 27
	context *= 0x94d049bb133111eb
	context ^= context >> 31
	return context
}

// localMemoryStreamID groups all cache lines emitted by one static load into
// one local-M1 footprint stream. Position remains part of ordinary StreamID
// for the frozen requester-RDMA predictor, but including it here makes M1
// learn how each position moves across workgroups rather than the adjacent
// lines simultaneously exposed by one coalesced instruction. Source L1 is an
// independent predictor-key field, so PC alone provides a bounded local
// context without workgroup or hardware-slot identity.
func localMemoryStreamID(
	_ *wavefront.Wavefront,
	pc uint64,
	_ int,
) uint64 {
	if pc != 0 {
		return pc
	}
	// Preserve a nonzero local context for synthetic instructions at PC zero.
	return mixMemoryStreamWord(0x243f6a8885a308d3)
}

func (c defaultCoalescer) generateWriteTransactions(
	wf *wavefront.Wavefront,
	reqs []*mem.WriteReq,
) []VectorMemAccessInfo {
	transactions := []VectorMemAccessInfo{}
	for _, req := range reqs {
		transaction := VectorMemAccessInfo{
			Write:     req,
			Wavefront: wf,
			Inst:      wf.DynamicInst(),
		}

		transactions = append(transactions, transaction)
	}
	return transactions
}

func (c defaultCoalescer) findOrCreateReadReq(
	reqs *[]*mem.ReadReq,
	addr uint64,
) *mem.ReadReq {
	for _, req := range *reqs {
		if c.isInSameCacheLine(addr, req.Address) {
			return req
		}
	}

	req := mem.ReadReqBuilder{}.
		WithAddress(c.cacheLineID(addr)).
		WithByteSize(1 << c.log2CacheLineSize).
		Build()
	*reqs = append(*reqs, req)
	return req
}

func (c defaultCoalescer) findOrCreateWriteReq(
	reqs *[]*mem.WriteReq,
	addr uint64,
	data []byte,
) *mem.WriteReq {
	for _, req := range *reqs {
		if c.isInSameCacheLine(addr, req.Address) {
			c.mergeDataWithReq(req, addr, data)
			return req
		}
	}

	req := mem.WriteReqBuilder{}.
		WithAddress(c.cacheLineID(addr)).
		WithData(make([]byte, 1<<c.log2CacheLineSize)).
		WithDirtyMask(make([]bool, 1<<c.log2CacheLineSize)).
		Build()
	c.mergeDataWithReq(req, addr, data)
	*reqs = append(*reqs, req)
	return req
}

func (c defaultCoalescer) mergeDataWithReq(
	req *mem.WriteReq,
	addr uint64,
	data []byte,
) {
	c.addressRangeMustFallInReq(req, addr, data)

	offset := c.addrOffsetInCacheLine(addr)

	for i, b := range data {
		req.Data[int(offset)+i] = b
		req.DirtyMask[int(offset)+i] = true
	}
}

func (c defaultCoalescer) addressRangeMustFallInReq(
	req *mem.WriteReq,
	addr uint64,
	data []byte,
) {
	if addr < req.Address {
		panic("addr < req.Address")
	}

	if addr+uint64(len(data)) > req.Address+uint64(len(req.Data)) {
		panic("req cannot hold data")
	}
}

func (c defaultCoalescer) addLaneInfo(
	transaction *VectorMemAccessInfo,
	wf *wavefront.Wavefront,
) {
	sp := wf.Scratchpad().AsFlat()
	exec := sp.EXEC
	addrs := sp.ADDR
	req := transaction.Read
	regCount := c.instRegCount(wf.Inst())

	for i := uint(0); i < 64; i++ {
		if !laneMasked(exec, i) {
			continue
		}

		for j := 0; j < regCount; j++ {
			addr := addrs[i] + uint64(j*4)
			reg := insts.VReg(wf.Inst().Dst.Register.RegIndex() + j)
			if c.isInSameCacheLine(addr, req.Address) {
				laneInfo := vectorMemAccessLaneInfo{
					laneID:                int(i),
					reg:                   reg,
					regCount:              1,
					addrOffsetInCacheLine: c.addrOffsetInCacheLine(addr),
				}
				transaction.laneInfo = append(transaction.laneInfo, laneInfo)
			}
		}
	}
}

func (c defaultCoalescer) isInSameCacheLine(addr1, addr2 uint64) bool {
	return c.cacheLineID(addr1) == c.cacheLineID(addr2)
}

func (c defaultCoalescer) cacheLineID(addr uint64) uint64 {
	return addr >> c.log2CacheLineSize << c.log2CacheLineSize
}

func (c defaultCoalescer) addrOffsetInCacheLine(addr uint64) uint64 {
	return addr & ((1 << c.log2CacheLineSize) - 1)
}

func (c defaultCoalescer) isLoadInst(inst *insts.Inst) bool {
	return inst.Opcode >= 6 && inst.Opcode <= 23
}

func (c defaultCoalescer) instRegCount(inst *insts.Inst) int {
	switch inst.Opcode {
	case 16, 17, 18, 19, 20:
		return 1
	case 24, 25, 26, 27, 28:
		return 1
	case 21, 29:
		return 2
	case 22, 30:
		return 3
	case 23, 31:
		return 4
	default:
		panic("not supported opcode")
	}
}
