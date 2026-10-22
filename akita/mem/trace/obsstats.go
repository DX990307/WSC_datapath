package trace

// ISCA'27 P13 observation statistics (statistics only, all off by default).
//
// Every hook in this file is passive: it reads values the caller already has,
// never sends a message, never schedules an event and never changes any
// component state, so the simulated timing is identical with the statistics
// on or off. When a statistic is off its hook returns after one bool check.
// Nothing is written per request: everything is aggregated online into
// counters and log-scale histograms (requests are kept only while in flight).
//
//   - Latency (E1, new O2a): every remote read is cut at
//       t0 the requester network interface (RDMA) receives it from the L1V
//          (RecvTime at the RDMA ToL1 port),
//       t1 its network packet leaves the requester RDMA (it holds an RRT
//          entry / window slot from now on),
//       t2 the packet reaches the owner RDMA (RecvTime at ToOutside),
//       t3 the owner RDMA forwards the line read to its L2 (or, with the M1
//          owner merge, joins an L2 read already pending for the line),
//       t4 the L2/HBM response reaches the owner RDMA (RecvTime at ToL2),
//       t4s the owner sends the response packet (unicast, bitmap or
//          multicast),
//       t5 the response reaches the requester RDMA (RecvTime at ToOutside;
//          per destination for a multicast: each leaf has its own t5),
//       t6 the requester RDMA sends the data to the L1V.
//     A logical read that joins a request already in flight (M1 requester
//     merge, M2 de-duplication) gets merge_wait = t5 - t0 instead of the
//     t0..t5 segments; a read served without a network trip after it arrived
//     (M3 requester-L2 hit, M2 ready line) is "local".
//   - Requester aggregation opportunity (new O5, HPCA Fig. 5 definition):
//     in-flight same-line duplicates at RDMA admission, same-line repeats
//     within W cycles, and the nearest earlier different line of the same
//     (requester, owner, 4 KB page, write epoch) within 0..64 cycles; plus the
//     lines that actually left in one cycle to the same owner page.
//   - Remote reuse (new O6): per (requester GPM, line, write epoch) at RDMA
//     admission; reuse intervals; multicast deliveries re-read by the same
//     GPM. L2 free capacity: time a way is invalid or holds a line that is
//     not read again before it is replaced.
//   - Owner side: same-line concurrency when a read reaches the owner.
//   - Links (O2b/E5): flits per directed mesh link, total and per window.

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/sarchlab/akita/v3/sim"
)

// ObsConfig selects the P13 statistics.
type ObsConfig struct {
	Latency   bool // E1 latency segments (+ owner concurrency)
	ReqAgg    bool // requester aggregation opportunity
	Reuse     bool // remote reuse + L2 free capacity
	Links     bool // per-link utilization
	TileWidth int  // mesh width (7)
	// CycleNS is the RDMA clock period used for the aggregation windows.
	CycleNS float64
	// AggWindowCycles is the same-line / same-page window of ReqAgg (16).
	AggWindowCycles int
	// WindowNS is the link-utilization window (1000 ns).
	WindowNS float64
	// LinkBytesPerNS is the capacity of one directed link.
	LinkBytesPerNS float64
	// FlitBytes is the bytes accounted per flit on a link.
	FlitBytes int
	// PerOwner (ISCA'27 P19 -report-qt-diag) also splits the Latency
	// segments by owner tile into <prefix>_latency_owner.csv.
	PerOwner bool
}

var (
	obsMu       sync.Mutex
	obsS        *obsState
	obsOn       bool // any requester/owner statistic
	obsLinksOn  bool
	obsL2FreeOn bool
)

// EnableObsStats turns the P13 statistics on (before the simulation starts).
func EnableObsStats(cfg ObsConfig) {
	obsMu.Lock()
	defer obsMu.Unlock()
	if cfg.TileWidth <= 0 {
		cfg.TileWidth = 7
	}
	if cfg.CycleNS <= 0 {
		cfg.CycleNS = 1
	}
	if cfg.AggWindowCycles <= 0 {
		cfg.AggWindowCycles = 16
	}
	if cfg.WindowNS <= 0 {
		cfg.WindowNS = 1000
	}
	if cfg.FlitBytes <= 0 {
		cfg.FlitBytes = 16
	}
	obsS = newObsState(cfg)
	obsOn = cfg.Latency || cfg.ReqAgg || cfg.Reuse
	obsLinksOn = cfg.Links
	obsL2FreeOn = cfg.Reuse
}

// DisableObsStats turns every P13 statistic off.
func DisableObsStats() {
	obsMu.Lock()
	defer obsMu.Unlock()
	obsS = nil
	obsOn, obsLinksOn, obsL2FreeOn = false, false, false
}

// ObsEnabled reports whether the requester/owner hooks are active.
func ObsEnabled() bool { return obsOn }

// ObsLinksEnabled reports whether the link hook is active.
func ObsLinksEnabled() bool { return obsLinksOn }

// ObsL2FreeEnabled reports whether the L2 free-capacity hooks are active.
func ObsL2FreeEnabled() bool { return obsL2FreeOn }

func obsNS(t sim.VTimeInSec) float64 { return float64(t) * 1e9 }

// ---------------------------------------------------------------------------
// log-scale histogram (8 bins per octave of ns + 1)

const obsHistBins = 256

type obsHist struct {
	n   uint64
	sum float64
	bin [obsHistBins]uint64
}

func obsHistIndex(v float64) int {
	if v < 0 {
		v = 0
	}
	i := int(math.Log2(v+1) * 8)
	if i >= obsHistBins {
		i = obsHistBins - 1
	}
	return i
}

func (h *obsHist) add(v float64) {
	if v < 0 {
		v = 0
	}
	h.n++
	h.sum += v
	h.bin[obsHistIndex(v)]++
}

