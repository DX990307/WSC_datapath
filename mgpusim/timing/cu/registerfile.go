package cu

import (
	"log"

	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/insts"
)

// A RegisterAccess is an incidence of reading or writing the register
type RegisterAccess struct {
	Time       sim.VTimeInSec
	Reg        *insts.Reg
	RegCount   int
	LaneID     int
	WaveOffset int
	Data       []byte
	OK         bool
}

// A RegisterFile provides the communication interface for a set of registers.
type RegisterFile interface {
	Read(access RegisterAccess)
	Write(access RegisterAccess)
}

// A SimpleRegisterFile is a Register file that can always read and write
// registers immediately
type SimpleRegisterFile struct {
	storage []byte

	// In vector register, each lane can have up-to 256 VGPRs. Then the offset
	// difference from v0 lane 0 to v0 lane 1 is 256*4 = 1024B. Field
	// ByteSizePerLane should be set to 1024 in vector registers.
	ByteSizePerLane int
}

// NewSimpleRegisterFile creates and returns a new SimpleRegisterFile
func NewSimpleRegisterFile(
	byteSize uint64,
	byteSizePerLane int,
) *SimpleRegisterFile {
	r := new(SimpleRegisterFile)
	r.storage = make([]byte, byteSize)
	r.ByteSizePerLane = byteSizePerLane
	return r
}

func (r *SimpleRegisterFile) Write(access RegisterAccess) {
	offset := r.getRegOffset(access.Reg, access.WaveOffset, access.LaneID)

	if access.RegCount == 0 {
		access.RegCount = 1
	}

	size := access.RegCount * 4
	copyRegisterBytes(r.storage[offset:offset+size], access.Data, size)
	access.OK = true
}

func (r *SimpleRegisterFile) Read(access RegisterAccess) {
	offset := r.getRegOffset(access.Reg, access.WaveOffset, access.LaneID)

	if access.RegCount == 0 {
		access.RegCount = 1
	}

	size := access.RegCount * 4
	copyRegisterBytes(access.Data, r.storage[offset:offset+size], size)
	access.OK = true
}

// copyRegisterBytes keeps the overwhelmingly common 4/8/16-byte register
// transfers at compile-time sizes. A generic copy with a runtime length was
// showing up as runtime.memmove for every operand lane in CPU profiles.
func copyRegisterBytes(dst, src []byte, size int) {
	if len(dst) < size || len(src) < size {
		copy(dst, src)
		return
	}
	switch size {
	case 4:
		*(*[4]byte)(dst) = *(*[4]byte)(src)
	case 8:
		*(*[8]byte)(dst) = *(*[8]byte)(src)
	case 16:
		*(*[16]byte)(dst) = *(*[16]byte)(src)
	default:
		copy(dst[:size], src[:size])
	}
}

func (r *SimpleRegisterFile) getRegOffset(reg *insts.Reg, offset int, laneID int) int {
	if reg.IsSReg() {
		return reg.RegIndex()*4 + offset
	}

	if reg.IsVReg() {
		regOffset := reg.RegIndex()*4 + laneID*r.ByteSizePerLane + offset
		return regOffset
	}

	log.Panic("Register type not supported by register files")

	return 0
}
