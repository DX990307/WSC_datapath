package rdma

import (
	"fmt"

	"github.com/sarchlab/akita/v3/sim"
)

// ISCA'27 geo-push upper bound (-report-geo-push-bound). Statistics only:
// nothing here changes timing.
//
// Every line a requester R receives from an owner O (one "delivery") is a
// potential push: the owner could have multicast the same line to R's
// neighbors within radius r (1 or 2 hops, R and O excluded) and they would
// have kept it in their L2. A later remote read of the same line by such a
// neighbor N that actually went to the network (its own L2 missed and it did
// not merge into an in-flight entry) within T (1 us or 10 us) of the
// delivery is a read the push would have saved, unless N crossed a kernel
// boundary in between (the pushed copy would have been invalidated) or any
// GPM wrote the line remotely in between.
//
// Accounting (per radius r and window T):
//   - a neighbor read is credited to the earliest qualifying delivery only
//     (the first pushed copy is the one that would be installed);
//   - covered: neighbor network reads with delivery.t <= need <= delivery.t+T;
//   - late: neighbor reads with owner-send <= need < delivery.t (the push
//     would have caught the neighbor's own read in flight; not "covered");
//   - copies: neighbor copies a push sends (|targets|), used copies: distinct
//     neighbors credited per delivery; zero-use pushes: deliveries with no
//     credited neighbor (counted when the delivery expires);
//   - bytes x hops: a read costs (1 request flit + 6 response flits) x 16 B =
//     112 B per hop; a push costs 112 B per link the multicast tree adds to
//     the owner -> R route (as specified for the bound).
// Time is the time a line is needed (first waiter's arrival at the RDMA)
// for reads, and the arrival at R for deliveries.

const (
	geoBoundBytesPerHop = 112.0
	geoBoundRetainNS    = 60000.0 // keep deliveries this long (>= 10 us + queueing)
)

// GeoBoundWindowsNS are the windows T of the bound.
var GeoBoundWindowsNS = [2]float64{1000, 10000}

type geoLineKey struct {
	pid  uint32
	line uint64
}

type geoRec struct {
	key   geoLineKey
	t     float64 // arrival at R (ns)
	tSend float64 // owner send (ns)
	r, o  int
	used  [3][2]uint64 // radius (1, 2) x window: neighbors credited
	dead  bool
}

// GeoBoundStats are the bound's counters. Index [r][w]: radius r (1, 2;
// index 0 unused), window w (0: 1 us, 1: 10 us).
type GeoBoundStats struct {
	Deliveries      uint64
	NetReads        uint64
	NetReadBytesHop float64
	Writes          uint64
	WriteKills      uint64    // deliveries discarded by a remote write
	BoundaryRejects uint64    // candidate credits rejected by a kernel boundary
	Pushes          [3]uint64 // deliveries with at least one push target
	Copies          [3]uint64
	CostBytesHop    [3]float64
	Covered         [3][2]uint64
	Late            [3][2]uint64
	SavedBytesHop   [3][2]float64
	UsedCopies      [3][2]uint64
	ZeroUsePushes   [3][2]uint64
	PeakLive        uint64
}

// GeoBound is the platform-wide tracker (the simulation is single-threaded).
type GeoBound struct {
	topo         *GeoTopology
	lines        map[geoLineKey][]*geoRec
	fifo         []*geoRec
	head         int
	lastBoundary []float64
	stats        GeoBoundStats
}

var geoBound *GeoBound

// EnableGeoBound turns the bound on for the whole platform.
func EnableGeoBound(topo *GeoTopology) *GeoBound {
	g := &GeoBound{
		topo:         topo,
		lines:        make(map[geoLineKey][]*geoRec),
		lastBoundary: make([]float64, topo.NumGPMs()),
	}
	for i := range g.lastBoundary {
		g.lastBoundary[i] = -1
	}
	geoBound = g
	return g
}

// DisableGeoBound turns the bound off (tests).
func DisableGeoBound() { geoBound = nil }

func vtNS(t sim.VTimeInSec) float64 { return float64(t) * 1e9 }

func (g *GeoBound) expire(nowNS float64) {
	for g.head < len(g.fifo) {
		rec := g.fifo[g.head]
		if rec.t+geoBoundRetainNS >= nowNS {
			break
		}
		g.finalize(rec)
		g.fifo[g.head] = nil
		g.head++
	}
	if g.head > 4096 && g.head*2 > len(g.fifo) {
		g.fifo = append([]*geoRec(nil), g.fifo[g.head:]...)
		g.head = 0
	}
}

func (g *GeoBound) finalize(rec *geoRec) {
	if !rec.dead {
		list := g.lines[rec.key]
		if len(list) > 0 && list[0] == rec {
			list = list[1:]
		} else {
			for i, x := range list {
				if x == rec {
					list = append(list[:i:i], list[i+1:]...)
					break
				}
			}
		}
		if len(list) == 0 {
			delete(g.lines, rec.key)
		} else {
			g.lines[rec.key] = list
		}
	}
	for r := 1; r <= 2; r++ {
		if len(g.topo.PushTargets(rec.o, []int{rec.r}, r)) == 0 {
			continue
		}
		for w := range GeoBoundWindowsNS {
			if rec.used[r][w] == 0 {
				g.stats.ZeroUsePushes[r][w]++
			}
		}
	}
}

