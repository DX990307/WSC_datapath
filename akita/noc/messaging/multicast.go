package messaging

import "github.com/sarchlab/akita/v3/sim"

// MulticastMsg is a message that one source sends to several destinations
// (ISCA'27 T18 owner-side merged reads). It is injected once, travels as one
// packet along the routing tree of the network, and is replicated only at the
// switches where the destinations' routes diverge.
//
// The tree is the union of the deterministic unicast routes (the mesh routing
// table's dimension order), so every link of the tree carries the packet once.
//
// Meta().Dst must be one of MulticastDsts(); networks that do not know about
// multicast route it like a unicast message to that destination.
type MulticastMsg interface {
	sim.Msg

	// MulticastDsts returns the remaining destinations of this packet in a
	// fixed order.
	MulticastDsts() []sim.Port

	// SplitForDsts returns the message that continues toward dsts, a non-empty
	// subset of MulticastDsts() whose routes leave the current switch through
	// the same output port. With a single destination it returns an ordinary
	// unicast message delivered to that destination. A switch calls it once per
	// branch and reuses the result for all flits of the packet.
	SplitForDsts(dsts []sim.Port) sim.Msg
}
