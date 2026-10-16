package emu

import (
	"math"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/mgpusim/v3/insts"
)

var _ = Describe("ALU", func() {

	var (
		alu   *ALUImpl
		state *mockInstState
	)

	BeforeEach(func() {
		alu = NewALU(nil)

		state = new(mockInstState)
		state.scratchpad = make([]byte, 4096)
	})

	It("should run V_ADD_U32 VOP3b", func() {
		state.inst = insts.NewInst()
		state.inst.FormatType = insts.VOP3b
		state.inst.Opcode = 281

		sp := state.Scratchpad().AsVOP3B()
		sp.SRC0[0] = 1
		sp.SRC1[0] = 2
		sp.SRC0[1] = 0xffffffff
		sp.SRC1[1] = 2
		sp.EXEC = 3

		alu.Run(state)

		Expect(sp.DST[0]).To(Equal(uint64(3)))
		Expect(sp.DST[1] & 0xffffffff).To(Equal(uint64(1)))
		Expect(sp.SDST).To(Equal(uint64(0x2)))
	})

	It("should run V_SUB_U32 VOP3b", func() {
		state.inst = insts.NewInst()
		state.inst.FormatType = insts.VOP3b
		state.inst.Opcode = 282

		sp := state.Scratchpad().AsVOP3B()
		sp.SRC0[0] = 1
		sp.SRC1[0] = 2
		sp.SRC0[1] = 0xffffffff
		sp.SRC1[1] = 2
		sp.EXEC = 3

		alu.Run(state)

		Expect(sp.DST[0] & 0xffffffff).To(Equal(uint64(0xffffffff)))
		Expect(sp.DST[1] & 0xffffffff).To(Equal(uint64(0xfffffffd)))
		Expect(sp.SDST).To(Equal(uint64(0x1)))
	})

	It("should run V_SUBREV_U32 VOP3b", func() {
		state.inst = insts.NewInst()
		state.inst.FormatType = insts.VOP3b
		state.inst.Opcode = 283

		sp := state.Scratchpad().AsVOP3B()
		sp.SRC0[0] = uint64(2)
		sp.SRC1[0] = uint64(0xffffffff)
		sp.SRC0[1] = uint64(2)
		sp.SRC1[1] = uint64(0x0)
		sp.EXEC = 3

		alu.Run(state)

		Expect(sp.DST[0]).To(Equal(uint64(0xfffffffd)))
		Expect(sp.DST[1]).To(Equal(uint64(0xfffffffe)))
		Expect(sp.SDST).To(Equal(uint64(2)))
	})

	It("should run V_ADDC_U32", func() {
		state.inst = insts.NewInst()
		state.inst.FormatType = insts.VOP3b
		state.inst.Opcode = 284

		sp := state.scratchpad.AsVOP3B()
		sp.SRC0[0] = uint64(0xfffffffd)
		sp.SRC1[0] = uint64(2)
		sp.SRC2[0] = uint64(1)
		sp.SRC0[1] = uint64(0xfffffffd)
		sp.SRC1[1] = uint64(1)
		sp.SRC2[1] = uint64(1)
		sp.EXEC = 0x3

		alu.Run(state)

		Expect(sp.DST[0]).To(Equal(uint64(0)))
		Expect(sp.DST[1]).To(Equal(uint64(0xfffffffe)))
		Expect(sp.SDST).To(Equal(uint64(1)))
	})

	It("should run V_SUBB_U32", func() {
		state.inst = insts.NewInst()
		state.inst.FormatType = insts.VOP3b
		state.inst.Opcode = 285

		sp := state.scratchpad.AsVOP3B()
		sp.SRC0[0] = uint64(0x1)
		sp.SRC1[0] = uint64(0x2)
		sp.SRC2[0] = uint64(0x1)
		sp.SRC0[1] = uint64(0xfffffffd)
		sp.SRC1[1] = uint64(0x1)
		sp.SRC2[1] = uint64(0x1)
		sp.EXEC = 0x3

		alu.Run(state)

		Expect(sp.DST[0]).To(Equal(uint64(0xfffffffe)))
		Expect(sp.DST[1]).To(Equal(uint64(0xfffffffc)))
		Expect(sp.SDST).To(Equal(uint64(1)))
	})

	It("should run V_SUBBREV_U32", func() {
		state.inst = insts.NewInst()
		state.inst.FormatType = insts.VOP3b
		state.inst.Opcode = 286

		sp := state.scratchpad.AsVOP3B()
		sp.SRC1[0] = uint64(0x1)
		sp.SRC0[0] = uint64(0x2)
		sp.SRC2[0] = uint64(0x1)
		sp.SRC1[1] = uint64(0xfffffffd)
		sp.SRC0[1] = uint64(0x1)
		sp.SRC2[1] = uint64(0x1)
		sp.EXEC = 0x3

		alu.Run(state)

		Expect(sp.DST[0]).To(Equal(uint64(0xfffffffe)))
		Expect(sp.DST[1]).To(Equal(uint64(0xfffffffc)))
		Expect(sp.SDST).To(Equal(uint64(1)))
	})

	It("should run V_DIV_SCALE_F64", func() {
		// Need more test case
		state.inst = insts.NewInst()
		state.inst.FormatType = insts.VOP3b
		state.inst.Opcode = 481

		sp := state.scratchpad.AsVOP3B()
		sp.SRC0[0] = uint64(0x3FF0000000000000)
		sp.SRC1[0] = uint64(0x0008A00000000000)
		sp.SRC2[0] = uint64(0x0008A00000000000)
		sp.EXEC = 0x1

		alu.Run(state)

		Expect(sp.DST[0]).To(Equal(math.Float64bits(math.Pow(2.0, 128))))
	})

	It("should run V_MAD_I64_I32", func() {
		state.inst = insts.NewInst()
		state.inst.FormatType = insts.VOP3b
		state.inst.Opcode = 489

		sp := state.Scratchpad().AsVOP3B()
		sp.SRC0[0] = uint64(uint32(0xfffffffd)) // -3
		sp.SRC1[0] = 1000
		sp.SRC2[0] = 5
		sp.SRC0[1] = 0x7fffffff
		sp.SRC1[1] = 0x7fffffff
		sp.SRC2[1] = uint64(1) << 32
		sp.SRC0[2] = 1
		sp.SRC1[2] = 1
		sp.SRC2[2] = 0x7fffffffffffffff // overflows
		sp.EXEC = 7

		alu.Run(state)

		Expect(int64(sp.DST[0])).To(Equal(int64(-2995)))
		Expect(sp.DST[1]).To(Equal(uint64(0x3fffffff00000001) + uint64(1)<<32))
		Expect(sp.DST[2]).To(Equal(uint64(0x8000000000000000)))
		Expect(sp.SDST).To(Equal(uint64(0x4)))
	})
})
