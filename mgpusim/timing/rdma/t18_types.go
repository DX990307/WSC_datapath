package rdma

import (
	"fmt"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/noc/messaging"
	"github.com/sarchlab/akita/v3/sim"
)

// T18Config configures the ISCA'27 T18 mechanism ("new m2": two-sided merge
// of remote requests). Everything is off in the zero value; with every field
// zero the RDMA engine behaves exactly like the baseline.
type T18Config struct {
	// ReqMerge enables the requester in-flight table merge: a remote 64-B line
	// read that finds an open in-flight entry for the same line joins its
	// waiter list instead of being sent.
	ReqMerge bool
	// OwnerMerge enables the owner merge table: remote reads of a line whose
	// owner-L2 read is already pending join that entry (one bit per requester
	// GPM); the line is read once from L2/HBM.
	OwnerMerge bool
	// OwnerMulticast sends a merged owner response as one multicast packet
	// along the routing tree; when false, one unicast packet per requester.
	OwnerMulticast bool
	// Window is the requester in-flight limit W; 0 keeps -rdma-max-outstanding.
	Window int
	// DistCredits divides W into per-destination credits proportional to the
	// modeled RTT (borrowing allowed); false keeps one shared pool.
	DistCredits bool
	// OwnerWindow is the owner-direction in-flight limit; 0 means Window when
	// Window > 0, else -rdma-max-outstanding.
	OwnerWindow int
	// ReqEntries / OwnerEntries are the table sizes in slots (0: 2 x window).
	ReqEntries   int
	OwnerEntries int
	// Ways is the number of slots per bucket of both tables (0: 4).
	Ways int
	// FingerprintBits is the tag width compared in parallel (0: 12).
	FingerprintBits int
	// MaxKicks bounds cuckoo displacement on insert (0: 32).
	MaxKicks int
	// ReqWaiters bounds the requester waiter pool (0: 4 x ReqEntries).
	ReqWaiters int
	// BypassWhenFull sends a request unmerged and untracked by the table when
	// the requester table is full and a window slot is free; otherwise the
	// request stalls at the RDMA input.
	BypassWhenFull bool
}

// RequesterActive reports whether the T18 requester engine replaces the
// baseline requester path.
func (c T18Config) RequesterActive() bool {
	return c.ReqMerge || c.Window > 0 || c.DistCredits
}

// Enabled reports whether any part of T18 is on.
func (c T18Config) Enabled() bool {
	return c.RequesterActive() || c.OwnerMerge
}

const t18MaxHops = 13 // 7x7 mesh: at most 12 hops

// T18Stats are the T18 counters (reported only when T18 is enabled).
type T18Stats struct {
	Enabled           bool
	RequesterActive   bool
	ReqMergeEnabled   bool
	OwnerMergeEnabled bool
	MulticastEnabled  bool
	DistCredits       bool
	Window            uint64
	OwnerWindow       uint64
	ReqTableSlots     uint64
	ReqWaiterCapacity uint64
	OwnerTableSlots   uint64

	// Requester.
	ReqAccepted          uint64 // requests accepted from L1 (merged or not)
	ReqMergeableReads    uint64 // full-line reads eligible for merging
	ReqMerged            uint64 // reads merged into an open entry
	ReqMergedInflight    uint64 // ... whose entry was already sent
	ReqMergedQueued      uint64 // ... whose entry still waited for a credit
	ReqNewEntries        uint64
	ReqClosedByWrite     uint64
	ReqTableFullStalls   uint64 // insert failures that stalled the request
	ReqBypasses          uint64 // insert failures sent untracked
	ReqWaiterFullStalls  uint64
	ReqSent              uint64 // network transactions sent
	ReqSentBorrowed      uint64 // ... sent on a borrowed credit
	ReqResponses         uint64
	ReqFanoutResponses   uint64 // responses to L1
	ReqPeakWaiters       uint64
	ReqPeakQueued        uint64
	ReqPeakInflight      uint64
	ReqTable             t18CuckooStats
	ReqQueueAreaNS       float64 // integral of queued requests over time
	ReqWindowFullNS      float64 // time with all W slots in flight
	ReqInflightAreaNS    float64 // integral of in-flight transactions
	ReqTimeNS            float64 // observation time (ns)
	ReqLatencyTotalNS    float64 // L1 arrival -> response to L1, all waiters
	HopDests             [t18MaxHops]uint64
	HopCredits           [t18MaxHops]uint64
	HopSent              [t18MaxHops]uint64
	HopMerged            [t18MaxHops]uint64
	HopInflightAreaNS    [t18MaxHops]float64
	HopRTTTotalNS        [t18MaxHops]float64 // send -> response, per transaction

	// Owner.
	OwnerReads              uint64 // remote full-line reads seen
	OwnerMerged             uint64
	OwnerL2Reads            uint64 // reads issued to L2 through the table
	OwnerTableFullFallbacks uint64
	OwnerSameRequester      uint64 // requester already in the bitmap: unicast path
	OwnerClosedByWrite      uint64
	OwnerSingleResponses    uint64
	OwnerMulticastPackets   uint64
	OwnerMulticastDests     uint64
	OwnerUnicastFanout      uint64 // unicast packets for merged entries (no multicast)
	OwnerFanoutHistogram    [49]uint64
	OwnerPeakLive           uint64
	OwnerTable              t18CuckooStats
}

