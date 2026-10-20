package wgsteal

import "github.com/sarchlab/akita/v3/sim"

// StealReq asks a victim's command processor for a chunk of WGs.
type StealReq struct {
	sim.MsgMeta
	Launch *Launch
	Thief  int // GPM index
	Victim int // GPM index
	Plan   Plan
	Issue  sim.VTimeInSec // when the thief decided to steal
}

// Meta returns the message meta data.
func (m *StealReq) Meta() *sim.MsgMeta { return &m.MsgMeta }

// StealRsp carries the stolen WGs (none = NACK) back to the thief.
type StealRsp struct {
	sim.MsgMeta
	Launch       *Launch
	Thief        int
	Victim       int
	Plan         Plan
	Ranges       []WGRange
	VictimRemain int
	Issue        sim.VTimeInSec
	Serve        sim.VTimeInSec
}

// Meta returns the message meta data.
func (m *StealRsp) Meta() *sim.MsgMeta { return &m.MsgMeta }