func (h *obsHist) merge(o *obsHist) {
	h.n += o.n
	h.sum += o.sum
	for i := range h.bin {
		h.bin[i] += o.bin[i]
	}
}

func (h *obsHist) mean() float64 {
	if h.n == 0 {
		return 0
	}
	return h.sum / float64(h.n)
}

// quantile returns the geometric centre of the bin holding the q-quantile
// (relative resolution about 9%).
func (h *obsHist) quantile(q float64) float64 {
	if h.n == 0 {
		return 0
	}
	rank := uint64(math.Ceil(q * float64(h.n)))
	if rank < 1 {
		rank = 1
	}
	var c uint64
	for i, b := range h.bin {
		c += b
		if c >= rank {
			return math.Pow(2, (float64(i)+0.5)/8) - 1
		}
	}
	return math.Pow(2, float64(obsHistBins)/8) - 1
}

// ---------------------------------------------------------------------------
// state

const obsMaxHops = 16

const (
	obsSegWait = iota
	obsSegFwd
	obsSegOwnerQueue
	obsSegSvcHit
	obsSegSvcMiss
	obsSegSvcMerged
	obsSegOwnerRspWait
	obsSegRetNet
	obsSegRet
	obsSegToL1
	obsSegMergeWait
	obsSegLocal
	obsSegRTT
	obsSegTotal
	obsNumSeg
)

var obsSegNames = [obsNumSeg]string{
	"wait_rrt", "fwd", "owner_queue", "owner_service_hit",
	"owner_service_miss", "owner_service_merged", "owner_rsp_wait",
	"ret_net", "ret", "to_l1v", "merge_wait", "local", "rtt", "total",
}

const (
	obsClsUnknown int8 = iota
	obsClsHit
	obsClsMiss
	obsClsMSHRHit
)

type obsHopStats struct {
	seg            [obsNumSeg]obsHist
	n              uint64 // completed logical remote reads
	nNet           uint64 // served by a packet sent after they arrived
	nMergedQueued  uint64 // ... and not the first read of that packet
	nMergedFlight  uint64 // joined a request already in the network
	nLocal         uint64 // served without a network trip after arrival
	nIncomplete    uint64 // network path without all owner timestamps
	nOwnerHit      uint64
	nOwnerMiss     uint64
	nOwnerMSHRHit  uint64
	nOwnerMerged   uint64
	nOwnerUnknown  uint64
	nMulticastLeaf uint64
}

type obsLineKey struct {
	g    int32
	line uint64 // address >> 6
}

type obsNetKey struct {
	id   string
	line uint64
}

// obsNetLine is one line of one network request packet.
type obsNetLine struct {
	g, owner       int
	t1, t2, t3, t4 float64
	t4s            float64
	cls            int8
	ownerMerged    bool
	multicast      bool
	ownerActive    bool // counted in the owner concurrency map
	lineAddr       uint64
}

// obsReqLine is the requester state of one (GPM, line).
type obsReqLine struct {
	active      int32
	lastAdmitNS float64
	hasNet      bool
	lastArrive  float64 // t5 of the latest response for this line
	net         obsNetLine
	netClaimed  bool // a read already completed as this packet's first
}

type obsLogical struct {
	g, owner int
	line     uint64
	t0       float64
	bytes    uint64
}

type obsPageKey struct {
	g     int32
	owner int32
	page  uint64
	epoch uint32
}

type obsPageState struct {
	latestLine, otherLine uint64
	latestNS, otherNS     float64
}

type obsOwnLine struct {
	total int
	reqs  map[int]int
}

type obsPackKey struct {
	owner int
	page  uint64
}

type obsPackState struct {
	cycle   int64
	started bool
	groups  map[obsPackKey]int
}

type obsL2Cache struct {
	name         string
	sets, ways   int
	valid        []bool
	lastTouch    []float64
	invalidSince []float64
	invalidNS    float64
	deadNS       float64
	fills        uint64
	evictsDead   uint64 // lifetimes with no read after the fill
	lifetimes    uint64
	readHits     uint64
}

type obsLink struct {
	src, dst string
	flits    uint64
	curWin   int64
	curFlits uint64
	maxWin   uint64
}

var obsAggThresholds = [...]int{0, 1, 2, 4, 8, 16, 32, 64}

type obsState struct {
	cfg ObsConfig

	tile map[string]int

	logical map[string]*obsLogical
	reqLine map[obsLineKey]*obsReqLine
	net     map[obsNetKey]*obsNetLine
	l2      map[string][]*obsNetLine
	l2cls   map[string]int8 // results seen before the line was attached
	own     map[uint64]*obsOwnLine

	hop        [obsMaxHops]obsHopStats
	ownerStats [64]obsHopStats // by owner tile (cfg.PerOwner)
	writesSeen uint64
	bytesByGPM map[int]uint64
	readsByGPM map[int]uint64

	// owner concurrency
	ownReads           uint64
	ownConcurrent      uint64
	ownConcurrentOther uint64

	// requester aggregation
	aggReads       uint64
	aggExact       uint64
	aggDupWindow   uint64
	aggSamePage    [len(obsAggThresholds)]uint64
	aggSibling     [len(obsAggThresholds)]uint64
	pageState      map[obsPageKey]*obsPageState
	siblingState   map[obsPageKey]*obsPageState
	pagePruneAt    uint64
	epoch          map[uint64]uint32
	pack           map[int]*obsPackState
	sentLines      uint64
	sentPackets    uint64
	packableLines  uint64
	multiLinePkts  uint64
	multiLineLines uint64

	// reuse
	reuse              map[uint64][2]uint32 // key -> (count, last t0 in ns)
	reuseOverflow      map[obsReuseKey][2]float64
	reuseReads         uint64
	reuseRetouch       uint64
	reuseRetouchIdle   uint64 // previous reads of this GPM already completed
	reuseInterval      obsHist
	mcastDelivered     map[obsLineKey]float64
	mcastDeliveries    uint64
	mcastReread        uint64
	mcastDeliveredOpen uint64

	// L2 free capacity
	l2caches []*obsL2Cache

	// links
	links    []*obsLink
	linkIdx  map[string]int
	winUtil  obsHist // per (link, window) utilization in permille
	meshWin  map[int64]uint64
	lastFlit float64
}

