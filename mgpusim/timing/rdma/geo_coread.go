package rdma

import (
	"fmt"
	"io"
)

// ISCA'27 affinity-push: co-read matrix (-report-coread-matrix). Statistics
// only: nothing here changes timing.
//
// For every demand remote read that goes to the network (the same events as
// the geo-push bound: own L2 missed, no merge into an own in-flight entry) by
// GPM n of line L at time t (when the line was first needed), every other GPM
// m that also read L from the network within |t - t_m| <= T (T = 1 us, 10 us)
// counts once as a co-read pair (n, m) -- per (n, m, line read), so a pair
// is counted on the later of the two reads. Pairs across a kernel boundary of
// n (a boundary between the two reads) or across a remote write to L are not
// counted. The matrix is symmetric by construction (each pair is counted on
// the later read, attributed to both orders).

const coReadRetainNS = 60000.0

type coReadRec struct {
	gpm int
	t   float64
}

// CoRead is the platform-wide co-read tracker.
type CoRead struct {
	topo         *GeoTopology
	lines        map[geoLineKey][]coReadRec
	lastBoundary []float64
	// Pairs[w][i][j]: co-reads of GPM i with GPM j within window w.
	Pairs  [2][][]uint64
	Reads  []uint64 // network reads per GPM
	CoHits [2][]uint64
	lastGC float64
}

var coRead *CoRead

// EnableCoRead turns the co-read matrix on.
func EnableCoRead(topo *GeoTopology) *CoRead {
	n := topo.NumGPMs()
	c := &CoRead{topo: topo, lines: make(map[geoLineKey][]coReadRec),
		lastBoundary: make([]float64, n), Reads: make([]uint64, n)}
	for i := range c.lastBoundary {
		c.lastBoundary[i] = -1
	}
	for w := range c.Pairs {
		c.Pairs[w] = make([][]uint64, n)
		for i := range c.Pairs[w] {
			c.Pairs[w][i] = make([]uint64, n)
		}
		c.CoHits[w] = make([]uint64, n)
	}
	coRead = c
	return c
}

// DisableCoRead turns it off (tests).
func DisableCoRead() { coRead = nil }

// CurrentCoRead returns the tracker or nil.
func CurrentCoRead() *CoRead { return coRead }

func (c *CoRead) gc(nowNS float64) {
	if nowNS-c.lastGC < 20000 {
		return
	}
	c.lastGC = nowNS
	for k, list := range c.lines {
		i := 0
		for i < len(list) && list[i].t+coReadRetainNS < nowNS {
			i++
		}
		if i == len(list) {
			delete(c.lines, k)
		} else if i > 0 {
			c.lines[k] = append([]coReadRec(nil), list[i:]...)
		}
	}
}

// NetRead records a network read of (pid, line) by GPM n needed at needNS.
func (c *CoRead) NetRead(n int, pid uint32, line uint64, needNS, nowNS float64) {
	if n < 0 {
		return
	}
	c.gc(nowNS)
	c.Reads[n]++
	key := geoLineKey{pid, line}
	list := c.lines[key]
	for w, win := range GeoBoundWindowsNS {
		var seen uint64
		for _, r := range list {
			if r.gpm == n || seen&(1<<uint(r.gpm)) != 0 {
				continue
			}
			lo, hi := r.t, needNS
			if lo > hi {
				lo, hi = hi, lo
			}
			if hi-lo > win {
				continue
			}
			// No kernel boundary of either GPM between the two reads.
			if c.lastBoundary[n] > lo || c.lastBoundary[r.gpm] > lo {
				continue
			}
			seen |= 1 << uint(r.gpm)
			c.Pairs[w][n][r.gpm]++
			c.Pairs[w][r.gpm][n]++
			c.CoHits[w][n]++
		}
	}
	c.lines[key] = append(list, coReadRec{gpm: n, t: needNS})
}

// Write forgets the reads of a written line.
func (c *CoRead) Write(pid uint32, line uint64) {
	delete(c.lines, geoLineKey{pid, line})
}

// KernelBoundary records a kernel launch at GPM g.
func (c *CoRead) KernelBoundary(g int, nowNS float64) {
	if g >= 0 && g < len(c.lastBoundary) {
		c.lastBoundary[g] = nowNS
	}
}

// WriteCSV writes window,i,j,tile_i,tile_j,hops,count (non-zero pairs) and
// one row per GPM with j = -1 holding its network reads.
func (c *CoRead) WriteCSV(w io.Writer) {
	fmt.Fprintln(w, "window_ns,gpm_i,gpm_j,tile_i,tile_j,hops,count")
	tile := func(g int) int {
		return c.topo.Coords[g][0] + c.topo.Coords[g][1]*c.topo.Width
	}
	for i, n := range c.Reads {
		fmt.Fprintf(w, "0,%d,-1,%d,-1,0,%d\n", i, tile(i), n)
	}
	for wi, win := range GeoBoundWindowsNS {
		for i := range c.Pairs[wi] {
			for j, v := range c.Pairs[wi][i] {
				if v == 0 {
					continue
				}
				fmt.Fprintf(w, "%.0f,%d,%d,%d,%d,%d,%d\n", win, i, j,
					tile(i), tile(j), c.topo.Hops(i, j), v)
			}
		}
	}
}
