package cp

import (
	"math"

	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/wgsteal"
)

// T19: the command processor's side of WG stealing. The CP gets a second
// mesh port for steal requests and replies, so that they never wait behind
// driver messages on ToDriver.

type delayedStealMsg struct {
	at  sim.VTimeInSec
	msg sim.Msg
}

type stealDispatcher interface {
	EnableWGSteal(coord *wgsteal.Coordinator, gpm int, port sim.Port,
		send func(msg sim.Msg, delayCycles int))
}

type cpSteal struct {
	coord   *wgsteal.Coordinator
	gpm     int
	port    sim.Port
	sender  sim.BufferedSender
	delayed []delayedStealMsg
}

// CreateStealPort creates the CP's steal port. The GPM builder adds it to
// the GPM's external ports before the mesh is built.
func (p *CommandProcessor) CreateStealPort() sim.Port {
	if p.steal != nil {
		return p.steal.port
	}
	port := sim.NewLimitNumMsgPort(p, 4, p.Name()+".ToSteal")
	p.steal = &cpSteal{
		port: port,
		sender: sim.NewBufferedSender(port,
			sim.NewBuffer(p.Name()+".ToStealSenderBuffer", math.MaxInt32)),
	}
	return port
}

// StealPort returns the steal port (nil if T19 is off).
func (p *CommandProcessor) StealPort() sim.Port {
	if p.steal == nil {
		return nil
	}
	return p.steal.port
}

// EnableWGSteal connects the CP and its dispatchers to the coordinator.
func (p *CommandProcessor) EnableWGSteal(coord *wgsteal.Coordinator, gpm int) {
	if p.steal == nil {
		panic("EnableWGSteal before CreateStealPort")
	}
	p.steal.coord = coord
	p.steal.gpm = gpm
	for _, d := range p.Dispatchers {
		sd, ok := d.(stealDispatcher)
		if !ok {
			panic("dispatcher does not support T19 WG stealing")
		}
		sd.EnableWGSteal(coord, gpm, p.steal.port, p.queueStealMsg)
	}
}

func (p *CommandProcessor) queueStealMsg(msg sim.Msg, delayCycles int) {
	now := p.Engine.CurrentTime()
	at := now
	if delayCycles > 0 {
		at = p.Freq.NCyclesLater(delayCycles, now)
	}
	p.steal.delayed = append(p.steal.delayed, delayedStealMsg{at: at, msg: msg})
}

func (p *CommandProcessor) tickSteal(now sim.VTimeInSec) bool {
	s := p.steal
	madeProgress := false

	// Messages whose delay (thief probe, victim service) has passed go to
	// the sender in order.
	kept := s.delayed[:0]
	for _, d := range s.delayed {
		if d.at <= now {
			d.msg.Meta().SendTime = now
			s.sender.Send(d.msg)
			madeProgress = true
		} else {
			kept = append(kept, d)
		}
	}
	s.delayed = kept
	if len(s.delayed) > 0 {
		madeProgress = true // keep ticking until the delay passes
	}

	madeProgress = p.sendMsgsOutFromPort(now, s.sender) || madeProgress

	msg := s.port.Peek()
	if msg == nil {
		return madeProgress
	}
	switch m := msg.(type) {
	case *wgsteal.StealReq:
		ranges, remain := m.Launch.Serve(s.gpm, m.Thief, m.Plan.Cand, now)
		cfg := s.coord.Config()
		rsp := &wgsteal.StealRsp{
			Launch:       m.Launch,
			Thief:        m.Thief,
			Victim:       s.gpm,
			Plan:         m.Plan,
			Ranges:       ranges,
			VictimRemain: remain,
			Issue:        m.Issue,
			Serve:        now,
		}
		rsp.ID = sim.GetIDGenerator().Generate()
		rsp.Src = s.port
		rsp.Dst = m.Src
		rsp.TrafficBytes = cfg.RspBytes
		s.delayed = append(s.delayed, delayedStealMsg{
			at:  p.Freq.NCyclesLater(cfg.VictimCycles, now),
			msg: rsp,
		})
	case *wgsteal.StealRsp:
		m.Launch.Receive(s.gpm, m.Victim, m.Ranges, m.VictimRemain,
			m.Issue, m.Serve, now, m.Plan)
	default:
		panic("unexpected message on the CP steal port")
	}
	s.port.Retrieve(now)
	return true
}