type obsReuseKey struct {
	g     int32
	line  uint64
	epoch uint32
}

func newObsState(cfg ObsConfig) *obsState {
	return &obsState{
		cfg:            cfg,
		tile:           map[string]int{},
		logical:        map[string]*obsLogical{},
		reqLine:        map[obsLineKey]*obsReqLine{},
		net:            map[obsNetKey]*obsNetLine{},
		l2:             map[string][]*obsNetLine{},
		l2cls:          map[string]int8{},
		own:            map[uint64]*obsOwnLine{},
		bytesByGPM:     map[int]uint64{},
		readsByGPM:     map[int]uint64{},
		pageState:      map[obsPageKey]*obsPageState{},
		siblingState:   map[obsPageKey]*obsPageState{},
		epoch:          map[uint64]uint32{},
		pack:           map[int]*obsPackState{},
		reuse:          map[uint64][2]uint32{},
		reuseOverflow:  map[obsReuseKey][2]float64{},
		mcastDelivered: map[obsLineKey]float64{},
		linkIdx:        map[string]int{},
		meshWin:        map[int64]uint64{},
	}
}

func (s *obsState) tileOf(name string) int {
	if t, ok := s.tile[name]; ok {
		return t
	}
	t := parseGPMID(name)
	s.tile[name] = t
	return t
}

func (s *obsState) hops(a, b int) int {
	h := manhattanHops(a, b, s.cfg.TileWidth)
	if h < 0 {
		return 0
	}
	if h >= obsMaxHops {
		h = obsMaxHops - 1
	}
	return h
}

// ---------------------------------------------------------------------------
// requester admission (t0)

// ObsAdmit records a remote request received by the requester RDMA from the
// L1V (first processing attempt); t0 is its arrival at the RDMA port.
func ObsAdmit(
	requester, owner string,
	id string,
	addr, bytes, pid uint64,
	isRead bool,
	t0 sim.VTimeInSec,
) {
	if !obsOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil {
		return
	}
	g := s.tileOf(requester)
	o := s.tileOf(owner)
	line := addr >> 6
	if !isRead {
		s.writesSeen++
		last := line
		if bytes > 0 {
			last = (addr + bytes - 1) >> 6
		}
		for l := line; l <= last; l++ {
			s.epoch[l]++
		}
		return
	}
	now := obsNS(t0)
	k := obsLineKey{g: int32(g), line: line}
	st := s.reqLine[k]
	ep := s.epoch[line]

	if s.cfg.ReqAgg {
		s.admitAgg(g, o, line, ep, now, st)
	}
	if s.cfg.Reuse {
		s.admitReuse(g, line, ep, now, st)
	}

	if st == nil {
		st = &obsReqLine{}
		s.reqLine[k] = st
	}
	st.active++
	st.lastAdmitNS = now
	s.logical[id] = &obsLogical{g: g, owner: o, line: line, t0: now, bytes: bytes}
}

func (s *obsState) admitAgg(g, o int, line uint64, ep uint32, now float64,
	st *obsReqLine) {
	s.aggReads++
	exact := st != nil && st.active > 0
	if exact {
		s.aggExact++
	}
	win := float64(s.cfg.AggWindowCycles) * s.cfg.CycleNS
	if st != nil && st.lastAdmitNS > 0 && now-st.lastAdmitNS <= win {
		s.aggDupWindow++
	}
	if exact {
		// HPCA definition: exact in-flight duplicates belong to the dedup
		// category, not to the orthogonal same-page opportunity.
		return
	}
	maxNS := float64(obsAggThresholds[len(obsAggThresholds)-1]) * s.cfg.CycleNS
	s.pageNearest(s.pageState, obsPageKey{g: int32(g), owner: int32(o),
		page: line >> 6, epoch: ep}, line, now, maxNS, &s.aggSamePage)
	s.pageNearest(s.siblingState, obsPageKey{g: int32(g), owner: int32(o),
		page: line >> 1, epoch: ep}, line, now, maxNS, &s.aggSibling)
	s.pagePruneAt++
	if s.pagePruneAt%(1<<16) == 0 {
		for k, v := range s.pageState {
			if now-v.latestNS > maxNS {
				delete(s.pageState, k)
			}
		}
		for k, v := range s.siblingState {
			if now-v.latestNS > maxNS {
				delete(s.siblingState, k)
			}
		}
	}
}

// pageNearest is analyze_observations.build_remote_spatial_distributions:
// the nearest earlier read of a different line of the same group.
func (s *obsState) pageNearest(m map[obsPageKey]*obsPageState, key obsPageKey,
	line uint64, now, maxNS float64, out *[len(obsAggThresholds)]uint64) {
	ps := m[key]
	prev := -1.0
	if ps != nil {
		switch {
		case now-ps.latestNS > maxNS:
			ps = nil
		case ps.latestLine != line:
			prev = ps.latestNS
		case ps.otherLine != line && now-ps.otherNS <= maxNS:
			prev = ps.otherNS
		}
	}
	if prev >= 0 {
		d := (now - prev) / s.cfg.CycleNS
		for i, th := range obsAggThresholds {
			if d <= float64(th)+1e-9 {
				out[i]++
			}
		}
	}
	if ps == nil {
		ps = &obsPageState{latestLine: line, otherLine: line,
			latestNS: now, otherNS: now}
		m[key] = ps
		return
	}
	if ps.latestLine == line {
		ps.latestNS = now
		return
	}
	ps.otherLine, ps.otherNS = ps.latestLine, ps.latestNS
	ps.latestLine, ps.latestNS = line, now
}

