package mesh

import (
	"fmt"
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

// mcTestMsg is a minimal multicast message for the T18 switch test.
type mcTestMsg struct {
	sim.MsgMeta
	dsts []sim.Port
}

func (m *mcTestMsg) Meta() *sim.MsgMeta        { return &m.MsgMeta }
func (m *mcTestMsg) MulticastDsts() []sim.Port { return m.dsts }
func (m *mcTestMsg) SplitForDsts(dsts []sim.Port) sim.Msg {
	if len(dsts) == 1 {
		leaf := &mcTestLeaf{MsgMeta: m.MsgMeta}
		leaf.ID = sim.GetIDGenerator().Generate()
		leaf.Dst = dsts[0]
		leaf.parent = m.ID
		return leaf
	}
	child := &mcTestMsg{MsgMeta: m.MsgMeta, dsts: append([]sim.Port(nil), dsts...)}
	child.ID = sim.GetIDGenerator().Generate()
	child.Dst = dsts[0]
	return child
}

type mcTestLeaf struct {
	sim.MsgMeta
	parent string
}

func (m *mcTestLeaf) Meta() *sim.MsgMeta { return &m.MsgMeta }

type mcSink struct {
	*sim.ComponentBase
	got []sim.Msg
}

func (s *mcSink) Handle(sim.Event) error { return nil }
func (s *mcSink) NotifyRecv(now sim.VTimeInSec, p sim.Port) {
	s.got = append(s.got, p.Retrieve(now))
}
func (s *mcSink) NotifyPortFree(sim.VTimeInSec, sim.Port) {}

type mcSendEvent struct {
	*sim.EventBase
	port sim.Port
	msg  sim.Msg
}

type mcSender struct{}

func (mcSender) Handle(e sim.Event) error {
	ev := e.(*mcSendEvent)
	if err := ev.port.Send(ev.msg); err != nil {
		panic("send failed")
	}
	return nil
}

// TestMulticastTreeDelivery sends one multicast packet on a 5x5 mesh and checks
// that every destination receives exactly one leaf (a unicast message addressed
// to it) and that no other tile receives anything.
func TestMulticastTreeDelivery(t *testing.T) {
	engine := sim.NewSerialEngine()
	connector := NewConnector().WithEngine(engine).WithFreq(1 * sim.GHz).
		WithSwitchLatency(2).WithBandwidth(4)
	connector.CreateNetwork("Mesh")
	sinks := map[[2]int]*mcSink{}
	ports := map[[2]int]sim.Port{}
	for x := 0; x < 5; x++ {
		for y := 0; y < 5; y++ {
			s := &mcSink{ComponentBase: sim.NewComponentBase(fmt.Sprintf("S%d%d", x, y))}
			p := sim.NewLimitNumMsgPort(s, 64, fmt.Sprintf("S%d%d.P", x, y))
			s.AddPort("P", p)
			connector.AddTile([3]int{x, y, 0}, []sim.Port{p})
			sinks[[2]int{x, y}] = s
			ports[[2]int{x, y}] = p
		}
	}
	connector.EstablishNetwork()

	src := ports[[2]int{2, 2}]
	targets := [][2]int{{0, 0}, {4, 0}, {2, 4}, {0, 2}, {4, 4}, {2, 0}}
	var dsts []sim.Port
	for _, c := range targets {
		dsts = append(dsts, ports[c])
	}
	msg := &mcTestMsg{
		MsgMeta: sim.MsgMeta{ID: "mc0", Src: src, Dst: dsts[0], TrafficBytes: 74},
		dsts:    dsts,
	}
	engine.Schedule(&mcSendEvent{
		EventBase: sim.NewEventBase(0, mcSender{}),
		port:      src, msg: msg,
	})
	engine.Run()

	for c, s := range sinks {
		want := 0
		for _, tc := range targets {
			if tc == c {
				want = 1
			}
		}
		if len(s.got) != want {
			t.Fatalf("tile %v received %d messages, want %d", c, len(s.got), want)
		}
		if want == 1 {
			leaf, ok := s.got[0].(*mcTestLeaf)
			if !ok || leaf.Dst != ports[c] {
				t.Fatalf("tile %v received %T", c, s.got[0])
			}
		}
	}
}
