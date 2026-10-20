package wgsteal

import (
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

// GPMInfo describes one GPM (index = driver GPU ID - 1).
type GPMInfo struct {
	X, Y  int
	Port  sim.Port // the CP's steal port on the mesh
	NumCU int
}

// GroupFunc maps a flattened WG ID to a WG group (benchmark hint).
type GroupFunc func(flat int) int

// Totals are run-wide statistics.
type Totals struct {
	Launches        int
	StealReqs       uint64
	StealsOK        uint64
	StealsNack      uint64
	WGsStolen       uint64
	NearSteals      uint64
	FarSteals       uint64
	LatSumNS        float64
	LatMaxNS        float64
	HopsSum         uint64
	AdvNearMsgs     uint64
	AdvFarMsgs      uint64
	ProbeEntries    uint64
	FilterDecided   uint64 // steals whose victim choice was decided by the overlap score
	AffinityDecided uint64 // decided by group / index affinity (no footprint)
	ScoreSum        float64
	ExactSum        float64
	ScoreCount      uint64
	GroupsRecorded  uint64
	GroupsKept      uint64
	GroupsReplaced  uint64
	GroupsEvicted   uint64
	WGsCompleted    uint64
}

type wgRec struct {
	l        *Launch
	flat     int
	group    int
	lastPage uint64
	hasLast  bool
}

type groupFP struct {
	kernel    string
	id        int
	home      int
	hist      *CuckooFilter
	cur       *CuckooFilter
	recording bool
	launchID  int
	lookups   int
	hits      int
	lastUse   uint64
}

type kernelHistory struct {
	groups map[int]*groupFP
}

// Coordinator holds the T19 state shared by the driver and every CP.
type Coordinator struct {
	mu   sync.Mutex
	cfg  Config
	gpms []GPMInfo
	hops [][]int

	rpf        []*RecentPages
	hist       map[string]*kernelHistory
	homeGroups [][]*groupFP
	useClock   uint64
	wgs        map[*kernels.WorkGroup]*wgRec

	nextLaunch int
	rows       []LaunchRow
	totals     Totals
}

// NewCoordinator creates the coordinator for the given GPMs.
func NewCoordinator(cfg Config, gpms []GPMInfo) (*Coordinator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	c := &Coordinator{
		cfg:  cfg,
		gpms: gpms,
		hist: map[string]*kernelHistory{},
		wgs:  map[*kernels.WorkGroup]*wgRec{},
	}
	n := len(gpms)
	c.hops = make([][]int, n)
	for i := range gpms {
		c.hops[i] = make([]int, n)
		for j := range gpms {
			c.hops[i][j] = abs(gpms[i].X-gpms[j].X) + abs(gpms[i].Y-gpms[j].Y)
		}
	}
	if cfg.Filter {
		c.rpf = make([]*RecentPages, n)
		for i := range c.rpf {
			c.rpf[i] = NewRecentPages(cfg.RPFLog2Buckets, cfg.FilterSlots, cfg.FPBits, cfg.RPFWindow)
		}
		c.homeGroups = make([][]*groupFP, n)
	}
	return c, nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// Config returns the configuration.
func (c *Coordinator) Config() Config { return c.cfg }

// NumGPMs returns the number of GPMs.
func (c *Coordinator) NumGPMs() int { return len(c.gpms) }

// Port returns the steal port of a GPM.
func (c *Coordinator) Port(gpm int) sim.Port { return c.gpms[gpm].Port }

// SetPort sets the steal port of a GPM.
func (c *Coordinator) SetPort(gpm int, p sim.Port) { c.gpms[gpm].Port = p }

// Hops returns the mesh distance between two GPMs.
func (c *Coordinator) Hops(a, b int) int { return c.hops[a][b] }

// Totals returns a copy of the run-wide statistics.
func (c *Coordinator) Totals() Totals {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.totals
}

// RecentPageStats returns the summed recent-page filter statistics.
func (c *Coordinator) RecentPageStats() (lookups, hits, falsePos, inserts, evictions, insertFails uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.rpf {
		lookups += r.Lookups
		hits += r.Hits
		falsePos += r.FalsePos
		inserts += r.Inserts
		evictions += r.Evictions
		insertFails += r.F.InsertFails
	}
	return
}

// Split returns the per-member WG boundaries (len = members+1) for a
// kernel of numWG WGs over GPMs with the given CU counts. "static" is
// MGPUSim's split (numCU * ceil(N / totalCU) per GPM, in order);
// "balanced" gives every GPM floor or ceil of N / GPMs, still contiguous.
func Split(mode string, numWG int, numCUs []int) []int {
	n := len(numCUs)
	dist := make([]int, n+1)
	if mode == "balanced" {
		base, extra := numWG/n, numWG%n
		for i := 0; i < n; i++ {
			k := base
			if i < extra {
				k++
			}
			dist[i+1] = dist[i] + k
		}
		return dist
	}
	total := 0
	for _, cu := range numCUs {
		total += cu
	}
	perCU := (numWG-1)/total + 1
	alloc := 0
	for i, cu := range numCUs {
		next := alloc + cu*perCU
		if next > numWG {
			next = numWG
		}
		dist[i+1] = next
		alloc = next
	}
	return dist
}

// RegisterWG maps a dispatched WG to its launch (needed for footprints).
func (c *Coordinator) RegisterWG(wg *kernels.WorkGroup, l *Launch, flat int) {
	if !c.cfg.Filter {
		return
	}
	c.mu.Lock()
	c.wgs[wg] = &wgRec{l: l, flat: flat, group: l.groupOf(flat)}
	c.mu.Unlock()
}

// WGDone records the completion of a WG of a steal-mode launch.
func (c *Coordinator) WGDone(l *Launch, wg *kernels.WorkGroup) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.wgs, wg)
	l.nCompleted++
	c.totals.WGsCompleted++
}