func (s *obsState) admitReuse(g int, line uint64, ep uint32, now float64,
	st *obsReqLine) {
	s.reuseReads++
	var cnt float64
	var last float64
	if ep == 0 && line < 1<<50 && g >= 0 && g < 64 && now < 4e9 {
		key := line<<6 | uint64(g)
		v, ok := s.reuse[key]
		if ok {
			cnt, last = float64(v[0]), float64(v[1])
		}
		s.reuse[key] = [2]uint32{v[0] + 1, uint32(now)}
	} else {
		key := obsReuseKey{g: int32(g), line: line, epoch: ep}
		v, ok := s.reuseOverflow[key]
		if ok {
			cnt, last = v[0], v[1]
		}
		s.reuseOverflow[key] = [2]float64{v[0] + 1, now}
	}
	if cnt > 0 {
		s.reuseRetouch++
		s.reuseInterval.add(now - last)
		if st == nil || st.active == 0 {
			s.reuseRetouchIdle++
		}
	}
	k := obsLineKey{g: int32(g), line: line}
	if t, ok := s.mcastDelivered[k]; ok && now >= t {
		s.mcastReread++
		delete(s.mcastDelivered, k)
	}
}

// ---------------------------------------------------------------------------
// network request (t1)

// ObsNetSend records one line of a remote read packet leaving the requester
// RDMA; linesInPacket is the number of lines the packet carries.
func ObsNetSend(requester, owner, netID string, addr uint64,
	linesInPacket int, now sim.VTimeInSec) {
	if !obsOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil {
		return
	}
	g := s.tileOf(requester)
	o := s.tileOf(owner)
	line := addr >> 6
	t := obsNS(now)
	if s.cfg.Latency {
		s.net[obsNetKey{id: netID, line: line}] = &obsNetLine{
			g: g, owner: o, t1: t, lineAddr: line}
	}
	if s.cfg.ReqAgg {
		s.sentLines++
		if linesInPacket > 1 {
			s.multiLineLines++
		} else {
			s.sentPackets++
		}
		p := s.pack[g]
		if p == nil {
			p = &obsPackState{groups: map[obsPackKey]int{}}
			s.pack[g] = p
		}
		cyc := int64(math.Floor(t/s.cfg.CycleNS + 1e-6))
		if !p.started || cyc != p.cycle {
			s.flushPack(p)
			p.cycle, p.started = cyc, true
		}
		p.groups[obsPackKey{owner: o, page: line >> 6}]++
	}
}

// ObsNetPacket counts one multi-line (M2 bitmap) request packet.
func ObsNetPacket(lines int) {
	if !obsOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	if s := obsS; s != nil && s.cfg.ReqAgg && lines > 1 {
		s.multiLinePkts++
		s.sentPackets++
	}
}

func (s *obsState) flushPack(p *obsPackState) {
	for k, n := range p.groups {
		if n >= 2 {
			s.packableLines += uint64(n)
		}
		delete(p.groups, k)
	}
}

// ---------------------------------------------------------------------------
// owner side (t2, t3, t4, t4s)

// ObsOwnerRecv records a request line reaching the owner RDMA.
func ObsOwnerRecv(netID string, addr uint64, recv sim.VTimeInSec) {
	if !obsOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil || !s.cfg.Latency {
		return
	}
	r := s.net[obsNetKey{id: netID, line: addr >> 6}]
	if r == nil || r.t2 > 0 {
		return
	}
	r.t2 = obsNS(recv)
	s.ownReads++
	ol := s.own[r.lineAddr]
	if ol == nil {
		ol = &obsOwnLine{reqs: map[int]int{}}
		s.own[r.lineAddr] = ol
	}
	if ol.total > 0 {
		s.ownConcurrent++
		if ol.total-ol.reqs[r.g] > 0 {
			s.ownConcurrentOther++
		}
	}
	ol.total++
	ol.reqs[r.g]++
	r.ownerActive = true
}

// ObsOwnerL2 records the owner forwarding a request line to its L2 (t3);
// merged is true when it joined an L2 read already pending (M1 owner merge).
func ObsOwnerL2(netID string, addr uint64, now sim.VTimeInSec,
	l2ReqID string, merged bool) {
	if !obsOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil || !s.cfg.Latency {
		return
	}
	r := s.net[obsNetKey{id: netID, line: addr >> 6}]
	if r == nil {
		return
	}
	r.t3 = obsNS(now)
	r.ownerMerged = merged
	if l2ReqID == "" {
		return
	}
	s.l2[l2ReqID] = append(s.l2[l2ReqID], r)
	if c, ok := s.l2cls[l2ReqID]; ok {
		r.cls = c
	}
}

// ObsL2Result records the L2 directory result of a request (any L2 access;
// only owner reads of remote requests are kept).
func ObsL2Result(reqID, result string) {
	if !obsOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil || !s.cfg.Latency {
		return
	}
	lines, ok := s.l2[reqID]
	if !ok {
		return
	}
	var c int8
	switch result {
	case "read-hit":
		c = obsClsHit
	case "read-miss":
		c = obsClsMiss
	case "read-mshr-hit":
		c = obsClsMSHRHit
	default:
		return
	}
	for _, r := range lines {
		if r.cls == obsClsUnknown {
			r.cls = c
		}
	}
	s.l2cls[reqID] = c
}

// ObsOwnerL2Rsp records the L2 response of an owner read reaching the owner
// RDMA (t4) for every request line waiting on it.
func ObsOwnerL2Rsp(l2ReqID string, recv sim.VTimeInSec) {
	if !obsOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil || !s.cfg.Latency {
		return
	}
	lines, ok := s.l2[l2ReqID]
	if !ok {
		return
	}
	t := obsNS(recv)
	for _, r := range lines {
		r.t4 = t
	}
	delete(s.l2, l2ReqID)
	delete(s.l2cls, l2ReqID)
}

