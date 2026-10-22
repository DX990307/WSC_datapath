package rdma

import (
	"fmt"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/noc/messaging"
	"github.com/sarchlab/akita/v3/sim"
)

// ISCA'27 geo-push ("geographic broadcast response").
//
// Owner. When the owner returns a read response to requester(s) R (a single
// line, an M1 multicast of a merged line, an M2 bitmap response, or a
// baseline-path response), it also sends the line(s) to the GPMs within
// Radius hops of each R (R and the owner excluded). The response and the
// pushes travel as ONE packet along the mesh multicast tree (GeoMulticast,
// split by the switches exactly like the M1/M2 T18 multicast): R receives
// its ordinary response, every neighbor a GeoPushMsg leaf. With the shared
// filter, only lines the owner has recently seen read by >= 2 distinct GPMs
// are pushed (a small set-associative table at the owner).
//
// Neighbor. A GeoPushMsg line is
//   - dropped when the neighbor wrote the line (uncacheable), when the data
//     was read at the owner at or before this GPM's last kernel boundary, or
//     when the neighbor already has the data (entry with data);
//   - delivered early to the waiters of the neighbor's own entry for the
//     line when that entry is still waiting for its data ("push catches an
//     in-flight request"): the waiters get the pushed data now; the entry
//     itself completes normally when its own response arrives (it keeps its
//     window slot until then, and fills the L2 as usual);
//   - otherwise installed into the neighbor's L2 through the M3 clean-fill
//     path (mem.RemoteDataFill with Push set): no window slot, no L1
//     response. The L2 drops it when the line is resident or in its MSHR,
//     when no invalid way is free (a push never evicts), or when its data
//     predates the slice's last flush or kernel boundary. Pushed copies are
//     M3 replica lines: they are invalidated at the next kernel boundary.
//
// Consistency is that of M3: within a kernel, data read by several GPMs is
// not written by another GPM (data-race freedom); requester writes make the
// line uncacheable at that requester; kernel boundaries drop every replica.

// GeoPushConfig configures geo-push (all off in the zero value).
type GeoPushConfig struct {
	// Mode: neighbor (push to the GPMs within Radius hops of each requester)
	// or affinity (push to each requester's learned top-K co-readers).
	Mode          GeoPushMode
	AffinityK     int // affinity: partners per requester (1 or 2)
	DecayHints    int // affinity: halve the counters every this many hints (0: 512)
	MinCount      int // affinity: a partner needs at least this count (0: 2)
	Radius        int  // 0 off, 1 or 2 hops around the requester
	SharedFilter  bool // push only lines seen read by >= 2 GPMs recently
	FilterEntries int  // owner sharing table entries (shared filter)
	FillQueue     int  // neighbor: pending L2 push fills (0: 64)
	// Readers mode (P19 Q2): reader bitmaps per page (geo_readers.go).
	ReaderWindowNS float64         // window length (0: 5000)
	ReaderMin      int             // online hot rule: readers per window (0: 4)
	ReaderMaxPages int             // pages tracked per owner, LRU (0: 1024)
	ReaderPages    map[uint64]bool // oracle hot pages (page addresses); nil = online
}

// GeoPushMode selects whom the owner pushes to.
type GeoPushMode int

// Geo-push modes.
const (
	GeoPushNeighbor GeoPushMode = iota
	GeoPushAffinity
	GeoPushReaders
)

func (m GeoPushMode) String() string {
	if m == GeoPushAffinity {
		return "affinity"
	}
	if m == GeoPushReaders {
		return "readers"
	}
	return "neighbor"
}

// Enabled reports whether geo-push is on.
func (c GeoPushConfig) Enabled() bool {
	return c.Radius > 0 || (c.Mode == GeoPushAffinity && c.AffinityK > 0) ||
		c.Mode == GeoPushReaders
}

