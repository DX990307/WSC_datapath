package rdma

import (
	"fmt"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// ISCA'27 wafer M1: combining hubs (-m1w-hub-mode, off by default).
//
// A requester sends an eligible read transaction (a single line or a page
// bitmap batch, after its own merge / batching) to a hub GPM instead of the
// owner. The hub's RDMA keeps a line table: a line already in flight from the
// hub (another requester asked first) or, with retention, recently returned
// to it, is served from that entry; only the missing lines are forwarded to
// the owner as one bitmap request issued by the hub. When the owner's lines
// come back, the hub answers every waiting requester (one response per
// requester per arrival). The requester's window slot, credits and merge
// table are unchanged: the transaction is still accounted to the owner and
// completes when the hub's response(s) arrive.
//
// Hub choice:
//   - region: the mesh is split into part x part regions with one hub GPM
//     each (the GPM with the smallest summed hops to its region); reads to an
//     owner more than min-hops away and outside the requester's region go
//     through the requester's hub;
//   - turn: the hub is the GPM at the turn tile (x_r, y_o) of the requester's
//     Y-then-X route to the owner, so the request path requester -> hub ->
//     owner and the response path owner -> hub -> requester have exactly the
//     direct hop count (no detour); every requester in the same column
//     shares that hub for that owner row.
//
// Consistency (same assumptions as M3): data-race-free kernels; a requester
// never routes a line it has written through a hub; the hub drops its
// retained lines and closes its in-flight lines at every kernel boundary
// (kernels are global and sequential); lines forwarded before the boundary
// still answer the requesters that were waiting for them.

// M1WHubConfig configures the combining hubs.
type M1WHubConfig struct {
	Mode     string // "" (off), "region" or "turn"
	Part     int    // region mode: part x part regions
	MinHops  int    // only reads to owners more than MinHops away
	RetainNS float64
	Entries  int // hub line-table capacity (lines)
}

// Enabled reports whether hubs are on.
func (c M1WHubConfig) Enabled() bool { return c.Mode != "" }

// Validate checks the configuration.
func (c M1WHubConfig) Validate() error {
	switch c.Mode {
	case "", "region", "turn":
	default:
		return fmt.Errorf("m1w hub mode must be region or turn, got %q", c.Mode)
	}
	if c.Mode == "region" && c.Part < 2 {
		return fmt.Errorf("m1w hub region partition must be >= 2")
	}
	if c.Entries <= 0 && c.Mode != "" {
		return fmt.Errorf("m1w hub entries must be positive")
	}
	return nil
}

// M1WHubStats are the hub counters of one RDMA engine.
type M1WHubStats struct {
	Enabled          bool
	RoutedTxns       uint64 // requester: transactions sent to a hub
	RoutedLines      uint64
	DirectTxns       uint64 // requester: eligible owner but sent direct (written line)
	HubReqs          uint64 // hub: requests received
	HubLines         uint64
	HubInflightHits  uint64 // line found in flight at the hub
	HubReadyHits     uint64 // line found retained at the hub
	HubForwardTxns   uint64
	HubForwardLines  uint64
	HubUntracked     uint64 // forwarded lines not entered (table full)
	HubResponses     uint64
	HubStaleAtBound  uint64
	HubPeakEntries   uint64
	HubBoundaries    uint64
	HubOutPeak       uint64
	HubOwnerRspLines uint64
	// Timing breakdown (ns sums; statistics only).
	ReqInNetNS   float64 // requester send -> hub receive (per request)
	ReqInWaitNS  float64 // hub receive -> served (per request)
	FwdRTTNS     float64 // hub forward send -> owner response received
	FwdRsps      uint64  // owner responses received for forwards
	FwdRspWaitNS float64 // owner response received -> handled
	OutWaitNS    float64 // queued at the hub -> sent (per message)
	OutMsgs      uint64
}

type m1wHubWaiter struct {
	req  *m1wHubReq
	line uint64 // offset in the page
}

type m1wHubLine struct {
	key     m1wLineKey
	ready   bool
	data    []byte
	readyAt sim.VTimeInSec
	waiters []m1wHubWaiter
	tracked bool
	stale   bool
}

type m1wHubReq struct {
	src       sim.Port
	id        string
	single    bool
	pid       uint32
	page      uint64
	remaining uint64 // lines not answered yet
	collected map[uint64][]byte
	queued    bool
}

type m1wHubFwd struct {
	sentAt sim.VTimeInSec
	owner  sim.Port
	lines  map[uint64]*m1wHubLine
}

type m1wHubState struct {
	cfg       M1WHubConfig
	lastSweep sim.VTimeInSec
	hubFor    []sim.Port // owner GPM index -> hub port (nil: direct)
	lines     map[m1wLineKey]*m1wHubLine
	fwd       map[string]*m1wHubFwd
	out       []sim.Msg
	outAt     []sim.VTimeInSec
	touched   []*m1wHubReq
	stats     M1WHubStats
}

// ConfigureM1WHub turns combining hubs on; SetM1WTopology must be called
// first.
func (c *Comp) ConfigureM1WHub(cfg M1WHubConfig) {
	if err := cfg.Validate(); err != nil {
		panic(err)
	}
	if !cfg.Enabled() {
		return
	}
	if c.m1wTopo == nil {
		panic("m1w hub: topology not set")
	}
	s := &m1wHubState{
		cfg:   cfg,
		lines: make(map[m1wLineKey]*m1wHubLine),
		fwd:   make(map[string]*m1wHubFwd),
	}
	s.stats.Enabled = true
	topo := c.m1wTopo
	n := topo.NumGPMs()
	s.hubFor = make([]sim.Port, n)
	r := c.m1wIdx
	var regionOf, hubOf []int
	if cfg.Mode == "region" {
		regionOf, hubOf = m1wPartition(topo, cfg.Part)
	}
	for o := 0; o < n; o++ {
		if o == r || topo.Hops(r, o) <= cfg.MinHops {
			continue
		}
		h := -1
		switch cfg.Mode {
		case "region":
			if regionOf[o] != regionOf[r] {
				h = hubOf[regionOf[r]]
			}
		case "turn":
			tile := topo.Coords[r][0] + topo.Coords[o][1]*topo.Width
			h = topo.tileGPM[tile]
		}
		if h < 0 || h == r || h == o {
			continue
		}
		s.hubFor[o] = topo.Ports[h]
	}
	c.m1wHub = s
}

// GetM1WHubStats returns the hub counters.
func (c *Comp) GetM1WHubStats() M1WHubStats {
	if c.m1wHub == nil {
		return M1WHubStats{}
	}
	s := c.m1wHub.stats
	return s
}

// m1wRoute returns the port a requester transaction to owner is sent to.
func (c *Comp) m1wRoute(owner sim.Port, entries []*remoteLineEntry) sim.Port {
	s := c.m1wHub
	if s == nil {
		return owner
	}
	o := c.m1wTopo.Index(owner)
	if o < 0 || s.hubFor[o] == nil {
		return owner
	}
	for _, e := range entries {
		if e != nil && c.remoteUncacheable[e.key.remoteLineIdentity] {
			s.stats.DirectTxns++
			return owner
		}
	}
	s.stats.RoutedTxns++
	s.stats.RoutedLines += uint64(len(entries))
	return s.hubFor[o]
}

func (c *Comp) m1wBatchEntries(batch *remoteBatch) []*remoteLineEntry {
	out := make([]*remoteLineEntry, 0, len(batch.lineOrder))
	for _, l := range batch.lineOrder {
		out = append(out, batch.lines[l])
	}
	return out
}

// m1wHubIsReq reports whether msg is a read this hub must serve (its
// address is owned by another GPM).
func (c *Comp) m1wHubIsReq(msg sim.Msg) bool {
	if c.m1wHub == nil {
		return false
	}
	switch r := msg.(type) {
	case *BitmapReadReq:
		return c.RemoteRDMAAddressTable.Find(r.PagePAddr) != c.ToOutside
	case *mem.ReadReq:
		return c.RemoteRDMAAddressTable.Find(r.Address) != c.ToOutside
	}
	return false
}

// m1wHubIsRsp reports whether rsp answers a hub forward.
func (c *Comp) m1wHubIsRsp(rspTo string) bool {
	return c.m1wHub != nil && c.m1wHub.fwd[rspTo] != nil
}

func (s *m1wHubState) live(e *m1wHubLine, now sim.VTimeInSec) bool {
	if e == nil || e.stale {
		return false
	}
	if !e.ready {
		return true
	}
	return float64(now-e.readyAt)*1e9 <= s.cfg.RetainNS
}

// m1wHubReq serves a request arriving at the hub.
func (c *Comp) m1wHubServe(now sim.VTimeInSec, msg sim.Msg) bool {
	s := c.m1wHub
	req := &m1wHubReq{src: msg.Meta().Src, id: msg.Meta().ID,
		collected: make(map[uint64][]byte)}
	var owner sim.Port
	var info interface{}
	switch r := msg.(type) {
	case *BitmapReadReq:
		req.pid = uint32(r.PID)
		req.page = r.PagePAddr
		req.remaining = r.LineBitmap
		owner = c.RemoteRDMAAddressTable.Find(r.PagePAddr)
		info = r.Info
	case *mem.ReadReq:
		if r.Address%remoteLineBytes != 0 || r.AccessByteSize != remoteLineBytes {
			panic("m1w hub: only full-line reads are routed to a hub")
		}
		req.single = true
		req.pid = uint32(r.PID)
		req.page = remotePageAddress(r.Address)
		req.remaining = uint64(1) << remoteLineOffset(r.Address)
		owner = c.RemoteRDMAAddressTable.Find(r.Address)
		info = r.Info
	}
	s.stats.HubReqs++
	s.stats.ReqInNetNS += float64(msg.Meta().RecvTime-msg.Meta().SendTime) * 1e9
	s.stats.ReqInWaitNS += float64(now-msg.Meta().RecvTime) * 1e9
	var missing uint64
	fwd := &m1wHubFwd{owner: owner, lines: make(map[uint64]*m1wHubLine)}
	for off := uint64(0); off < 64; off++ {
		bit := uint64(1) << off
		if req.remaining&bit == 0 {
			continue
		}
		s.stats.HubLines++
		key := m1wLineKey{req.pid, req.page + off*remoteLineBytes}
		e := s.lines[key]
		if s.live(e, now) {
			if e.ready {
				s.stats.HubReadyHits++
				c.m1wHubDeliver(req, off, e.data)
			} else {
				s.stats.HubInflightHits++
				e.waiters = append(e.waiters, m1wHubWaiter{req: req, line: off})
			}
			continue
		}
		if e != nil {
			delete(s.lines, key)
		}
		ne := &m1wHubLine{key: key}
		ne.waiters = append(ne.waiters, m1wHubWaiter{req: req, line: off})
		if len(s.lines) < s.cfg.Entries {
			ne.tracked = true
			s.lines[key] = ne
		} else {
			s.stats.HubUntracked++
		}
		missing |= bit
		fwd.lines[off] = ne
	}
	if uint64(len(s.lines)) > s.stats.HubPeakEntries {
		s.stats.HubPeakEntries = uint64(len(s.lines))
	}
	if missing != 0 {
		f := &BitmapReadReq{
			MsgMeta: sim.MsgMeta{
				ID:           sim.GetIDGenerator().Generate(),
				Src:          c.ToOutside,
				Dst:          owner,
				TrafficBytes: bitmapReqTraffic,
			},
			PID:        msgPID(msg),
			PagePAddr:  req.page,
			LineBitmap: missing,
			Info:       info,
		}
		s.fwd[f.ID] = fwd
		s.out = append(s.out, f)
		s.outAt = append(s.outAt, now)
		s.stats.HubForwardTxns++
		s.stats.HubForwardLines += uint64(len(fwd.lines))
	}
	c.m1wHubFlushTouched(now)
	c.ToOutside.Retrieve(now)
	return true
}

func msgPID(msg sim.Msg) vm.PID {
	switch r := msg.(type) {
	case *BitmapReadReq:
		return r.PID
	case *mem.ReadReq:
		return r.PID
	}
	return 0
}

func (c *Comp) m1wHubDeliver(req *m1wHubReq, off uint64, data []byte) {
	bit := uint64(1) << off
	if req.remaining&bit == 0 {
		panic("m1w hub: line delivered twice")
	}
	req.remaining &^= bit
	req.collected[off] = data
	if !req.queued {
		req.queued = true
		c.m1wHub.touched = append(c.m1wHub.touched, req)
	}
}

// m1wHubFlushTouched queues one response per requester that got lines.
func (c *Comp) m1wHubFlushTouched(now sim.VTimeInSec) {
	s := c.m1wHub
	for _, req := range s.touched {
		req.queued = false
		if len(req.collected) == 0 {
			continue
		}
		var rsp sim.Msg
		if req.single {
			var data []byte
			for _, d := range req.collected {
				data = d
			}
			rsp = mem.DataReadyRspBuilder{}.
				WithSrc(c.ToOutside).
				WithDst(req.src).
				WithRspTo(req.id).
				WithData(append([]byte(nil), data...)).
				Build()
		} else {
			lineData := make(map[uint64][]byte, len(req.collected))
			for off, d := range req.collected {
				lineData[off] = append([]byte(nil), d...)
			}
			rsp = &BitmapReadRsp{
				MsgMeta: sim.MsgMeta{
					ID:           sim.GetIDGenerator().Generate(),
					Src:          c.ToOutside,
					Dst:          req.src,
					TrafficBytes: bitmapRspOverhead + len(lineData)*int(remoteLineBytes),
				},
				RespondTo: req.id,
				LineData:  lineData,
			}
		}
		req.collected = make(map[uint64][]byte)
		s.out = append(s.out, rsp)
		s.outAt = append(s.outAt, now)
		s.stats.HubResponses++
	}
	s.touched = s.touched[:0]
	if uint64(len(s.out)) > s.stats.HubOutPeak {
		s.stats.HubOutPeak = uint64(len(s.out))
	}
}

// m1wHubOwnerRsp handles the owner's answer to a hub forward.
func (c *Comp) m1wHubOwnerRsp(now sim.VTimeInSec, rsp sim.Msg) bool {
	s := c.m1wHub
	var rspTo string
	var lineData map[uint64][]byte
	switch r := rsp.(type) {
	case *BitmapReadRsp:
		rspTo = r.RespondTo
		lineData = r.LineData
	default:
		panic("m1w hub: unexpected owner response")
	}
	f := s.fwd[rspTo]
	s.stats.FwdRsps++
	s.stats.FwdRTTNS += float64(rsp.Meta().RecvTime-f.sentAt) * 1e9
	s.stats.FwdRspWaitNS += float64(now-rsp.Meta().RecvTime) * 1e9
	for off := uint64(0); off < 64; off++ {
		data, ok := lineData[off]
		if !ok {
			continue
		}
		e := f.lines[off]
		if e == nil {
			panic("m1w hub: owner returned a line that was not forwarded")
		}
		delete(f.lines, off)
		s.stats.HubOwnerRspLines++
		e.ready = true
		e.readyAt = now
		e.data = append([]byte(nil), data...)
		for _, w := range e.waiters {
			c.m1wHubDeliver(w.req, w.line, e.data)
		}
		e.waiters = nil
		if e.tracked && (e.stale || s.cfg.RetainNS <= 0) {
			if s.lines[e.key] == e {
				delete(s.lines, e.key)
			}
		}
	}
	if len(f.lines) == 0 {
		delete(s.fwd, rspTo)
	}
	c.m1wHubFlushTouched(now)
	c.ToOutside.Retrieve(now)
	return true
}

// m1wHubSend drains the hub's output queue (forwards and responses).
func (c *Comp) m1wHubSend(now sim.VTimeInSec) bool {
	s := c.m1wHub
	if s == nil || len(s.out) == 0 {
		return false
	}
	progress := false
	for i := 0; i < c.effectivePipelineWidth() && len(s.out) > 0; i++ {
		msg := s.out[0]
		msg.Meta().SendTime = now
		if err := c.ToOutside.Send(msg); err != nil {
			break
		}
		if f := s.fwd[msg.Meta().ID]; f != nil {
			f.sentAt = now
		}
		s.stats.OutWaitNS += float64(now-s.outAt[0]) * 1e9
		s.stats.OutMsgs++
		s.out = s.out[1:]
		s.outAt = s.outAt[1:]
		progress = true
	}
	return progress || len(s.out) > 0
}

// m1wHubBoundary: a kernel launch at this GPM drops retained lines and
// closes in-flight ones.
func (c *Comp) m1wHubBoundary() {
	s := c.m1wHub
	if s == nil {
		return
	}
	s.stats.HubBoundaries++
	for k, e := range s.lines {
		if !e.ready {
			e.stale = true
			s.stats.HubStaleAtBound++
		}
		delete(s.lines, k)
	}
}

// m1wHubSweep drops expired retained lines (bounded work per call).
func (c *Comp) m1wHubSweep(now sim.VTimeInSec) {
	s := c.m1wHub
	if s == nil || s.cfg.RetainNS <= 0 || len(s.lines) < s.cfg.Entries ||
		float64(now-s.lastSweep)*1e9 < 1000 {
		return
	}
	s.lastSweep = now
	for k, e := range s.lines {
		if e.ready && float64(now-e.readyAt)*1e9 > s.cfg.RetainNS {
			delete(s.lines, k)
		}
	}
}

func (c *Comp) m1wHubPending() bool {
	s := c.m1wHub
	return s != nil && (len(s.out) > 0 || len(s.fwd) > 0)
}