// ObsOwnerSend records the owner sending the response of a request line.
func ObsOwnerSend(netID string, addr uint64, now sim.VTimeInSec,
	multicast bool) {
	if !obsOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil || !s.cfg.Latency {
		return
	}
	r := s.net[obsNetKey{id: netID, line: addr >> 6}]
	if r == nil {
		return
	}
	r.t4s = obsNS(now)
	r.multicast = multicast
	s.ownerRelease(r)
}

func (s *obsState) ownerRelease(r *obsNetLine) {
	if !r.ownerActive {
		return
	}
	r.ownerActive = false
	ol := s.own[r.lineAddr]
	if ol == nil {
		return
	}
	ol.total--
	if ol.reqs[r.g]--; ol.reqs[r.g] <= 0 {
		delete(ol.reqs, r.g)
	}
	if ol.total <= 0 {
		delete(s.own, r.lineAddr)
	}
}

// ---------------------------------------------------------------------------
// requester response (t5) and completion (t6)

// ObsReqArrive records the response of a request line reaching the
// requester RDMA (t5).
func ObsReqArrive(requester, netID string, addr uint64, recv sim.VTimeInSec) {
	if !obsOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil {
		return
	}
	line := addr >> 6
	g := s.tileOf(requester)
	t := obsNS(recv)
	k := obsLineKey{g: int32(g), line: line}
	nk := obsNetKey{id: netID, line: line}
	r := s.net[nk]
	if r != nil {
		delete(s.net, nk)
		s.ownerRelease(r)
	}
	st := s.reqLine[k]
	if st != nil {
		st.lastArrive = t
		st.hasNet = r != nil
		st.netClaimed = false
		if r != nil {
			st.net = *r
		}
	}
	if r != nil && r.multicast && s.cfg.Reuse {
		s.mcastDeliveries++
		if _, ok := s.mcastDelivered[k]; !ok {
			s.mcastDeliveredOpen++
		}
		s.mcastDelivered[k] = t
	}
}

// ObsL1Done records the requester RDMA sending a response to the L1V (t6).
func ObsL1Done(id string, now sim.VTimeInSec) {
	if !obsOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil {
		return
	}
	l := s.logical[id]
	if l == nil {
		return
	}
	delete(s.logical, id)
	t6 := obsNS(now)
	s.bytesByGPM[l.g] += l.bytes
	s.readsByGPM[l.g]++
	k := obsLineKey{g: int32(l.g), line: l.line}
	st := s.reqLine[k]
	if s.cfg.Latency {
		h := &s.hop[s.hops(l.g, l.owner)]
		if s.cfg.PerOwner && l.owner >= 0 && l.owner < len(s.ownerStats) {
			s.classify(h, l, st, t6, &s.ownerStats[l.owner])
		} else {
			s.classify(h, l, st, t6)
		}
	}
	if st != nil {
		st.active--
		if st.active <= 0 {
			delete(s.reqLine, k)
		}
	}
}

func (s *obsState) classify(h *obsHopStats, l *obsLogical, st *obsReqLine,
	t6 float64, extra ...*obsHopStats) {
	hs := append([]*obsHopStats{h}, extra...)
	add := func(seg int, v float64) {
		for _, x := range hs {
			x.seg[seg].add(v)
		}
	}
	inc := func(f func(x *obsHopStats)) {
		for _, x := range hs {
			f(x)
		}
	}
	inc(func(x *obsHopStats) { x.n++ })
	add(obsSegTotal, t6-l.t0)
	if st == nil || !st.hasNet || st.lastArrive < l.t0 || st.lastArrive > t6 {
		inc(func(x *obsHopStats) { x.nLocal++ })
		add(obsSegLocal, t6-l.t0)
		return
	}
	n := &st.net
	t5 := st.lastArrive
	add(obsSegToL1, t6-t5)
	if l.t0 > n.t1 {
		inc(func(x *obsHopStats) { x.nMergedFlight++ })
		add(obsSegMergeWait, t5-l.t0)
		return
	}
	inc(func(x *obsHopStats) { x.nNet++ })
	if st.netClaimed {
		inc(func(x *obsHopStats) { x.nMergedQueued++ })
	}
	st.netClaimed = true
	add(obsSegWait, n.t1-l.t0)
	add(obsSegRTT, t5-n.t1)
	if n.multicast {
		inc(func(x *obsHopStats) { x.nMulticastLeaf++ })
	}
	if n.t2 == 0 || n.t3 == 0 || n.t4 == 0 {
		inc(func(x *obsHopStats) { x.nIncomplete++ })
		return
	}
	add(obsSegFwd, n.t2-n.t1)
	add(obsSegOwnerQueue, n.t3-n.t2)
	switch {
	case n.ownerMerged:
		inc(func(x *obsHopStats) { x.nOwnerMerged++ })
		add(obsSegSvcMerged, n.t4-n.t3)
	case n.cls == obsClsHit:
		inc(func(x *obsHopStats) { x.nOwnerHit++ })
		add(obsSegSvcHit, n.t4-n.t3)
	case n.cls == obsClsMiss || n.cls == obsClsMSHRHit:
		inc(func(x *obsHopStats) { x.nOwnerMiss++ })
		if n.cls == obsClsMSHRHit {
			inc(func(x *obsHopStats) { x.nOwnerMSHRHit++ })
		}
		add(obsSegSvcMiss, n.t4-n.t3)
	default:
		inc(func(x *obsHopStats) { x.nOwnerUnknown++ })
	}
	add(obsSegRet, t5-n.t4)
	if n.t4s > 0 {
		add(obsSegOwnerRspWait, n.t4s-n.t4)
		add(obsSegRetNet, t5-n.t4s)
	}
}

