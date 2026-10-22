package rdma

// ISCA'27 P19 Q1: hop-dependent prefetch depth (off by default).
//
// When a demand read leaves for the network (it bypassed the requester L2
// because SEEN/RESIDENT said "not here"), the requester also queues the next
// D lines of the same 4 KiB page as speculative lines. Like the existing
// prefetch, they only ride the demand's batch (same owner page): a line that
// finds no open batch, or a full batch, is dropped, so Q1 never sends an extra
// network transaction. D depends on the owner distance: D = Near for <= 2
// hops, Mid for 3-5, Far for >= 6. Lines already in the requester's
// in-flight table, written by this GPM (uncacheable), or issued by Q1 /
// demand in this kernel (an exact recent-line set of RecentLines entries,
// cleared at kernel boundaries) are skipped. Owners that receive at least
// HotShare of this requester's recent network lines are "hot" and get no
// extra depth (the hot corner owner is already the bottleneck).
//
// A Q1 line is filled into the requester L2 like a first-touch speculative
// line (invalid ways only unless a demand read joins it), and is invalidated
// at kernel boundaries with every other replica (M3 / A1 rules unchanged).

import (
	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/sim"
)

// Q1Config configures the hop-dependent depth.
type Q1Config struct {
	Near, Mid, Far int     // extra lines per demand by distance band
	HotShare       float64 // owner share above which no extra lines (0 = off)
	RecentLines    int     // recent-line set size
}

// Enabled reports whether Q1 adds any line.
func (q Q1Config) Enabled() bool { return q.Near > 0 || q.Mid > 0 || q.Far > 0 }

// Q1Stats are the Q1 counters.
type Q1Stats struct {
	Demands, Queued, SkipInflight, SkipRecent, SkipUncacheable, SkipHot,
	SkipCapacity, SkipFilter, Useful, Unused uint64
}

type q1State struct {
	cfg        Q1Config
	recent     map[uint64]struct{}
	recentFIFO []uint64
	ownerLines []float64
	total      float64
	stats      Q1Stats
}

// ConfigureQ1 turns Q1 on for this engine (needs the T18 topology).
func (c *Comp) ConfigureQ1(cfg Q1Config) {
	if !cfg.Enabled() {
		c.q1 = nil
		return
	}
	if cfg.RecentLines <= 0 {
		cfg.RecentLines = 8192
	}
	c.q1 = &q1State{cfg: cfg, recent: map[uint64]struct{}{}}
}

// Q1Result returns the Q1 counters (nil when off).
func (c *Comp) Q1Result() *Q1Stats {
	if c.q1 == nil {
		return nil
	}
	s := c.q1.stats
	return &s
}

func (c *Comp) q1Boundary() {
	if c.q1 == nil {
		return
	}
	c.q1.recent = map[uint64]struct{}{}
	c.q1.recentFIFO = c.q1.recentFIFO[:0]
}

func (q *q1State) remember(line uint64) {
	if _, ok := q.recent[line]; ok {
		return
	}
	if len(q.recentFIFO) >= q.cfg.RecentLines {
		delete(q.recent, q.recentFIFO[0])
		q.recentFIFO = q.recentFIFO[1:]
	}
	q.recent[line] = struct{}{}
	q.recentFIFO = append(q.recentFIFO, line)
}

// q1Depth returns the extra lines for a demand to dst and records the owner.
func (c *Comp) q1Depth(dst sim.Port) int {
	q := c.q1
	if c.t18Topo == nil {
		return 0
	}
	idx, ok := c.t18Topo.index[dst]
	if !ok {
		return 0
	}
	if len(q.ownerLines) < len(c.t18Topo.coord) {
		q.ownerLines = make([]float64, len(c.t18Topo.coord))
	}
	q.ownerLines[idx]++
	q.total++
	if q.total >= 4096 {
		for i := range q.ownerLines {
			q.ownerLines[i] /= 2
		}
		q.total /= 2
	}
	if q.cfg.HotShare > 0 && q.total >= 256 &&
		q.ownerLines[idx]/q.total >= q.cfg.HotShare {
		q.stats.SkipHot++
		return 0
	}
	h := t18Hops(c.t18Topo.self, c.t18Topo.coord[idx])
	switch {
	case h <= 2:
		return q.cfg.Near
	case h <= 5:
		return q.cfg.Mid
	default:
		return q.cfg.Far
	}
}

// q1QueueDepth queues the extra lines after a demand entry that goes to the
// network without an L2 probe (it is already in remotePendingBatch).
func (c *Comp) q1QueueDepth(
	now sim.VTimeInSec,
	demand *remoteLineEntry,
	dst sim.Port,
) {
	q := c.q1
	if q == nil || demand == nil {
		return
	}
	q.stats.Demands++
	base := demand.key.remoteLineIdentity
	q.remember(base.lineAddr)
	d := c.q1Depth(dst)
	page := remotePageAddress(base.lineAddr)
	for j := 1; j <= d; j++ {
		line := base.lineAddr + uint64(j)*remoteLineBytes
		if remotePageAddress(line) != page {
			break
		}
		id := base
		id.lineAddr = line
		if c.remoteUncacheable[id] {
			q.stats.SkipUncacheable++
			continue
		}
		if _, ok := q.recent[line]; ok {
			q.stats.SkipRecent++
			continue
		}
		key := remoteLineKey{remoteLineIdentity: id, epoch: c.remoteEpochs[id]}
		if c.remoteLines[key] != nil {
			q.stats.SkipInflight++
			continue
		}
		if len(c.remoteLines) >= c.remoteOutstandingCapacity() {
			q.stats.SkipCapacity++
			return
		}
		filter := c.requestFilterForAddress(line)
		if filter == nil || !filter.TryScheduleUpdate(now,
			remoteTypedFilterKey(id, writeback.FilterPending), false) {
			q.stats.SkipFilter++
			continue
		}
		entry := c.newRemoteDemandEntry(key, dst, demand.info)
		entry.state = remoteLinePendingBatch
		entry.speculative = true
		entry.q1 = true
		entry.admit = true
		c.insertRemoteLine(key, entry)
		c.remotePendingBatch = append(c.remotePendingBatch, entry)
		q.remember(line)
		q.stats.Queued++
	}
}