// GeoPushStats are the per-RDMA geo-push counters.
type GeoPushStats struct {
	Enabled bool
	// Owner.
	OwnerRspCandidates uint64 // read responses considered
	OwnerFiltered      uint64 // ... no line passed the shared filter
	OwnerNoTarget      uint64 // ... no neighbor to push to
	OwnerPushPackets   uint64 // responses sent with pushes
	OwnerPushDests     uint64 // push destinations (sum over packets)
	OwnerPushLines     uint64 // pushed line copies (lines x destinations)
	OwnerExtraLinks    uint64 // tree links added by the pushes
	OwnerExtraLinkByte uint64 // ... x packet bytes
	FilterLookups      uint64
	FilterShared       uint64
	// Neighbor.
	RcvMsgs           uint64
	RcvLines          uint64
	DropNoL2          uint64
	DropStale         uint64
	DropUncacheable   uint64
	DropHave          uint64 // own entry already has the data
	DropInflightNoWtr uint64 // own entry in flight without waiters (prefetch)
	DropProbing       uint64 // own entry probing its L2
	DropQueueFull     uint64
	CatchInflight     uint64 // early delivery to an in-flight entry
	CatchQueued       uint64 // early delivery to a not yet sent entry
	CatchWaiters      uint64 // L1 requests served early
	FillsSent         uint64
	FillsInstalled    uint64
	// Affinity mode.
	AffHintsSent     uint64 // previous-reader hints piggybacked on responses
	AffHintsLearned  uint64 // hints received and counted (requester)
	AffDecays        uint64
	AffHdrSent       uint64 // requests carrying a non-empty partner list
	AffPartnerHops   [t18MaxHops]uint64 // push destinations by hops to R
	AffNoPartnerRsps uint64 // responses whose requesters had no partner
}

type geoPushLine struct {
	addr uint64
	data []byte
}

// GeoPushMsg is a neighbor's leaf of a geo-push multicast.
type GeoPushMsg struct {
	sim.MsgMeta
	PID    vm.PID
	Owner  sim.Port
	Origin sim.VTimeInSec // owner L2 read issue time of the oldest line
	Lines  []geoPushLine
}

// Meta returns the message metadata.
func (m *GeoPushMsg) Meta() *sim.MsgMeta { return &m.MsgMeta }

// GeoMulticast carries a response (Inner, for InnerDsts) and its pushes (for
// PushDsts) as one packet along the multicast tree.
type GeoMulticast struct {
	sim.MsgMeta
	Inner     sim.Msg
	InnerDsts []sim.Port
	PushDsts  []sim.Port
	Push      *GeoPushMsg // template of the push leaves
}

var _ messaging.MulticastMsg = (*GeoMulticast)(nil)

// Meta returns the message metadata.
func (m *GeoMulticast) Meta() *sim.MsgMeta { return &m.MsgMeta }

// MulticastDsts returns the remaining destinations.
func (m *GeoMulticast) MulticastDsts() []sim.Port {
	out := make([]sim.Port, 0, len(m.InnerDsts)+len(m.PushDsts))
	out = append(out, m.InnerDsts...)
	return append(out, m.PushDsts...)
}

func portIn(p sim.Port, list []sim.Port) bool {
	for _, x := range list {
		if x == p {
			return true
		}
	}
	return false
}

func (m *GeoMulticast) innerFor(dsts []sim.Port) sim.Msg {
	if len(dsts) == len(m.InnerDsts) {
		return m.Inner
	}
	mc, ok := m.Inner.(messaging.MulticastMsg)
	if !ok {
		panic("geo multicast: splitting a unicast inner message")
	}
	return mc.SplitForDsts(dsts)
}

func (m *GeoMulticast) pushLeaf(dst sim.Port) *GeoPushMsg {
	leaf := *m.Push
	leaf.ID = sim.GetIDGenerator().Generate()
	leaf.Src = m.Src
	leaf.Dst = dst
	leaf.SendTime = m.SendTime
	leaf.TrafficBytes = m.TrafficBytes
	return &leaf
}