// T18MulticastRsp is the owner's merged read response. It carries one 64-B
// line to several requester RDMA engines; switches split it along the routing
// tree (messaging.MulticastMsg) and each leaf is an ordinary DataReadyRsp.
type T18MulticastRsp struct {
	sim.MsgMeta
	Data   []byte
	Dsts   []sim.Port
	RspTos []string // RspTos[i] answers the request of Dsts[i]
}

var _ messaging.MulticastMsg = (*T18MulticastRsp)(nil)

// t18MulticastHeaderBytes: a 48-bit destination bitmap on top of the normal
// response header.
const t18MulticastHeaderBytes = 6

// Meta returns the message metadata.
func (m *T18MulticastRsp) Meta() *sim.MsgMeta { return &m.MsgMeta }

// MulticastDsts returns the remaining destinations.
func (m *T18MulticastRsp) MulticastDsts() []sim.Port { return m.Dsts }

// SplitForDsts returns the packet for a branch of the tree.
func (m *T18MulticastRsp) SplitForDsts(dsts []sim.Port) sim.Msg {
	rspTos := make([]string, 0, len(dsts))
	for _, d := range dsts {
		found := false
		for i, md := range m.Dsts {
			if md == d {
				rspTos = append(rspTos, m.RspTos[i])
				found = true
				break
			}
		}
		if !found {
			panic(fmt.Sprintf("multicast %s has no destination %s", m.ID, d.Name()))
		}
	}
	if len(dsts) == 1 {
		leaf := mem.DataReadyRspBuilder{}.
			WithSendTime(m.SendTime).
			WithSrc(m.Src).
			WithDst(dsts[0]).
			WithRspTo(rspTos[0]).
			WithData(append([]byte(nil), m.Data...)).
			Build()
		return leaf
	}
	child := &T18MulticastRsp{
		MsgMeta: m.MsgMeta,
		Data:    m.Data,
		Dsts:    append([]sim.Port(nil), dsts...),
		RspTos:  rspTos,
	}
	child.ID = sim.GetIDGenerator().Generate()
	child.Dst = dsts[0]
	return child
}

// t18Topology is the mesh position of every RDMA engine, used for hop counts
// (credits, statistics). Coordinates are (x, y) tiles of the 7x7 mesh.
type t18Topology struct {
	self  [2]int
	ports []sim.Port
	coord [][2]int
	index map[sim.Port]int
}

func t18Hops(a, b [2]int) int {
	dx := a[0] - b[0]
	if dx < 0 {
		dx = -dx
	}
	dy := a[1] - b[1]
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}
