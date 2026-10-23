package switching

import (
	"fmt"

	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/noc/messaging"
	"github.com/sarchlab/akita/v3/noc/networking/arbitration"
	"github.com/sarchlab/akita/v3/noc/networking/routing"
	"github.com/sarchlab/akita/v3/pipelining"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

type flitPipelineItem struct {
	taskID string
	flit   *messaging.Flit
	// normal is set on bypass-capable ports when the flit took the normal
	// pipeline (see bypass.go).
	normal bool
}

func (f flitPipelineItem) TaskID() string {
	return f.taskID
}

// A portComplex is the infrastructure related to a port.
type portComplex struct {
	// localPort is the port that is equipped on the switch.
	localPort sim.Port

	// remotePort is the port that is connected to the localPort.
	remotePort sim.Port

	// Data arrived at the local port needs to be processed in a pipeline. There
	// is a processing pipeline for each local port.
	pipeline pipelining.Pipeline

	// The flits here are buffered after the pipeline and are waiting to be
	// assigned with an output buffer.
	routeBuffer sim.Buffer

	// The flits here are buffered to wait to be forwarded to the output buffer.
	forwardBuffer sim.Buffer

	// The flits here are waiting to be sent to the next hop.
	sendOutBuffer sim.Buffer

	// NumInputChannel is the number of flits that can stream into the
	// switch from the port. The RouteBuffer and the ForwardBuffer should
	// have the capacity of this number.
	numInputChannel int

	// NumOutputChannel is the number of flits that can stream out of the
	// switch to the port. The SendOutBuffer should have the capacity of this
	// number.
	numOutputChannel int

	// bypass is the low-load short pipeline; nil when bypass is off or the
	// port is not deeper than BypassLatency.
	bypass *bypassState
}

// Switch is an Akita component that can forward request to destination.
type Switch struct {
	*sim.TickingComponent

	ports                []sim.Port
	portToComplexMapping map[sim.Port]portComplex
	routingTable         routing.Table
	arbiter              arbitration.Arbiter
	flitSwitchStart      map[string]sim.VTimeInSec
	flitRouteStart       map[string]sim.VTimeInSec
	flitForwardStart     map[string]sim.VTimeInSec
	multicastSplits      map[string]*multicastSplit
}

// addPort adds a new port on the switch.
func (s *Switch) addPort(complex portComplex) {
	s.ports = append(s.ports, complex.localPort)
	s.portToComplexMapping[complex.localPort] = complex
	s.arbiter.AddBuffer(complex.forwardBuffer)
}

// GetRoutingTable returns the routine table used by the switch.
func (s *Switch) GetRoutingTable() routing.Table {
	return s.routingTable
}

// Tick update the Switch's state.
func (s *Switch) Tick(now sim.VTimeInSec) bool {
	madeProgress := false

	madeProgress = s.sendOut(now) || madeProgress
	madeProgress = s.forward(now) || madeProgress
	madeProgress = s.route(now) || madeProgress
	madeProgress = s.movePipeline(now) || madeProgress
	madeProgress = s.startProcessing(now) || madeProgress

	return madeProgress
}

func (s *Switch) flitParentTaskID(flit *messaging.Flit) string {
	return flit.ID + "_e2e"
}

func (s *Switch) flitTaskID(flit *messaging.Flit) string {
	return flit.ID + "_" + s.Name()
}

func (s *Switch) startProcessing(now sim.VTimeInSec) (madeProgress bool) {
	for _, port := range s.ports {
		pc := s.portToComplexMapping[port]

		for i := 0; i < pc.numInputChannel; i++ {
			item := port.Peek()
			if item == nil {
				break
			}

			flit := item.(*messaging.Flit)
			reason := -1
			if pc.bypass != nil {
				reason = s.chooseBypass(pc, flit)
			}
			if reason != bypassYes && !pc.pipeline.CanAccept() {
				break
			}
			recvTime := flit.Meta().RecvTime
			if recvTime == 0 {
				recvTime = now
			}
			memtrace.RecordMemoryPathNetworkFlitStage(
				s.Name(),
				"switch_input_queue",
				flit.Msg.Meta().ID,
				flit.Meta().ID,
				flit.SeqID,
				flit.NumFlitInMsg,
				recvTime,
				now,
				flit.Meta().Src,
				port,
			)
			pipelineItem := flitPipelineItem{
				taskID: s.flitTaskID(flit),
				flit:   flit,
			}
			switch {
			case reason == bypassYes:
				flit.BypassRun++
				pc.bypass.pipeline.Accept(now, pipelineItem)
			case reason >= 0:
				flit.BypassRun = 0
				pipelineItem.normal = true
				pc.bypass.normalInFlight++
				pc.pipeline.Accept(now, pipelineItem)
			default:
				pc.pipeline.Accept(now, pipelineItem)
			}
			if reason >= 0 {
				countBypass(reason)
			}
			port.Retrieve(now)
			s.flitSwitchStart[flit.Meta().ID] = now
			madeProgress = true

			tracing.StartTask(
				s.flitTaskID(flit),
				s.flitParentTaskID(flit),
				s, "flit", "flit_inside_sw",
				flit,
			)

			// fmt.Printf("%.10f, %s, switch recv flit, %s\n",
			// 	now, s.Name(), flit.ID)
		}
	}

	return madeProgress
}

func (s *Switch) movePipeline(now sim.VTimeInSec) (madeProgress bool) {
	for _, port := range s.ports {
		pc := s.portToComplexMapping[port]
		madeProgress = pc.pipeline.Tick(now) || madeProgress
		if pc.bypass != nil {
			madeProgress = pc.bypass.pipeline.Tick(now) || madeProgress
		}
	}

	return madeProgress
}

func (s *Switch) route(now sim.VTimeInSec) (madeProgress bool) {
	for _, port := range s.ports {
		pc := s.portToComplexMapping[port]
		routeBuf := pc.routeBuffer
		forwardBuf := pc.forwardBuffer

		for i := 0; i < pc.numInputChannel; i++ {
			item := routeBuf.Peek()
			if item == nil {
				break
			}

			if !forwardBuf.CanPush() {
				break
			}

			pipelineItem := item.(flitPipelineItem)
			flit := pipelineItem.flit
			if mc, ok := flit.Msg.(messaging.MulticastMsg); ok {
				if !s.routeMulticastFlit(now, flit, mc, forwardBuf) {
					break
				}
				routeBuf.Pop()
				if pipelineItem.normal {
					pc.bypass.normalInFlight--
				}
				madeProgress = true
				continue
			}
			start := s.flitSwitchStart[flit.Meta().ID]
			if start == 0 {
				start = now
			}
			memtrace.RecordMemoryPathNetworkFlitStage(
				s.Name(),
				"switch_pipeline",
				flit.Msg.Meta().ID,
				flit.Meta().ID,
				flit.SeqID,
				flit.NumFlitInMsg,
				start,
				now,
				nil,
				nil,
			)
			s.assignFlitOutputBuf(flit)
			routeBuf.Pop()
			if pipelineItem.normal {
				pc.bypass.normalInFlight--
			}
			forwardBuf.Push(flit)
			s.flitRouteStart[flit.Meta().ID] = now
			madeProgress = true

			// fmt.Printf("%.10f, %s, switch route flit, %s\n",
			// 	now, s.Name(), flit.ID)
		}
	}

	return madeProgress
}

func (s *Switch) forward(now sim.VTimeInSec) (madeProgress bool) {
	inputBuffers := s.arbiter.Arbitrate(now)

	for _, buf := range inputBuffers {
		for {
			item := buf.Peek()
			if item == nil {
				break
			}

			flit := item.(*messaging.Flit)
			if !flit.OutputBuf.CanPush() {
				break
			}

			start := s.flitRouteStart[flit.Meta().ID]
			if start == 0 {
				start = now
			}
			memtrace.RecordMemoryPathNetworkFlitStage(
				s.Name(),
				"switch_arb_wait",
				flit.Msg.Meta().ID,
				flit.Meta().ID,
				flit.SeqID,
				flit.NumFlitInMsg,
				start,
				now,
				nil,
				nil,
			)
			flit.OutputBuf.Push(flit)
			buf.Pop()
			s.flitForwardStart[flit.Meta().ID] = now
			madeProgress = true

			// fmt.Printf("%.10f, %s, switch forward flit, %s\n",
			// now, s.Name(), item.(*messaging.Flit).ID)
		}
	}

	return madeProgress
}

func (s *Switch) sendOut(now sim.VTimeInSec) (madeProgress bool) {
	for _, port := range s.ports {
		pc := s.portToComplexMapping[port]
		sendOutBuf := pc.sendOutBuffer

		for i := 0; i < pc.numOutputChannel; i++ {
			item := sendOutBuf.Peek()
			if item == nil {
				break
			}

			flit := item.(*messaging.Flit)
			start := s.flitForwardStart[flit.Meta().ID]
			if start == 0 {
				start = now
			}
			flit.Meta().Src = pc.localPort
			flit.Meta().Dst = pc.remotePort
			flit.Meta().SendTime = now

			err := pc.localPort.Send(flit)
			if err == nil {
				memtrace.RecordMemoryPathNetworkFlitStage(
					s.Name(),
					"switch_output_wait",
					flit.Msg.Meta().ID,
					flit.Meta().ID,
					flit.SeqID,
					flit.NumFlitInMsg,
					start,
					now,
					pc.localPort,
					pc.remotePort,
				)
				delete(s.flitSwitchStart, flit.Meta().ID)
				delete(s.flitRouteStart, flit.Meta().ID)
				delete(s.flitForwardStart, flit.Meta().ID)
				sendOutBuf.Pop()
				madeProgress = true

				// fmt.Printf("%.10f, %s, switch send flit out, %s\n",
				// now, s.Name(), flit.ID)

				tracing.EndTask(s.flitTaskID(flit), s)
			}
		}
	}

	return madeProgress
}

// multicastSplit is the per-packet branching decision of a switch for a
// MulticastMsg. All flits of the packet follow the same branches.
type multicastSplit struct {
	msgs   []sim.Msg
	bufs   []sim.Buffer
	routed int
}

// routeMulticastFlit routes one flit of a multicast packet (T18). The
// destinations are grouped by the output port of their unicast route; with
// one group the flit continues unchanged, otherwise the switch replicates the
// flit once per group (one copy per tree branch), each copy carrying the
// sub-packet for that branch. Replication happens when the flit leaves the
// route stage: every copy takes one slot of the input port's forward buffer
// and then competes for its output port like any other flit, so each tree
// edge carries the packet exactly once. It returns false, without consuming
// the flit, when the forward buffer cannot hold all copies this cycle.
func (s *Switch) routeMulticastFlit(
	now sim.VTimeInSec,
	flit *messaging.Flit,
	mc messaging.MulticastMsg,
	forwardBuf sim.Buffer,
) bool {
	if s.multicastSplits == nil {
		s.multicastSplits = make(map[string]*multicastSplit)
	}
	split := s.multicastSplits[mc.Meta().ID]
	if split == nil {
		split = s.splitMulticast(mc)
		s.multicastSplits[mc.Meta().ID] = split
	}
	if forwardBuf.Capacity()-forwardBuf.Size() < len(split.msgs) {
		return false
	}
	for i, msg := range split.msgs {
		f := flit
		if i > 0 {
			f = messaging.FlitBuilder{}.
				WithSendTime(flit.SendTime).
				WithSrc(flit.Src).
				WithDst(flit.Dst).
				WithSeqID(flit.SeqID).
				WithNumFlitInMsg(flit.NumFlitInMsg).
				WithMsg(msg).
				Build()
			f.RecvTime = flit.RecvTime
		}
		f.Msg = msg
		f.OutputBuf = split.bufs[i]
		forwardBuf.Push(f)
		s.flitRouteStart[f.Meta().ID] = now
	}
	split.routed++
	if split.routed >= flit.NumFlitInMsg {
		delete(s.multicastSplits, mc.Meta().ID)
	}
	return true
}

func (s *Switch) splitMulticast(mc messaging.MulticastMsg) *multicastSplit {
	dsts := mc.MulticastDsts()
	if len(dsts) == 0 {
		panic(fmt.Sprintf("%s: multicast %s has no destination",
			s.Name(), mc.Meta().ID))
	}
	var ports []sim.Port
	groups := make(map[sim.Port][]sim.Port)
	for _, dst := range dsts {
		outPort := s.routingTable.FindPort(dst)
		if outPort == nil {
			panic(fmt.Sprintf("%s: no output port for %s", s.Name(), dst))
		}
		if _, seen := groups[outPort]; !seen {
			ports = append(ports, outPort)
		}
		groups[outPort] = append(groups[outPort], dst)
	}
	split := &multicastSplit{}
	for _, port := range ports {
		group := groups[port]
		var msg sim.Msg = mc
		if len(ports) > 1 || len(group) == 1 {
			msg = mc.SplitForDsts(group)
		}
		buf := s.portToComplexMapping[port].sendOutBuffer
		if buf == nil {
			panic(fmt.Sprintf("%s: no output buffer for %s", s.Name(), port))
		}
		split.msgs = append(split.msgs, msg)
		split.bufs = append(split.bufs, buf)
	}
	return split
}

func (s *Switch) assignFlitOutputBuf(f *messaging.Flit) {
	outPort := s.routingTable.FindPort(f.Msg.Meta().Dst)
	if outPort == nil {
		panic(fmt.Sprintf("%s: no output port for %s",
			s.Name(), f.Msg.Meta().Dst))
	}

	pc := s.portToComplexMapping[outPort]

	f.OutputBuf = pc.sendOutBuffer
	if f.OutputBuf == nil {
		panic(fmt.Sprintf("%s: no output buffer for %s",
			s.Name(), f.Msg.Meta().Dst))
	}
}

// SwitchBuilder can build switches
type SwitchBuilder struct {
	engine       sim.Engine
	freq         sim.Freq
	routingTable routing.Table
	arbiter      arbitration.Arbiter
}

// WithEngine sets the engine that the switch to build uses.
func (b SwitchBuilder) WithEngine(engine sim.Engine) SwitchBuilder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency that the switch to build works at.
func (b SwitchBuilder) WithFreq(freq sim.Freq) SwitchBuilder {
	b.freq = freq
	return b
}

// WithArbiter sets the arbiter to be used by the switch to build.
func (b SwitchBuilder) WithArbiter(arbiter arbitration.Arbiter) SwitchBuilder {
	b.arbiter = arbiter
	return b
}

// WithRoutingTable sets the routing table to be used by the switch to build.
func (b SwitchBuilder) WithRoutingTable(rt routing.Table) SwitchBuilder {
	b.routingTable = rt
	return b
}

// Build creates a new switch
func (b SwitchBuilder) Build(name string) *Switch {
	b.engineMustBeGiven()
	b.freqMustNotBeZero()
	b.routingTableMustBeGiven()
	b.arbiterMustBeGiven()

	s := &Switch{}
	s.TickingComponent = sim.NewTickingComponent(name, b.engine, b.freq, s)
	s.routingTable = b.routingTable
	s.arbiter = b.arbiter
	s.portToComplexMapping = make(map[sim.Port]portComplex)
	s.flitSwitchStart = make(map[string]sim.VTimeInSec)
	s.flitRouteStart = make(map[string]sim.VTimeInSec)
	s.flitForwardStart = make(map[string]sim.VTimeInSec)
	return s
}

func (b SwitchBuilder) engineMustBeGiven() {
	if b.engine == nil {
		panic("engine of switch is not given")
	}
}

func (b SwitchBuilder) freqMustNotBeZero() {
	if b.freq == 0 {
		panic("switch frequency cannot be 0")
	}
}

func (b SwitchBuilder) routingTableMustBeGiven() {
	if b.routingTable == nil {
		panic("switch requires a routing table to operate")
	}
}

func (b SwitchBuilder) arbiterMustBeGiven() {
	if b.arbiter == nil {
		panic("switch requires an arbiter to operate")
	}
}

// SwitchPortAdder can add a port to a switch.
type SwitchPortAdder struct {
	sw               *Switch
	localPort        sim.Port
	remotePort       sim.Port
	latency          int
	numInputChannel  int
	numOutputChannel int
	bufferSize       int
}

// MakeSwitchPortAdder creates a SwitchPortAdder that can add ports for the
// provided switch.
func MakeSwitchPortAdder(sw *Switch) SwitchPortAdder {
	return SwitchPortAdder{
		sw:               sw,
		numInputChannel:  1,
		numOutputChannel: 1,
		latency:          1,
	}
}

// WithPorts defines the ports to add. The local port is part of the switch.
// The remote port is the port on an endpoint or on another switch.
func (a SwitchPortAdder) WithPorts(local, remote sim.Port) SwitchPortAdder {
	a.localPort = local
	a.remotePort = remote
	return a
}

// WithLatency sets the latency of the port.
func (a SwitchPortAdder) WithLatency(latency int) SwitchPortAdder {
	a.latency = latency
	return a
}

// WithNumInputChannel sets the number of input channels of the port. This
// number determines the number of flits that can be injected into the switch
// from the port in each cycle.
func (a SwitchPortAdder) WithNumInputChannel(num int) SwitchPortAdder {
	a.numInputChannel = num
	return a
}

// WithNumOutputChannel sets the number of output channels of the port. This
// number determines the number of flits that can be ejected from the switch
// to the port in each cycle.
func (a SwitchPortAdder) WithNumOutputChannel(num int) SwitchPortAdder {
	a.numOutputChannel = num
	return a
}

// WithBufferSize sets the depth (in flits) of the port's route, forward, and
// send-out buffers. The depth never drops below the channel count, so the
// default (0) keeps buffers equal to the per-cycle channel count. The number
// of flits moved per cycle is still bounded by the channel counts.
func (a SwitchPortAdder) WithBufferSize(num int) SwitchPortAdder {
	a.bufferSize = num
	return a
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// AddPort adds the port to the switch.
func (a SwitchPortAdder) AddPort() {
	complexID := len(a.sw.ports)
	complexName := fmt.Sprintf("%s.PortComplex%d", a.sw.Name(), complexID)

	sendOutBuf := sim.NewBuffer(complexName+"SendOutBuf",
		maxInt(a.numOutputChannel, a.bufferSize))
	forwardBuf := sim.NewBuffer(complexName+"ForwardBuf",
		maxInt(a.numInputChannel, a.bufferSize))
	routeBuf := sim.NewBuffer(complexName+"RouteBuf",
		maxInt(a.numInputChannel, a.bufferSize))
	pipeline := pipelining.MakeBuilder().
		WithNumStage(a.latency).
		WithCyclePerStage(1).
		WithPipelineWidth(a.numInputChannel).
		WithPostPipelineBuffer(routeBuf).
		Build(a.localPort.Name() + ".Pipeline")

	pc := portComplex{
		localPort:        a.localPort,
		remotePort:       a.remotePort,
		pipeline:         pipeline,
		routeBuffer:      routeBuf,
		forwardBuffer:    forwardBuf,
		sendOutBuffer:    sendOutBuf,
		numInputChannel:  a.numInputChannel,
		numOutputChannel: a.numOutputChannel,
	}
	if BypassLatency > 0 && a.latency > BypassLatency {
		pc.bypass = newBypassState(a.localPort.Name(),
			a.numInputChannel, routeBuf)
	}

	a.sw.addPort(pc)
}