// OnRead records a vector load of a WG on a GPM (filter mode only). It is
// called from a passive hook at the CU when the load leaves the vector
// memory unit.
func (c *Coordinator) OnRead(gpm int, wg *kernels.WorkGroup, addr uint64) {
	if !c.cfg.Filter {
		return
	}
	page := addr >> 12
	c.mu.Lock()
	defer c.mu.Unlock()
	rec := c.wgs[wg]
	if rec == nil {
		return
	}
	if rec.hasLast && rec.lastPage == page {
		return
	}
	rec.lastPage, rec.hasLast = page, true
	c.rpf[gpm].Touch(page)
	g := c.groupFootprint(rec.l, rec.group, rec.flat, true)
	if g == nil {
		return
	}
	h := HashPage(page)
	if g.recording {
		if !g.hist.Lookup(h) {
			g.hist.Insert(h, page)
		}
		return
	}
	if g.cur.Lookup(h) {
		return
	}
	g.cur.Insert(h, page)
	g.lookups++
	if g.hist.Lookup(h) {
		g.hits++
	}
}

// groupFootprint returns the footprint entry of a group of l's kernel,
// creating it (and evicting the home GPM's least recently used entry) if
// create is set. On the first touch in a launch it starts recording (no
// history) or checking against the history.
func (c *Coordinator) groupFootprint(l *Launch, group, flat int, create bool) *groupFP {
	kh := c.hist[l.Name]
	if kh == nil {
		if !create {
			return nil
		}
		kh = &kernelHistory{groups: map[int]*groupFP{}}
		c.hist[l.Name] = kh
	}
	g := kh.groups[group]
	if g == nil {
		if !create {
			return nil
		}
		home := l.members[l.staticSlotOf(flat)]
		if len(c.homeGroups[home]) >= c.cfg.GroupTablePerGPM {
			c.evictLRU(home)
		}
		g = &groupFP{
			kernel:   l.Name,
			id:       group,
			home:     home,
			hist:     NewCuckooFilter(c.cfg.GroupLog2Buckets, c.cfg.FilterSlots, c.cfg.FPBits, c.cfg.RPFLog2Buckets),
			launchID: -1,
		}
		kh.groups[group] = g
		c.homeGroups[home] = append(c.homeGroups[home], g)
	}
	c.useClock++
	g.lastUse = c.useClock
	if create && g.launchID != l.ID {
		g.launchID = l.ID
		g.lookups, g.hits = 0, 0
		if g.hist.Count() == 0 {
			g.recording = true
		} else {
			g.recording = false
			if g.cur == nil {
				g.cur = NewCuckooFilter(c.cfg.GroupLog2Buckets, c.cfg.FilterSlots, c.cfg.FPBits, c.cfg.RPFLog2Buckets)
			} else {
				g.cur.Clear()
			}
		}
		l.touched = append(l.touched, g)
	}
	return g
}

