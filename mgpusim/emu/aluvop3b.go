package emu

import (
	"log"
	"math"
)

func (u *ALUImpl) runVOP3B(state InstEmuState) {
	inst := state.Inst()

	u.vop3aPreprocess(state)

	switch inst.Opcode {
	case 281:
		u.runVADDU32VOP3b(state)
	case 282:
		u.runVSUBU32VOP3b(state)
	case 283:
		u.runVSUBREVU32VOP3b(state)
	case 284:
		u.runVADDCU32VOP3b(state)
	case 285:
		u.runVSUBBU32VOP3b(state)
	case 286:
		u.runVSUBBREVU32VOP3b(state)
	case 481:
		u.runVDIVSCALEF64(state)
	case 489:
		u.runVMADI64I32(state)
	default:
		log.Panicf("Opcode %d for VOP3b format is not implemented", inst.Opcode)
	}

	u.vop3aPostprocess(state)
}

func (u *ALUImpl) runVADDU32VOP3b(state InstEmuState) {
	sp := state.Scratchpad().AsVOP3B()

	var i uint
	for i = 0; i < 64; i++ {
		if !laneMasked(sp.EXEC, i) {
			continue
		}

		sp.DST[i] = sp.SRC0[i] + sp.SRC1[i]
		if sp.DST[i] > 0xffffffff {
			sp.SDST |= 1 << i
			sp.DST[i] &= 0xffffffff
		}
	}
}

func (u *ALUImpl) runVSUBU32VOP3b(state InstEmuState) {
	sp := state.Scratchpad().AsVOP3B()

	var i uint
	for i = 0; i < 64; i++ {
		if !laneMasked(sp.EXEC, i) {
			continue
		}

		sp.DST[i] = sp.SRC0[i] - sp.SRC1[i]
		if sp.SRC0[i] < sp.SRC1[i] {
			sp.SDST |= 1 << i
			sp.DST[i] &= 0xffffffff
		}
	}
}

func (u *ALUImpl) runVSUBREVU32VOP3b(state InstEmuState) {
	sp := state.Scratchpad().AsVOP3B()

	var i uint
	for i = 0; i < 64; i++ {
		if !laneMasked(sp.EXEC, i) {
			continue
		}

		sp.DST[i] = sp.SRC1[i] - sp.SRC0[i]
		if sp.DST[i] > 0xffffffff {
			sp.SDST |= 1 << i
			sp.DST[i] &= 0xffffffff
		}
	}
}

func (u *ALUImpl) runVADDCU32VOP3b(state InstEmuState) {
	sp := state.Scratchpad().AsVOP3B()

	var i uint
	for i = 0; i < 64; i++ {
		if !laneMasked(sp.EXEC, i) {
			continue
		}

		sp.DST[i] = sp.SRC0[i] + sp.SRC1[i] + ((sp.SRC2[i] & (1 << i)) >> i)
		carry := uint64(0)
		if sp.DST[i] > 0xffffffff {
			carry = 1
		}
		sp.SDST |= carry << i
		sp.DST[i] &= 0xffffffff
	}
}

func (u *ALUImpl) runVSUBBU32VOP3b(state InstEmuState) {
	sp := state.Scratchpad().AsVOP3B()

	var i uint
	for i = 0; i < 64; i++ {
		if !laneMasked(sp.EXEC, i) {
			continue
		}

		sp.DST[i] = sp.SRC0[i] - sp.SRC1[i] - ((sp.SRC2[i] & (1 << i)) >> i)
		carry := uint64(0)
		if sp.DST[i] > 0xffffffff {
			carry = 1
		}
		sp.SDST |= carry << i
		sp.DST[i] &= 0xffffffff
	}
}

func (u *ALUImpl) runVSUBBREVU32VOP3b(state InstEmuState) {
	sp := state.Scratchpad().AsVOP3B()

	var i uint
	for i = 0; i < 64; i++ {
		if !laneMasked(sp.EXEC, i) {
			continue
		}

		sp.DST[i] = sp.SRC1[i] - sp.SRC0[i] - ((sp.SRC2[i] & (1 << i)) >> i)
		carry := uint64(0)
		if sp.DST[i] > 0xffffffff {
			carry = 1
		}
		sp.SDST |= carry << i
		sp.DST[i] &= 0xffffffff
	}
}