// Deliver records that requester r received line (pid, line) from owner o.
func (g *GeoBound) Deliver(r, o int, pid uint32, line uint64,
	sendNS, nowNS float64) {
	if r < 0 || o < 0 || r == o {
		return
	}
	g.expire(nowNS)
	rec := &geoRec{key: geoLineKey{pid, line}, t: nowNS, tSend: sendNS, r: r, o: o}
	g.lines[rec.key] = append(g.lines[rec.key], rec)
	g.fifo = append(g.fifo, rec)
	if live := uint64(len(g.fifo) - g.head); live > g.stats.PeakLive {
		g.stats.PeakLive = live
	}
	g.stats.Deliveries++
	for rad := 1; rad <= 2; rad++ {
		n := len(g.topo.PushTargets(o, []int{r}, rad))
		if n > 0 {
			g.stats.Pushes[rad]++
		}
		g.stats.Copies[rad] += uint64(n)
		g.stats.CostBytesHop[rad] +=
			float64(g.topo.ExtraPushLinks(o, r, rad)) * geoBoundBytesPerHop
	}
}

// NetRead records a demand read of (pid, line) by GPM n from owner o that
// went to the network; needNS is when the line was first needed.
func (g *GeoBound) NetRead(n, o int, pid uint32, line uint64,
	needNS, nowNS float64) {
	if n < 0 || o < 0 || n == o {
		return
	}
	g.expire(nowNS)
	hops := g.topo.Hops(n, o)
	g.stats.NetReads++
	g.stats.NetReadBytesHop += float64(hops) * geoBoundBytesPerHop
	list := g.lines[geoLineKey{pid, line}]
	if len(list) == 0 {
		return
	}
	bit := uint64(1) << uint(n)
	for rad := 1; rad <= 2; rad++ {
		for w, win := range GeoBoundWindowsNS {
			var best *geoRec
			late := false
			for _, rec := range list {
				if rec.r == n || rec.o == n ||
					g.topo.Hops(rec.r, n) > rad {
					continue
				}
				if needNS >= rec.t && needNS <= rec.t+win {
					if g.lastBoundary[n] >= rec.t {
						g.stats.BoundaryRejects++
						continue
					}
					best = rec
					break
				}
				if needNS >= rec.tSend && needNS < rec.t {
					late = true
				}
			}
			if best == nil {
				if late {
					g.stats.Late[rad][w]++
				}
				continue
			}
			g.stats.Covered[rad][w]++
			g.stats.SavedBytesHop[rad][w] += float64(hops) * geoBoundBytesPerHop
			if best.used[rad][w]&bit == 0 {
				best.used[rad][w] |= bit
				g.stats.UsedCopies[rad][w]++
			}
		}
	}
}

// Write discards the deliveries of a line written remotely.
func (g *GeoBound) Write(pid uint32, line uint64) {
	g.stats.Writes++
	key := geoLineKey{pid, line}
	for _, rec := range g.lines[key] {
		rec.dead = true
		g.stats.WriteKills++
	}
	delete(g.lines, key)
}

// KernelBoundary records a kernel launch at GPM gpm.
func (g *GeoBound) KernelBoundary(gpm int, nowNS float64) {
	if gpm >= 0 && gpm < len(g.lastBoundary) {
		g.lastBoundary[gpm] = nowNS
	}
}

// Finish finalizes the live deliveries and returns the counters.
func (g *GeoBound) Finish() GeoBoundStats {
	for g.head < len(g.fifo) {
		g.finalize(g.fifo[g.head])
		g.fifo[g.head] = nil
		g.head++
	}
	return g.stats
}

// Stats returns the counters so far.
func (g *GeoBound) Stats() GeoBoundStats { return g.stats }

// GeoBoundRows returns (name, value) rows of the bound for a CSV.
func GeoBoundRows(s GeoBoundStats) [][2]string {
	div := func(a, b float64) float64 {
		if b == 0 {
			return 0
		}
		return a / b
	}
	var rows [][2]string
	put := func(name string, v float64) {
		rows = append(rows, [2]string{name, fmt.Sprintf("%.6g", v)})
	}
	put("deliveries", float64(s.Deliveries))
	put("net_reads", float64(s.NetReads))
	put("net_read_bytes_hop", s.NetReadBytesHop)
	put("remote_writes", float64(s.Writes))
	put("write_kills", float64(s.WriteKills))
	put("boundary_rejects", float64(s.BoundaryRejects))
	put("peak_live_deliveries", float64(s.PeakLive))
	wname := [2]string{"1us", "10us"}
	for r := 1; r <= 2; r++ {
		p := fmt.Sprintf("r%d_", r)
		put(p+"pushes", float64(s.Pushes[r]))
		put(p+"copies", float64(s.Copies[r]))
		put(p+"copies_per_push", div(float64(s.Copies[r]), float64(s.Pushes[r])))
		put(p+"push_cost_bytes_hop", s.CostBytesHop[r])
		for w := range wname {
			q := p + wname[w] + "_"
			put(q+"covered_reads", float64(s.Covered[r][w]))
			put(q+"covered_frac", div(float64(s.Covered[r][w]), float64(s.NetReads)))
			put(q+"late_reads", float64(s.Late[r][w]))
			put(q+"saved_bytes_hop", s.SavedBytesHop[r][w])
			put(q+"saved_frac_bytes_hop", div(s.SavedBytesHop[r][w], s.NetReadBytesHop))
			put(q+"net_bytes_hop_change",
				div(s.CostBytesHop[r]-s.SavedBytesHop[r][w], s.NetReadBytesHop))
			put(q+"used_copies", float64(s.UsedCopies[r][w]))
			put(q+"used_neighbors_per_push",
				div(float64(s.UsedCopies[r][w]), float64(s.Pushes[r])))
			put(q+"useless_copy_frac",
				1-div(float64(s.UsedCopies[r][w]), float64(s.Copies[r])))
			put(q+"zero_use_push_frac",
				div(float64(s.ZeroUsePushes[r][w]), float64(s.Pushes[r])))
		}
	}
	return rows
}

// CurrentGeoBound returns the platform's tracker, or nil when off.
func CurrentGeoBound() *GeoBound { return geoBound }