// ObsLogicalBytes returns the bytes and reads completed per requester tile.
func ObsLogicalBytes() (bytes, reads map[int]uint64) {
	obsMu.Lock()
	defer obsMu.Unlock()
	bytes, reads = map[int]uint64{}, map[int]uint64{}
	if obsS == nil {
		return
	}
	for k, v := range obsS.bytesByGPM {
		bytes[k] = v
	}
	for k, v := range obsS.readsByGPM {
		reads[k] = v
	}
	return
}

// ---------------------------------------------------------------------------
// L2 free capacity

// ObsL2Register registers an L2 cache and returns its index.
func ObsL2Register(name string, sets, ways int) int {
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil {
		return -1
	}
	for i, c := range s.l2caches {
		if c.name == name {
			return i
		}
	}
	n := sets * ways
	s.l2caches = append(s.l2caches, &obsL2Cache{
		name: name, sets: sets, ways: ways,
		valid: make([]bool, n), lastTouch: make([]float64, n),
		invalidSince: make([]float64, n),
	})
	return len(s.l2caches) - 1
}

func (s *obsState) l2block(id, set, way int) (*obsL2Cache, int) {
	if id < 0 || id >= len(s.l2caches) {
		return nil, -1
	}
	c := s.l2caches[id]
	i := set*c.ways + way
	if i < 0 || i >= len(c.valid) {
		return nil, -1
	}
	return c, i
}

// ObsL2Fill records a new line allocated in an L2 way (a new lifetime).
func ObsL2Fill(id, set, way int, now sim.VTimeInSec) {
	if !obsL2FreeOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil {
		return
	}
	c, i := s.l2block(id, set, way)
	if c == nil {
		return
	}
	t := obsNS(now)
	if c.valid[i] {
		c.deadNS += t - c.lastTouch[i]
		c.lifetimes++
	} else {
		c.invalidNS += t - c.invalidSince[i]
	}
	c.valid[i] = true
	c.lastTouch[i] = t
	c.fills++
}

// ObsL2ReadHit records a read hit on an L2 way.
func ObsL2ReadHit(id, set, way int, now sim.VTimeInSec) {
	if !obsL2FreeOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil {
		return
	}
	c, i := s.l2block(id, set, way)
	if c == nil || !c.valid[i] {
		return
	}
	c.lastTouch[i] = obsNS(now)
	c.readHits++
}

// ObsL2InvalidateWay records one L2 way becoming invalid without a new fill
// (M3 kernel-boundary invalidation of a remote replica line).
func ObsL2InvalidateWay(id, set, way int, now sim.VTimeInSec) {
	if !obsL2FreeOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil {
		return
	}
	c, i := s.l2block(id, set, way)
	if c == nil || !c.valid[i] {
		return
	}
	t := obsNS(now)
	c.deadNS += t - c.lastTouch[i]
	c.lifetimes++
	c.valid[i] = false
	c.invalidSince[i] = t
}

// ObsL2Invalidate records every way of an L2 cache becoming invalid (flush).
func ObsL2Invalidate(id int, now sim.VTimeInSec) {
	if !obsL2FreeOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil || id < 0 || id >= len(s.l2caches) {
		return
	}
	c := s.l2caches[id]
	t := obsNS(now)
	for i := range c.valid {
		if c.valid[i] {
			c.deadNS += t - c.lastTouch[i]
			c.lifetimes++
			c.valid[i] = false
			c.invalidSince[i] = t
		}
	}
}

// ---------------------------------------------------------------------------
// links

// ObsLinkRegister returns the index of the directed link src -> dst.
func ObsLinkRegister(src, dst string) int {
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil {
		return -1
	}
	key := src + "->" + dst
	if i, ok := s.linkIdx[key]; ok {
		return i
	}
	s.links = append(s.links, &obsLink{src: src, dst: dst, curWin: -1})
	s.linkIdx[key] = len(s.links) - 1
	return len(s.links) - 1
}

// ObsLinkFlit records one flit delivered over a directed link.
func ObsLinkFlit(idx int, now sim.VTimeInSec) {
	if !obsLinksOn {
		return
	}
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil || idx < 0 || idx >= len(s.links) {
		return
	}
	l := s.links[idx]
	t := obsNS(now)
	w := int64(t / s.cfg.WindowNS)
	if w != l.curWin {
		s.foldLinkWindow(l, w)
	}
	l.curFlits++
	l.flits++
	s.meshWin[w]++
	if t > s.lastFlit {
		s.lastFlit = t
	}
}

// foldLinkWindow closes the link's current window (and the empty windows up
// to w) into the per-(link, window) utilization histogram.
func (s *obsState) foldLinkWindow(l *obsLink, w int64) {
	capFlits := s.cfg.LinkBytesPerNS * s.cfg.WindowNS / float64(s.cfg.FlitBytes)
	if l.curWin >= 0 {
		if capFlits > 0 {
			s.winUtil.add(1000 * float64(l.curFlits) / capFlits)
		}
		if l.curFlits > l.maxWin {
			l.maxWin = l.curFlits
		}
		empty := w - l.curWin - 1
		if empty > 0 {
			s.winUtil.n += uint64(empty)
			s.winUtil.bin[0] += uint64(empty)
		}
	} else if w > 0 {
		s.winUtil.n += uint64(w)
		s.winUtil.bin[0] += uint64(w)
	}
	l.curWin = w
	l.curFlits = 0
}

// ---------------------------------------------------------------------------
// output

func obsF(v float64) string {
	return fmt.Sprintf("%.6g", v)
}

