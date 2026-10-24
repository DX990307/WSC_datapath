package pagemigration

import (
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

const (
	pullReqBytes    = 12 // same overhead as a memory read request
	pullRspOverhead = 4  // same overhead as a data-ready response
)

// PullReq asks the copy engine of the source GPM for Size bytes at PAddr.
type PullReq struct {
	sim.MsgMeta
	PAddr uint64
	Size  uint64
	PID   vm.PID // the source reads its L2 with the page's PID
}

// Meta returns the message meta data.
func (r *PullReq) Meta() *sim.MsgMeta { return &r.MsgMeta }

// PullRsp carries the data of a PullReq back to the destination GPM.
type PullRsp struct {
	sim.MsgMeta
	RespondTo string
	Data      []byte
}

// Meta returns the message meta data.
func (r *PullRsp) Meta() *sim.MsgMeta { return &r.MsgMeta }

// InvalMsg (P21) tells a replica holder that its copy of a page is dropped
// (only its network traffic matters; the receiver discards it).
type InvalMsg struct {
	sim.MsgMeta
}

// Meta returns the message meta data.
func (r *InvalMsg) Meta() *sim.MsgMeta { return &r.MsgMeta }

func newInvalMsg(now sim.VTimeInSec, src, dst sim.Port) *InvalMsg {
	r := &InvalMsg{}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src, r.Dst, r.SendTime = src, dst, now
	r.TrafficBytes = pullReqBytes
	return r
}

func newPullReq(now sim.VTimeInSec, src, dst sim.Port, pAddr, size uint64) *PullReq {
	r := &PullReq{PAddr: pAddr, Size: size}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src, r.Dst, r.SendTime = src, dst, now
	r.TrafficBytes = pullReqBytes
	return r
}

func newPullRsp(now sim.VTimeInSec, src, dst sim.Port, rspTo string, data []byte) *PullRsp {
	r := &PullRsp{RespondTo: rspTo, Data: data}
	r.ID = sim.GetIDGenerator().Generate()
	r.Src, r.Dst, r.SendTime = src, dst, now
	r.TrafficBytes = len(data) + pullRspOverhead
	return r
}