func (c *Coordinator) evictLRU(home int) {
	list := c.homeGroups[home]
	best := 0
	for i, g := range list {
		if g.lastUse < list[best].lastUse {
			best = i
		}
	}
	g := list[best]
	delete(c.hist[g.kernel].groups, g.id)
	c.homeGroups[home] = append(list[:best], list[best+1:]...)
	c.totals.GroupsEvicted++
}

// finishHistory updates the group histories at the end of a launch: a
// group recorded for the first time keeps its footprint; a group whose
// pages mostly missed its history (fraction < ReplaceBelow) gets the new
// footprint (the old entries are deleted); otherwise the history is kept.
func (c *Coordinator) finishHistory(l *Launch) {
	for _, g := range l.touched {
		if g.launchID != l.ID {
			continue
		}
		if g.recording {
			g.recording = false
			c.totals.GroupsRecorded++
			continue
		}
		if g.lookups > 0 && float64(g.hits) < c.cfg.ReplaceBelow*float64(g.lookups) {
			g.hist, g.cur = g.cur, g.hist
			c.totals.GroupsReplaced++
		} else {
			c.totals.GroupsKept++
		}
		g.cur.Clear()
	}
	l.touched = nil
}

// NewLaunch creates the steal state of one unified kernel launch.
// members are the GPM indices of the unified device in order and dist the
// per-member WG boundaries (len(members)+1).
func (c *Coordinator) NewLaunch(name string, members []int, dist []int,
	groupFn GroupFunc, now sim.VTimeInSec) *Launch {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := newLaunch(c, c.nextLaunch, name, members, dist, groupFn, float64(now)*1e9)
	c.nextLaunch++
	c.totals.Launches++
	return l
}

// LaunchRow is one kernel x GPM row of the T19 statistics.
type LaunchRow struct {
	Launch, GPM                              int
	Name                                     string
	StaticWGs, Executed, StolenIn, StolenOut int
	StealReqs, StealsOK, StealsNack          int
	NearOK, FarOK                            int
	LatSumNS, LatMaxNS                       float64
	HopsSum                                  int
	StartNS, FirstPopNS, FinishNS            float64
}

// Rows returns a copy of the finished launches' rows.
func (c *Coordinator) Rows() []LaunchRow {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]LaunchRow(nil), c.rows...)
}

// WriteCSV writes the per-launch x GPM rows.
func (c *Coordinator) WriteCSV(w io.Writer) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := fmt.Fprintln(w, "launch,kernel,gpm,static_wgs,executed_wgs,stolen_in,stolen_out,"+
		"steal_reqs,steals_ok,steals_nack,near_ok,far_ok,steal_lat_sum_ns,steal_lat_max_ns,"+
		"steal_hops_sum,launch_ns,first_dispatch_ns,finish_ns"); err != nil {
		return err
	}
	rows := append([]LaunchRow(nil), c.rows...)
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Launch != rows[j].Launch {
			return rows[i].Launch < rows[j].Launch
		}
		return rows[i].GPM < rows[j].GPM
	})
	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "%d,%s,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d,%.1f,%.1f,%d,%.1f,%.1f,%.1f\n",
			r.Launch, r.Name, r.GPM, r.StaticWGs, r.Executed, r.StolenIn, r.StolenOut,
			r.StealReqs, r.StealsOK, r.StealsNack, r.NearOK, r.FarOK, r.LatSumNS, r.LatMaxNS,
			r.HopsSum, r.StartNS, r.FirstPopNS, r.FinishNS); err != nil {
			return err
		}
	}
	return nil
}