//nolint:gocyclo,funlen
func (u *ALUImpl) runVDIVSCALEF64(state InstEmuState) {
	sp := state.Scratchpad().AsVOP3B()
	var i uint
	for i = 0; i < 64; i++ {
		if !laneMasked(sp.EXEC, i) {
			continue
		}

		// set to 0
		//sp.VCC = sp.VCC & ((1 << i) - 1)
		sp.VCC = 0

		src0 := math.Float64frombits(sp.SRC0[i])
		src1 := math.Float64frombits(sp.SRC1[i])
		src2 := math.Float64frombits(sp.SRC2[i])

		exponentSrc1 := (sp.SRC1[i] << 1) >> 53
		exponentSrc2 := (sp.SRC2[i] << 1) >> 53

		diffExpSrc2Src1 := int64(exponentSrc2) - int64(exponentSrc1)

		fractionSrc1 := (sp.SRC1[i] << 12) >> 12

		reversedSrc1 := float64(1) / src1
		src2DivSrc1 := src2 / src1

		exponentRevSrc1 := (uint64(reversedSrc1) << 1) >> 53
		fractionRevSrc1 := (uint64(reversedSrc1) << 12) >> 12

		exponentSrc2DivSrc1 := (uint64(src2DivSrc1) << 1) >> 53
		fractionSrc2DivSrc1 := (uint64(src2DivSrc1) << 12) >> 12

		if src2 == 0 || src1 == 0 {
			sp.DST[i] = 0x7FFFFFFFFFFFFFFF // NaN
		} else if diffExpSrc2Src1 >= 768 {
			// N/D near MAX_FLOAT
			//sp.VCC = sp.VCC | (1 << i)
			sp.VCC = 1
			if src0 == src1 {
				// Only scale the denominator
				sp.DST[i] = math.Float64bits(src0 * math.Pow(2.0, 128))
			}
		} else if exponentSrc1 == 0 && fractionSrc1 != 0 {
			// subnormal .. => DENORM
			sp.DST[i] = math.Float64bits(src0 * math.Pow(2.0, 128))
		} else if (exponentRevSrc1 == 0 && fractionRevSrc1 != 0) && (exponentSrc2DivSrc1 == 0 && fractionSrc2DivSrc1 != 0) {
			//sp.VCC = sp.VCC | (1 << i)
			sp.VCC = 1
			if src0 == src1 {
				// Only scale the denominator
				sp.DST[i] = math.Float64bits(src0 * math.Pow(2.0, 128))
			}
		} else if exponentRevSrc1 == 0 && fractionRevSrc1 != 0 {
			sp.DST[i] = math.Float64bits(src0 * math.Pow(2.0, 128))
		} else if exponentSrc2DivSrc1 == 0 && fractionSrc2DivSrc1 != 0 {
			//sp.VCC = sp.VCC | (1 << i)
			sp.VCC = 1
			if src0 == src2 {
				// Only scale the denominator
				sp.DST[i] = math.Float64bits(src0 * math.Pow(2.0, 128))
			}
		} else if exponentSrc2 <= 53 {
			// Numerator is tiny
			sp.DST[i] = math.Float64bits(src0 * math.Pow(2.0, 128))
		}
	}
}

// runVMADI64I32 implements V_MAD_I64_I32 (GCN3 ISA reference, ch. 12, "VOP3b
// Opcodes", opcode 489): {SDST, D.i64} = S0.i32 * S1.i32 + S2.i64. The lane's
// SDST bit is set on signed overflow of the 64-bit result.
func (u *ALUImpl) runVMADI64I32(state InstEmuState) {
	sp := state.Scratchpad().AsVOP3B()

	var i uint
	for i = 0; i < 64; i++ {
		if !laneMasked(sp.EXEC, i) {
			continue
		}

		prod := int64(int32(uint32(sp.SRC0[i]))) * int64(int32(uint32(sp.SRC1[i])))
		add := int64(sp.SRC2[i])
		sum := prod + add
		sp.DST[i] = uint64(sum)
		if (prod >= 0) == (add >= 0) && (sum >= 0) != (prod >= 0) {
			sp.SDST |= 1 << i
		}
	}
}
