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
	// modeled RTT (borrowing allowed); false keeps one shared pool. It is the
	// credit policy rtt when CreditPolicy is T18CreditShared (the zero value).
	DistCredits bool
	// CreditPolicy selects shared / rtt / near (t18_credits.go). The zero
	// value (shared) defers to DistCredits, so existing configurations keep
	// their behavior.
	CreditPolicy T18CreditPolicy
	// NearReserve (near only) is the number of slots of W reserved for
	// destinations at most NearHops hops away.
	NearReserve int
	NearHops    int
	// ExtraStats enables the ISCA'27 v3 statistics (distance groups, near
	// zone, owner table full time); statistics only, no timing change.
	ExtraStats bool
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
	// ComposeLineEntries (M1 composed with M2 packing only) sets the
	// requester line table to exactly this many line entries; the M1
	// in-flight index, the waiter pool (ReqWaiters, else 4 x entries) and
	// the open-batch limit follow it. 0 keeps the old sizing
	// max(ReqEntries or 2W, M2's 2 x batches x batch-lines) = 1024 at W=64.
	ComposeLineEntries int
	// BypassWhenFull sends a request unmerged and untracked by the table when
	// the requester table is full and a window slot is free; otherwise the
	// request stalls at the RDMA input.
	BypassWhenFull bool
}

// RequesterActive reports whether the T18 requester engine replaces the
// baseline requester path.
func (c T18Config) RequesterActive() bool {
	return c.ReqMerge || c.Window > 0 || c.DistCredits ||
		c.CreditPolicy != T18CreditShared
}

// EffectiveCreditPolicy resolves CreditPolicy and the older DistCredits.
func (c T18Config) EffectiveCreditPolicy() T18CreditPolicy {
	if c.CreditPolicy != T18CreditShared {
		return c.CreditPolicy
	}
	if c.DistCredits {
		return T18CreditRTT
	}
	return T18CreditShared
}

// Enabled reports whether any part of T18 is on.
func (c T18Config) Enabled() bool {
	return c.RequesterActive() || c.OwnerMerge
}

const t18MaxHops = 17 // 9x9 mesh: at most 16 hops (7x7: 12)

