package addresstranslator

import (
	"reflect"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// SharingAccess describes one translated memory access at page granularity.
type SharingAccess struct {
	Time        sim.VTimeInSec
	RequesterID uint64
	OwnerID     uint64
	PID         vm.PID
	VAddr       uint64
	PAddr       uint64
	PageVAddr   uint64
	PagePAddr   uint64
	PageBlock   uint64
	PageSize    uint64
	Bytes       uint64
	AccessType  string
}

// SharingTracer receives compact memory-access records after translation.
type SharingTracer interface {
	TraceSharingAccess(SharingAccess)
}

func (t *AddressTranslator) traceSharingAccess(
	needTime sim.VTimeInSec,
	req mem.AccessReq,
	page vm.Page,
) {
	if t.sharingTracer == nil {
		return
	}

	offset := req.GetAddress() % (1 << t.log2PageSize)
	t.sharingTracer.TraceSharingAccess(SharingAccess{
		Time:        needTime,
		RequesterID: t.deviceID,
		OwnerID:     page.DeviceID,
		PID:         req.GetPID(),
		VAddr:       req.GetAddress(),
		PAddr:       page.PAddr + offset,
		PageVAddr:   page.VAddr,
		PagePAddr:   page.PAddr,
		PageBlock:   page.PageBlock,
		PageSize:    page.PageSize,
		Bytes:       req.GetByteSize(),
		AccessType:  sharingAccessType(req),
	})
}

func sharingAccessType(req mem.AccessReq) string {
	switch req.(type) {
	case *mem.ReadReq:
		return "R"
	case *mem.WriteReq:
		return "W"
	default:
		return reflect.TypeOf(req).String()
	}
}