// SplitForDsts returns the packet for a branch of the tree.
func (m *GeoMulticast) SplitForDsts(dsts []sim.Port) sim.Msg {
	var in, push []sim.Port
	for _, d := range dsts {
		switch {
		case portIn(d, m.InnerDsts):
			in = append(in, d)
		case portIn(d, m.PushDsts):
			push = append(push, d)
		default:
			panic(fmt.Sprintf("geo multicast %s has no destination %s",
				m.ID, d.Name()))
		}
	}
	if len(push) == 0 {
		return m.innerFor(in)
	}
	if len(in) == 0 && len(push) == 1 {
		return m.pushLeaf(push[0])
	}
	child := &GeoMulticast{
		MsgMeta:  m.MsgMeta,
		PushDsts: push,
		Push:     m.Push,
	}
	child.ID = sim.GetIDGenerator().Generate()
	if len(in) > 0 {
		child.Inner = m.innerFor(in)
		child.InnerDsts = in
		child.Dst = in[0]
	} else {
		child.Dst = push[0]
	}
	return child
}

// geoShareTable is the owner's sharing filter: per line, the first reader
// and whether a second, different GPM has read it since the entry was
// allocated. Set associative (4 ways), LRU.
type geoShareTable struct {
	sets  int
	ways  int
	tick  uint64
	slots []geoShareSlot
}

type geoShareSlot struct {
	valid  bool
	key    geoLineKey
	first  int
	shared bool
	lru    uint64
	// last / prev: the most recent reader and the most recent reader other
	// than last (affinity hints); -1 when unknown.
	last, prev int
}

func newGeoShareTable(entries int) *geoShareTable {
	if entries < 4 {
		entries = 4
	}
	ways := 4
	return &geoShareTable{sets: entries / ways, ways: ways,
		slots: make([]geoShareSlot, (entries/ways)*ways)}
}

func (t *geoShareTable) set(k geoLineKey) []geoShareSlot {
	h := (k.line>>6)*0x9E3779B97F4A7C15 ^ uint64(k.pid)*0xC2B2AE3D27D4EB4F
	s := int((h >> 32) % uint64(t.sets))
	return t.slots[s*t.ways : (s+1)*t.ways]
}

func (t *geoShareTable) record(k geoLineKey, reader int) {
	t.tick++
	set := t.set(k)
	victim := -1
	for i := range set {
		if set[i].valid && set[i].key == k {
			if set[i].first != reader {
				set[i].shared = true
			}
			if set[i].last != reader {
				set[i].prev = set[i].last
				set[i].last = reader
			}
			set[i].lru = t.tick
			return
		}
		switch {
		case !set[i].valid:
			if victim < 0 || set[victim].valid {
				victim = i
			}
		case victim < 0 || (set[victim].valid && set[i].lru < set[victim].lru):
			victim = i
		}
	}
	set[victim] = geoShareSlot{valid: true, key: k, first: reader, lru: t.tick,
		last: reader, prev: -1}
}

// hint returns the most recent reader of k other than reader, or -1.
func (t *geoShareTable) hint(k geoLineKey, reader int) int {
	for _, s := range t.set(k) {
		if s.valid && s.key == k {
			if s.last != reader {
				return s.last
			}
			return s.prev
		}
	}
	return -1
}

func (t *geoShareTable) shared(k geoLineKey) bool {
	for _, s := range t.set(k) {
		if s.valid && s.key == k {
			return s.shared
		}
	}
	return false
}

type geoFillItem struct {
	pid    vm.PID
	addr   uint64
	data   []byte
	origin sim.VTimeInSec
}

// geoPushState is the per-RDMA geo-push state (nil when geo-push is off).
type geoPushState struct {
	cfg          GeoPushConfig
	share        *geoShareTable
	// Affinity mode. Owner: the latest partner list each requester sent.
	partnersOf [][]int
	// Requester: saturating co-read counters per GPM and the current top K.
	affCnt   []uint8
	affHints int
	affTop   []int
	readers      *q2Readers // readers mode (P19 Q2)
	fillQ        []*geoFillItem
	fillInflight map[string]*geoFillItem
	stats        GeoPushStats
}