// T18Stats are the T18 counters (reported only when T18 is enabled).
type T18Stats struct {
	Enabled           bool
	RequesterActive   bool
	ReqMergeEnabled   bool
	OwnerMergeEnabled bool
	MulticastEnabled  bool
	DistCredits       bool
	CreditPolicy      T18CreditPolicy
	NearReserve       uint64
	NearHops          uint64
	ExtraStats        bool
	Window            uint64
	OwnerWindow       uint64
	ReqTableSlots     uint64
	ReqWaiterCapacity uint64
	OwnerTableSlots   uint64

	// Requester.
	ReqAccepted         uint64 // requests accepted from L1 (merged or not)
	ReqMergeableReads   uint64 // full-line reads eligible for merging
	ReqMerged           uint64 // reads merged into an open entry
	ReqMergedInflight   uint64 // ... whose entry was already sent
	ReqMergedQueued     uint64 // ... whose entry still waited for a credit
	ReqNewEntries       uint64
	ReqClosedByWrite    uint64
	ReqTableFullStalls  uint64 // requests stalled by a full table (distinct)
	ReqBypasses         uint64 // insert failures sent untracked
	ReqWaiterFullStalls uint64 // requests stalled by a full waiter pool (distinct)
	ReqStallRetries     uint64 // failed accept attempts (about one per cycle)
	ReqSent             uint64 // network transactions sent
	ReqSentBorrowed     uint64 // ... sent on a borrowed credit
	ReqResponses        uint64
	ReqFanoutResponses  uint64 // responses to L1
	ReqPeakWaiters      uint64
	ReqPeakQueued       uint64
	ReqPeakInflight     uint64
	ReqTable            t18CuckooStats
	ReqQueueAreaNS      float64 // integral of queued requests over time
	ReqWindowFullNS     float64 // time with all W slots in flight
	ReqInflightAreaNS   float64 // integral of in-flight transactions
	ReqTimeNS           float64 // observation time (ns)
	ReqLatencyTotalNS   float64 // L1 arrival -> response to L1, all waiters
	HopDests            [t18MaxHops]uint64
	HopCredits          [t18MaxHops]uint64
	HopSent             [t18MaxHops]uint64
	HopMerged           [t18MaxHops]uint64
	HopInflightAreaNS   [t18MaxHops]float64
	HopRTTTotalNS       [t18MaxHops]float64 // send -> response, per transaction

	// ISCA'27 v3 statistics (ExtraStats). Distance groups: near <= 3 hops,
	// mid 4-6, far >= 7 (t18DistGroup).
	GroupInflightAreaNS  [3]float64 // integral of in-flight transactions
	GroupQueuedAreaNS    [3]float64 // integral of queued requests/batches
	GroupQueuedFullNS    [3]float64 // ... while the window is full
	GroupWaitFullTimeNS  [3]float64 // time the window is full and the group has queued work
	NearZoneAreaNS       float64    // integral of near-zone slots in use
	NearZoneFullNS       float64    // time the near zone is full
	NearZoneSends        uint64     // transactions sent in the near zone
	NearOverflowSends    uint64     // near destinations sent in the general zone (near zone full)
	NearOverflowBorrowed uint64     // ... of which on a borrowed general credit

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
	OwnerBatchChildReads    uint64 // M1+M2: bitmap child reads that opened an entry
	OwnerBatchChildMerged   uint64 // M1+M2: bitmap child reads merged into an entry
	Composed                bool   // M1 requester runs on top of M2
	OwnerPeakLive           uint64
	OwnerTable              t18CuckooStats
	// ISCA'27 v3 statistics (ExtraStats).
	OwnerTimeNS      float64 // observation time of the owner table
	OwnerTableAreaNS float64 // integral of owner table entries
	OwnerTableFullNS float64 // time with every owner table slot in use
}

// T18MulticastRsp is the owner's merged read response. It carries one 64-B
// line to several requester RDMA engines; switches split it along the routing
// tree (messaging.MulticastMsg) and each leaf is an ordinary DataReadyRsp.
type T18MulticastRsp struct {
	sim.MsgMeta
	Data   []byte
	Dsts   []sim.Port
	RspTos []string // RspTos[i] answers the request of Dsts[i]
	// Lines (M1+M2 only; nil otherwise): Lines[i] >= 0 means Dsts[i] asked
	// for this line as line Lines[i] of the M2 bitmap request RspTos[i]; its
	// leaf is a one-line BitmapReadRsp. -1: an ordinary DataReadyRsp leaf.
	Lines []int
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
	var lines []int
	if m.Lines != nil {
		lines = make([]int, 0, len(dsts))
	}
	for _, d := range dsts {
		found := false
		for i, md := range m.Dsts {
			if md == d {
				rspTos = append(rspTos, m.RspTos[i])
				if m.Lines != nil {
					lines = append(lines, m.Lines[i])
				}
				found = true
				break
			}
		}
		if !found {
			panic(fmt.Sprintf("multicast %s has no destination %s", m.ID, d.Name()))
		}
	}
	if len(dsts) == 1 && lines != nil && lines[0] >= 0 {
		return &BitmapReadRsp{
			MsgMeta: sim.MsgMeta{
				ID:           sim.GetIDGenerator().Generate(),
				Src:          m.Src,
				Dst:          dsts[0],
				SendTime:     m.SendTime,
				TrafficBytes: bitmapRspOverhead + len(m.Data),
			},
			RespondTo: rspTos[0],
			LineData: map[uint64][]byte{
				uint64(lines[0]): append([]byte(nil), m.Data...),
			},
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
		Lines:   lines,
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

// t18DistGroup maps a hop count to the ISCA'27 v3 distance group: 0 near
// (<= 3 hops), 1 mid (4-6), 2 far (>= 7).
func t18DistGroup(h int) int {
	switch {
	case h <= 3:
		return 0
	case h <= 6:
		return 1
	}
	return 2
}
