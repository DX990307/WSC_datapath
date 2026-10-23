package switching

import (
	"sync/atomic"

	"github.com/sarchlab/akita/v3/noc/messaging"
	"github.com/sarchlab/akita/v3/pipelining"
	"github.com/sarchlab/akita/v3/sim"
)

// Low-load multi-hop bypass (SMART-NoC-like approximation, isca27/hop-bypass).
//
// When BypassLatency > 0, every switch input port whose normal pipeline is
// deeper than BypassLatency gets a second, BypassLatency-stage pipeline. A
// flit arriving at such a port takes the short pipeline when the port is idle
// and the output it routes to is idle:
//   - no older flit of this input port is still in the normal pipeline, the
//     route buffer or the forward buffer (keeps per-port FIFO order), and
//   - the send-out buffer of its output port is empty (unicast only; for a
//     multicast flit only the input side is checked).
//
// Otherwise it takes the normal -switch-latency pipeline. With
// BypassMaxHops > 0 a flit may bypass at most that many consecutive switches;
// the next switch then uses the normal pipeline and the run restarts.
// Endpoint ports (latency 1) are never affected. Off (0) keeps the original
// behavior.
var (
	BypassLatency int
	BypassMaxHops int
)

// BypassCounters counts flit arrivals at bypass-capable switch ports.
type BypassCounters struct {
	Bypassed     int64 // took the short pipeline
	BusyInput    int64 // normal: older flits queued at this input port
	BusyOutput   int64 // normal: output send-out buffer not empty
	HopCap       int64 // normal: reached BypassMaxHops
	PipelineFull int64 // normal: short pipeline first stage occupied
}

var bypassCounters BypassCounters

// GetBypassCounters returns a snapshot of the global bypass counters.
func GetBypassCounters() BypassCounters {
	return BypassCounters{
		Bypassed:     atomic.LoadInt64(&bypassCounters.Bypassed),
		BusyInput:    atomic.LoadInt64(&bypassCounters.BusyInput),
		BusyOutput:   atomic.LoadInt64(&bypassCounters.BusyOutput),
		HopCap:       atomic.LoadInt64(&bypassCounters.HopCap),
		PipelineFull: atomic.LoadInt64(&bypassCounters.PipelineFull),
	}
}

type bypassState struct {
	pipeline       pipelining.Pipeline
	normalInFlight int // flits in normal pipeline or route buffer
}

func newBypassState(name string, width int, routeBuf sim.Buffer) *bypassState {
	return &bypassState{
		pipeline: pipelining.MakeBuilder().
			WithNumStage(BypassLatency).
			WithCyclePerStage(1).
			WithPipelineWidth(width).
			WithPostPipelineBuffer(routeBuf).
			Build(name + ".BypassPipeline"),
	}
}

const (
	bypassYes = iota
	bypassBusyInput
	bypassBusyOutput
	bypassHopCap
	bypassPipelineFull
)

// chooseBypass decides whether flit takes the short pipeline of pc.
func (s *Switch) chooseBypass(pc portComplex, flit *messaging.Flit) int {
	bp := pc.bypass
	if bp.normalInFlight > 0 || pc.routeBuffer.Size() > 0 ||
		pc.forwardBuffer.Size() > 0 {
		return bypassBusyInput
	}
	if BypassMaxHops > 0 && flit.BypassRun >= BypassMaxHops {
		return bypassHopCap
	}
	if _, ok := flit.Msg.(messaging.MulticastMsg); !ok {
		outPort := s.routingTable.FindPort(flit.Msg.Meta().Dst)
		if outPort != nil {
			if out, ok := s.portToComplexMapping[outPort]; ok &&
				out.sendOutBuffer.Size() > 0 {
				return bypassBusyOutput
			}
		}
	}
	if !bp.pipeline.CanAccept() {
		return bypassPipelineFull
	}
	return bypassYes
}

func countBypass(reason int) {
	switch reason {
	case bypassYes:
		atomic.AddInt64(&bypassCounters.Bypassed, 1)
	case bypassBusyInput:
		atomic.AddInt64(&bypassCounters.BusyInput, 1)
	case bypassBusyOutput:
		atomic.AddInt64(&bypassCounters.BusyOutput, 1)
	case bypassHopCap:
		atomic.AddInt64(&bypassCounters.HopCap, 1)
	case bypassPipelineFull:
		atomic.AddInt64(&bypassCounters.PipelineFull, 1)
	}
}