// ConfigureGeoPush turns geo-push on for this RDMA engine (needs the geo
// topology).
func (c *Comp) ConfigureGeoPush(cfg GeoPushConfig) {
	if !cfg.Enabled() {
		c.geo = nil
		return
	}
	if cfg.Radius > 2 {
		panic("geo-push radius must be 0, 1 or 2")
	}
	if cfg.FillQueue <= 0 {
		cfg.FillQueue = 64
	}
	if cfg.FilterEntries <= 0 {
		cfg.FilterEntries = 1024
	}
	g := &geoPushState{cfg: cfg, fillInflight: make(map[string]*geoFillItem)}
	g.stats.Enabled = true
	if cfg.SharedFilter || cfg.Mode == GeoPushAffinity {
		g.share = newGeoShareTable(cfg.FilterEntries)
	}
	if cfg.Mode == GeoPushAffinity {
		if cfg.AffinityK < 1 || cfg.AffinityK > 2 {
			panic("affinity-k must be 1 or 2")
		}
		if cfg.DecayHints <= 0 {
			cfg.DecayHints = 512
		}
		if cfg.MinCount <= 0 {
			cfg.MinCount = 2
		}
		g.cfg = cfg
		n := 64
		if c.geoTopo != nil {
			n = c.geoTopo.NumGPMs()
		}
		g.partnersOf = make([][]int, n)
		g.affCnt = make([]uint8, n)
	}
	if cfg.Mode == GeoPushReaders {
		g.readers = newQ2Readers(cfg)
	}
	c.geo = g
}

// GetGeoPushStats returns the geo-push counters.
func (c *Comp) GetGeoPushStats() GeoPushStats {
	if c.geo == nil {
		return GeoPushStats{}
	}
	return c.geo.stats
}

// ---------------------------------------------------------------- owner

// geoOwnerRecordRead feeds the sharing filter with a remote read arriving at
// the owner.
func (c *Comp) geoOwnerRecordRead(msg sim.Msg) {
	g := c.geo
	if g != nil && g.readers != nil && c.geoTopo != nil {
		now := c.Engine.CurrentTime()
		switch req := msg.(type) {
		case *BitmapReadReq:
			g.readers.record(uint32(req.PID), req.PagePAddr,
				c.geoTopo.Index(req.Src), now)
		case *mem.ReadReq:
			g.readers.record(uint32(req.PID), req.Address,
				c.geoTopo.Index(req.Src), now)
		}
	}
	if g == nil || g.share == nil || c.geoTopo == nil {
		return
	}
	if g.partnersOf != nil {
		c.affOwnerReadHeader(msg)
	}
	switch req := msg.(type) {
	case *BitmapReadReq:
		reader := c.geoTopo.Index(req.Src)
		for _, l := range bitmapLines(req.LineBitmap) {
			g.share.record(geoLineKey{uint32(req.PID),
				req.PagePAddr + l*remoteLineBytes}, reader)
		}
	case *mem.ReadReq:
		g.share.record(geoLineKey{uint32(req.PID),
			remoteLineAddress(req.Address)}, c.geoTopo.Index(req.Src))
	}
}

