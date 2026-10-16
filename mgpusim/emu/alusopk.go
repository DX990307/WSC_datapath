package emu

import (
	"log"
)

func (u *ALUImpl) runSOPK(state InstEmuState) {
	inst := state.Inst()
	switch inst.Opcode {
	case 0:
		u.runSMOVKI32(state)
	case 2:
		u.runSCMPKEQI32(state)
	case 3:
		u.runSCMPKLGI32(state)
	case 14:
		u.runSADDKI32(state)
	case 15:
		u.runSMULKI32(state)
	default:
		log.Panicf("Opcode %d for SOPK format is not implemented", inst.Opcode)
	}
}

func (u *ALUImpl) runSMOVKI32(state InstEmuState) {
	sp := state.Scratchpad().AsSOPK()
	imm := asInt16(uint16(sp.IMM & 0xffff))
	sp.DST = uint64(imm)
}

func (u *ALUImpl) runSCMPKLGI32(state InstEmuState) {
	sp := state.Scratchpad().AsSOPK()
	imm := asInt16(uint16(sp.IMM & 0xffff))
	if asInt16(uint16(sp.DST)) != imm {
		sp.SCC = 1
	} else {
		sp.SCC = 0
	}
}

// runSCMPKEQI32 implements S_CMPK_EQ_I32 (GCN3 ISA reference, ch. 12, "SOPK
// Instructions", opcode 2): SCC = (D.i == signext(SIMM16)).
func (u *ALUImpl) runSCMPKEQI32(state InstEmuState) {
	sp := state.Scratchpad().AsSOPK()
	imm := int32(asInt16(uint16(sp.IMM & 0xffff)))
	if asInt32(uint32(sp.DST)) == imm {
		sp.SCC = 1
	} else {
		sp.SCC = 0
	}
}

// runSADDKI32 implements S_ADDK_I32 (GCN3 ISA reference, ch. 12, "SOPK
// Instructions", opcode 14): D.i = D.i + signext(SIMM16); SCC = signed
// overflow.
func (u *ALUImpl) runSADDKI32(state InstEmuState) {
	sp := state.Scratchpad().AsSOPK()
	imm := int32(asInt16(uint16(sp.IMM & 0xffff)))
	dst := asInt32(uint32(sp.DST))
	sum := dst + imm

	sp.DST = uint64(uint32(sum))
	if (dst >= 0) == (imm >= 0) && (sum >= 0) != (dst >= 0) {
		sp.SCC = 1
	} else {
		sp.SCC = 0
	}
}

func (u *ALUImpl) runSMULKI32(state InstEmuState) {
	sp := state.Scratchpad().AsSOPK()
	imm := asInt16(uint16(sp.IMM & 0xffff))
	dst := asInt32(uint32(sp.DST))

	sp.DST = int64ToBits(int64(int32(imm) * dst))
}