// ObsWrite writes the per-run files <prefix>_latency.csv, _summary.csv,
// _links.csv and _l2free.csv. label is the workload column; endNS the
// simulated end time.
func ObsWrite(prefix, label string, endNS float64) error {
	obsMu.Lock()
	defer obsMu.Unlock()
	s := obsS
	if s == nil {
		return nil
	}
	var errs []string
	if s.cfg.Latency {
		if err := s.writeLatency(prefix+"_latency.csv", label); err != nil {
			errs = append(errs, err.Error())
		}
		if s.cfg.PerOwner {
			if err := s.writeLatencyBy(prefix+"_latency_owner.csv", label,
				"owner", s.ownerStats[:]); err != nil {
				errs = append(errs, err.Error())
			}
		}
	}
	if s.cfg.Links {
		if err := s.writeLinks(prefix+"_links.csv", label, endNS); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if s.cfg.Reuse {
		if err := s.writeL2Free(prefix+"_l2free.csv", label, endNS); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if err := s.writeSummary(prefix+"_summary.csv", label, endNS); err != nil {
		errs = append(errs, err.Error())
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

type obsCSV struct {
	f *os.File
	w *bufio.Writer
}

func obsCreate(path string) (*obsCSV, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &obsCSV{f: f, w: bufio.NewWriter(f)}, nil
}

func (c *obsCSV) row(cols ...string) {
	c.w.WriteString(strings.Join(cols, ","))
	c.w.WriteByte('\n')
}

func (c *obsCSV) close() error {
	if err := c.w.Flush(); err != nil {
		c.f.Close()
		return err
	}
	return c.f.Close()
}

func (s *obsState) writeLatency(path, label string) error {
	return s.writeLatencyBy(path, label, "hops", s.hop[:])
}

func (s *obsState) writeLatencyBy(path, label, key string,
	stats []obsHopStats) error {
	c, err := obsCreate(path)
	if err != nil {
		return err
	}
	head := []string{"workload", key, "n", "n_net", "n_req_merged_queued",
		"n_req_merged_inflight", "n_local", "n_incomplete", "n_owner_hit",
		"n_owner_miss", "n_owner_mshr_hit", "n_owner_merged", "n_owner_unknown",
		"n_multicast_leaf"}
	for _, sn := range obsSegNames {
		head = append(head, sn+"_n", sn+"_mean", sn+"_p50", sn+"_p95")
	}
	c.row(head...)
	var all obsHopStats
	emit := func(name string, h *obsHopStats) {
		cols := []string{label, name,
			fmt.Sprint(h.n), fmt.Sprint(h.nNet), fmt.Sprint(h.nMergedQueued),
			fmt.Sprint(h.nMergedFlight), fmt.Sprint(h.nLocal),
			fmt.Sprint(h.nIncomplete), fmt.Sprint(h.nOwnerHit),
			fmt.Sprint(h.nOwnerMiss), fmt.Sprint(h.nOwnerMSHRHit),
			fmt.Sprint(h.nOwnerMerged), fmt.Sprint(h.nOwnerUnknown),
			fmt.Sprint(h.nMulticastLeaf)}
		for i := range h.seg {
			g := &h.seg[i]
			cols = append(cols, fmt.Sprint(g.n), obsF(g.mean()),
				obsF(g.quantile(0.5)), obsF(g.quantile(0.95)))
		}
		c.row(cols...)
	}
	for hop := range stats {
		h := &stats[hop]
		if h.n == 0 {
			continue
		}
		emit(fmt.Sprint(hop), h)
		all.n += h.n
		all.nNet += h.nNet
		all.nMergedQueued += h.nMergedQueued
		all.nMergedFlight += h.nMergedFlight
		all.nLocal += h.nLocal
		all.nIncomplete += h.nIncomplete
		all.nOwnerHit += h.nOwnerHit
		all.nOwnerMiss += h.nOwnerMiss
		all.nOwnerMSHRHit += h.nOwnerMSHRHit
		all.nOwnerMerged += h.nOwnerMerged
		all.nOwnerUnknown += h.nOwnerUnknown
		all.nMulticastLeaf += h.nMulticastLeaf
		for i := range h.seg {
			all.seg[i].merge(&h.seg[i])
		}
	}
	emit("all", &all)
	return c.close()
}

func (s *obsState) writeLinks(path, label string, endNS float64) error {
	end := endNS
	if s.lastFlit > end {
		end = s.lastFlit
	}
	endWin := int64(end / s.cfg.WindowNS)
	for _, l := range s.links {
		s.foldLinkWindow(l, endWin+1)
	}
	// Links that never carried a flit were never registered: their windows
	// are idle.
	if missing := s.expectedLinks() - len(s.links); missing > 0 {
		zeros := uint64(missing) * uint64(endWin+1)
		s.winUtil.n += zeros
		s.winUtil.bin[0] += zeros
	}
	c, err := obsCreate(path)
	if err != nil {
		return err
	}
	c.row("workload", "link_src", "link_dst", "flits", "bytes", "util",
		"max_window_util")
	capWin := s.cfg.LinkBytesPerNS * s.cfg.WindowNS
	idx := make([]int, len(s.links))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		la, lb := s.links[idx[a]], s.links[idx[b]]
		if la.src != lb.src {
			return la.src < lb.src
		}
		return la.dst < lb.dst
	})
	for _, i := range idx {
		l := s.links[i]
		bytes := float64(l.flits) * float64(s.cfg.FlitBytes)
		util := 0.0
		if end > 0 && s.cfg.LinkBytesPerNS > 0 {
			util = bytes / (end * s.cfg.LinkBytesPerNS)
		}
		mw := 0.0
		if capWin > 0 {
			mw = float64(l.maxWin) * float64(s.cfg.FlitBytes) / capWin
		}
		c.row(label, l.src, l.dst, fmt.Sprint(l.flits), obsF(bytes),
			obsF(util), obsF(mw))
	}
	return c.close()
}

// expectedLinks is the number of directed links of a square mesh.
func (s *obsState) expectedLinks() int {
	w := s.cfg.TileWidth
	return 4 * w * (w - 1)
}

func (s *obsState) writeL2Free(path, label string, endNS float64) error {
	c, err := obsCreate(path)
	if err != nil {
		return err
	}
	c.row("workload", "cache", "ways_total", "invalid_frac", "dead_frac",
		"tail_frac", "free_frac", "fills", "lifetimes", "read_hits")
	sort.Slice(s.l2caches, func(a, b int) bool {
		return s.l2caches[a].name < s.l2caches[b].name
	})
	for _, l2 := range s.l2caches {
		inv, dead, tail := l2.invalidNS, l2.deadNS, 0.0
		for i := range l2.valid {
			if l2.valid[i] {
				tail += endNS - l2.lastTouch[i]
			} else {
				inv += endNS - l2.invalidSince[i]
			}
		}
		den := float64(len(l2.valid)) * endNS
		if den <= 0 {
			den = 1
		}
		c.row(label, l2.name, fmt.Sprint(len(l2.valid)), obsF(inv/den),
			obsF(dead/den), obsF(tail/den), obsF((inv+dead)/den),
			fmt.Sprint(l2.fills), fmt.Sprint(l2.lifetimes),
			fmt.Sprint(l2.readHits))
	}
	return c.close()
}

func (s *obsState) writeSummary(path, label string, endNS float64) error {
	c, err := obsCreate(path)
	if err != nil {
		return err
	}
	c.row("workload", "metric", "value")
	put := func(k string, v float64) { c.row(label, k, obsF(v)) }
	putU := func(k string, v uint64) { c.row(label, k, fmt.Sprint(v)) }
	put("end_ns", endNS)
	putU("open_logical_reads", uint64(len(s.logical)))
	putU("open_network_lines", uint64(len(s.net)))
	putU("remote_writes_seen", s.writesSeen)
	if s.cfg.Latency {
		putU("owner_reads", s.ownReads)
		putU("owner_same_line_concurrent", s.ownConcurrent)
		putU("owner_same_line_concurrent_other_gpm", s.ownConcurrentOther)
		if s.ownReads > 0 {
			put("owner_same_line_concurrent_frac",
				float64(s.ownConcurrent)/float64(s.ownReads))
			put("owner_same_line_concurrent_other_gpm_frac",
				float64(s.ownConcurrentOther)/float64(s.ownReads))
		}
	}
	if s.cfg.ReqAgg {
		for _, p := range s.pack {
			s.flushPack(p)
		}
		putU("agg_remote_reads", s.aggReads)
		putU("agg_inflight_same_line_same_gpm", s.aggExact)
		putU("agg_same_line_window", s.aggDupWindow)
		put("agg_window_cycles", float64(s.cfg.AggWindowCycles))
		put("agg_cycle_ns", s.cfg.CycleNS)
		for i, th := range obsAggThresholds {
			putU(fmt.Sprintf("agg_same_page_le_%dcyc", th), s.aggSamePage[i])
			putU(fmt.Sprintf("agg_sibling128_le_%dcyc", th), s.aggSibling[i])
		}
		putU("agg_sent_lines", s.sentLines)
		putU("agg_sent_packets", s.sentPackets)
		putU("agg_same_cycle_same_page_lines", s.packableLines)
		putU("agg_multi_line_packets", s.multiLinePkts)
		putU("agg_multi_line_packet_lines", s.multiLineLines)
	}
	if s.cfg.Reuse {
		uniq := uint64(len(s.reuse) + len(s.reuseOverflow))
		var reused uint64
		for _, v := range s.reuse {
			if v[0] > 1 {
				reused++
			}
		}
		for _, v := range s.reuseOverflow {
			if v[0] > 1 {
				reused++
			}
		}
		putU("reuse_remote_reads", s.reuseReads)
		putU("reuse_unique_lines", uniq)
		putU("reuse_lines_reused", reused)
		putU("reuse_retouch_reads", s.reuseRetouch)
		putU("reuse_retouch_reads_after_complete", s.reuseRetouchIdle)
		put("reuse_interval_mean_ns", s.reuseInterval.mean())
		for _, q := range []float64{0.5, 0.9, 0.99} {
			put(fmt.Sprintf("reuse_interval_p%g_ns", q*100),
				s.reuseInterval.quantile(q))
		}
		for i, b := range s.reuseInterval.bin {
			if b > 0 {
				put(fmt.Sprintf("reuse_interval_bin_%03d_ns_ge", i),
					math.Pow(2, float64(i)/8)-1)
				putU(fmt.Sprintf("reuse_interval_bin_%03d_count", i), b)
			}
		}
		putU("multicast_deliveries", s.mcastDeliveries)
		putU("multicast_delivered_lines", s.mcastDeliveredOpen)
		putU("multicast_reread_lines", s.mcastReread)
	}
	if s.cfg.Links {
		var total uint64
		var maxW uint64
		var keys []int64
		for w, v := range s.meshWin {
			total += v
			if v > maxW {
				maxW = v
			}
			keys = append(keys, w)
		}
		putU("mesh_flits", total)
		put("mesh_bytes", float64(total)*float64(s.cfg.FlitBytes))
		put("mesh_max_window_bytes", float64(maxW)*float64(s.cfg.FlitBytes))
		put("link_window_util_p99", s.winUtil.quantile(0.99)/1000)
		put("link_window_util_p50", s.winUtil.quantile(0.5)/1000)
		put("link_window_ns", s.cfg.WindowNS)
		put("link_bytes_per_ns", s.cfg.LinkBytesPerNS)
		putU("link_windows", s.winUtil.n)
		putU("links_registered", uint64(len(s.links)))
		putU("links_expected", uint64(s.expectedLinks()))
	}
	return c.close()
}