// geoWrap returns the packet to send for a read response msg to the
// requesters reqs carrying lines: msg itself, or a GeoMulticast with the
// pushes. Call geoSent after a successful send.
func (c *Comp) geoWrap(
	now sim.VTimeInSec,
	msg sim.Msg,
	reqs []sim.Port,
	pid vm.PID,
	lines []geoPushLine,
	origin sim.VTimeInSec,
) sim.Msg {
	g := c.geo
	if g == nil || c.geoTopo == nil || len(lines) == 0 {
		return msg
	}
	g.stats.OwnerRspCandidates++
	if g.partnersOf != nil {
		c.affSendHints(msg, reqs, pid, lines)
	}
	if g.share != nil && g.cfg.SharedFilter {
		kept := lines[:0:0]
		for _, l := range lines {
			g.stats.FilterLookups++
			if g.share.shared(geoLineKey{uint32(pid), l.addr}) {
				g.stats.FilterShared++
				kept = append(kept, l)
			}
		}
		if len(kept) == 0 {
			g.stats.OwnerFiltered++
			return msg
		}
		lines = kept
	}
	reqIdx := make([]int, 0, len(reqs))
	for _, p := range reqs {
		if i := c.geoTopo.Index(p); i >= 0 {
			reqIdx = append(reqIdx, i)
		}
	}
	var targets []int
	if g.cfg.Mode == GeoPushReaders {
		excl := append([]int{c.geoIdx}, reqIdx...)
		targets = g.readers.targets(uint32(pid), lines[0].addr, now, excl)
	} else if g.cfg.Mode == GeoPushAffinity {
		targets = c.affTargets(reqIdx)
	} else {
		targets = c.geoTopo.PushTargets(c.geoIdx, reqIdx, g.cfg.Radius)
	}
	if len(targets) == 0 {
		g.stats.OwnerNoTarget++
		return msg
	}
	pushDsts := make([]sim.Port, len(targets))
	for i, t := range targets {
		pushDsts[i] = c.geoTopo.Ports[t]
	}
	innerDsts := reqs
	if mc, ok := msg.(messaging.MulticastMsg); ok {
		innerDsts = mc.MulticastDsts()
	}
	bytes := msg.Meta().TrafficBytes
	if _, isMC := msg.(*T18MulticastRsp); !isMC {
		bytes += t18MulticastHeaderBytes
	}
	w := &GeoMulticast{
		MsgMeta: sim.MsgMeta{
			ID:           sim.GetIDGenerator().Generate(),
			Src:          c.ToOutside,
			Dst:          innerDsts[0],
			SendTime:     now,
			TrafficBytes: bytes,
		},
		Inner:     msg,
		InnerDsts: append([]sim.Port(nil), innerDsts...),
		PushDsts:  pushDsts,
		Push: &GeoPushMsg{
			PID:    pid,
			Owner:  c.ToOutside,
			Origin: origin,
			Lines:  lines,
		},
	}
	return w
}

// geoSent records a sent (possibly wrapped) response.
func (c *Comp) geoSent(sent sim.Msg) {
	w, ok := sent.(*GeoMulticast)
	if !ok {
		return
	}
	g := c.geo
	g.stats.OwnerPushPackets++
	g.stats.OwnerPushDests += uint64(len(w.PushDsts))
	g.stats.OwnerPushLines += uint64(len(w.PushDsts) * len(w.Push.Lines))
	reqIdx := make([]int, 0, len(w.InnerDsts))
	all := make([]int, 0, len(w.InnerDsts)+len(w.PushDsts))
	for _, p := range w.InnerDsts {
		if i := c.geoTopo.Index(p); i >= 0 {
			reqIdx = append(reqIdx, i)
			all = append(all, i)
		}
	}
	for _, p := range w.PushDsts {
		all = append(all, c.geoTopo.Index(p))
	}
	extra := c.geoTopo.TreeLinks(c.geoIdx, all) -
		c.geoTopo.TreeLinks(c.geoIdx, reqIdx)
	g.stats.OwnerExtraLinks += uint64(extra)
	g.stats.OwnerExtraLinkByte += uint64(extra * w.TrafficBytes)
}

// geoSend wraps and sends an owner read response; it returns the send error.
func (c *Comp) geoSend(
	now sim.VTimeInSec,
	msg sim.Msg,
	reqs []sim.Port,
	pid vm.PID,
	lines []geoPushLine,
	origin sim.VTimeInSec,
) *sim.SendError {
	out := c.geoWrap(now, msg, reqs, pid, lines, origin)
	if err := c.ToOutside.Send(out); err != nil {
		return err
	}
	c.geoSent(out)
	return nil
}

// ---------------------------------------------------------------- neighbor

// geoReceive handles a push leaf. It never blocks the outside port.
func (c *Comp) geoReceive(now sim.VTimeInSec, msg *GeoPushMsg) {
	c.ToOutside.Retrieve(now)
	g := c.geo
	if g == nil {
		return
	}
	g.stats.RcvMsgs++
	for _, l := range msg.Lines {
		g.stats.RcvLines++
		c.geoReceiveLine(msg, l)
	}
}

func (c *Comp) geoReceiveLine(msg *GeoPushMsg, l geoPushLine) {
	g := c.geo
	if !c.remoteConfig.Enabled || c.remoteConfig.DisableRequesterL2 ||
		c.remoteCacheModules == nil {
		g.stats.DropNoL2++
		return
	}
	if c.geoBoundarySet && msg.Origin <= c.geoLastBoundary {
		g.stats.DropStale++
		return
	}
	c.ensureRemoteDataPathState()
	identity := c.remoteIdentity(
		&mem.ReadReq{Address: l.addr, PID: msg.PID},
		c.RemoteRDMAAddressTable.Find(l.addr))
	if c.remoteUncacheable[identity] {
		g.stats.DropUncacheable++
		return
	}
	key := remoteLineKey{remoteLineIdentity: identity,
		epoch: c.remoteEpochs[identity]}
	if entry := c.remoteLines[key]; entry != nil {
		switch {
		case entry.state == remoteLineReady || len(entry.data) > 0:
			g.stats.DropHave++
		case entry.boundaryStale:
			g.stats.DropStale++
		case entry.state == remoteLineProbing:
			g.stats.DropProbing++
		case len(entry.waiters) == 0:
			g.stats.DropInflightNoWtr++
		default:
			if entry.state == remoteLineInflight {
				g.stats.CatchInflight++
			} else {
				g.stats.CatchQueued++
			}
			g.stats.CatchWaiters += uint64(len(entry.waiters))
			entry.data = append([]byte(nil), l.data...)
			entry.geoEarly = true
			c.queueRemoteReady(entry)
		}
		return
	}
	if len(g.fillQ) >= g.cfg.FillQueue {
		g.stats.DropQueueFull++
		return
	}
	g.fillQ = append(g.fillQ, &geoFillItem{
		pid: msg.PID, addr: l.addr, data: l.data, origin: msg.Origin})
}

// geoSendFills sends queued push fills to the requester L2.
func (c *Comp) geoSendFills(now sim.VTimeInSec) bool {
	g := c.geo
	progress := false
	for i := 0; i < c.effectivePipelineWidth() && len(g.fillQ) > 0; i++ {
		item := g.fillQ[0]
		dst := c.remoteCacheModules.Find(item.addr)
		if dst == nil {
			g.fillQ = g.fillQ[1:]
			g.stats.DropNoL2++
			progress = true
			continue
		}
		fill := mem.RemoteDataFillBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToL2).
			WithDst(dst).
			WithPID(item.pid).
			WithAddress(item.addr).
			WithData(append([]byte(nil), item.data...)).
			WithPush(item.origin).
			Build()
		if err := c.ToL2.Send(fill); err != nil {
			break
		}
		g.fillQ = g.fillQ[1:]
		g.fillInflight[fill.ID] = item
		g.stats.FillsSent++
		progress = true
	}
	return progress
}

func (c *Comp) isGeoFillRsp(rsp mem.AccessRsp) bool {
	if c.geo == nil {
		return false
	}
	fillRsp, ok := rsp.(*mem.RemoteDataFillRsp)
	return ok && c.geo.fillInflight[fillRsp.GetRspTo()] != nil
}

func (c *Comp) processGeoFillRsp(now sim.VTimeInSec, rsp mem.AccessRsp) bool {
	fillRsp := rsp.(*mem.RemoteDataFillRsp)
	delete(c.geo.fillInflight, fillRsp.GetRspTo())
	if fillRsp.Installed {
		c.geo.stats.FillsInstalled++
	}
	c.ToL2.Retrieve(now)
	return true
}

func (c *Comp) geoHasPendingWork() bool {
	return c.geo != nil && (len(c.geo.fillQ) > 0 || len(c.geo.fillInflight) > 0)
}
