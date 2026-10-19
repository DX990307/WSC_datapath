package runner

// P12 statistics for the T18/T19 measurements (statistics only, off by
// default). Like the T16 per-GPM tracer (gpmtracer.go), both tracers attach
// passive akita tracing hooks and never send a message or schedule an event,
// so they cannot change timing.
//
// -report-wg-footprint (T19): for every work-group, the set of 4 KB pages
// (virtual page numbers, address >> 12) that its vector loads read, observed
// at the CU when the load leaves the vector memory unit (before the L1V, so
// L1V hits count). Scalar loads (kernel arguments, constants) and stores are
// not included. A WG records at most -wg-footprint-max-pages distinct pages;
// reads to further new pages are counted (overflow_reads) and the WG is
// marked truncated. One gzip CSV row per WG, written when the WG completes.
//
// -report-m1-sharing (T18): remote reads, observed at the L1V when a miss
// leaves for the GPM's RDMA engine (physical line = address >> 6; the owner
// GPM is address / 8 GB - 1, the platform's memory map):
//   - in-flight duplicates at the requester: a remote read whose line already
//     has an outstanding remote read (L1V send to L1V response) from the same
//     GPM (another CU's L1V) and/or from other GPMs;
//   - in-flight duplicates at the owner: a request that the owner's RDMA
//     forwards to its L2 while a request for the same line is still
//     outstanding there (RDMA send to L2 until the response is handled);
//   - sharing in fixed time windows (aligned to 0, by the L1V send time):
//     for every (window, line), the set of requester GPMs, with the hop
//     distances between them, to the owner, and the size of the YX-routed
//     multicast tree from the owner (the mesh routes Y first).

import (
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"math"
	"math/bits"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
	"github.com/sarchlab/mgpusim/v3/kernels"
	"github.com/sarchlab/mgpusim/v3/protocol"
	"github.com/sarchlab/mgpusim/v3/timing/cu"
)

var wgFootprintFlag = flag.Bool("report-wg-footprint", false,
	"Statistics only (P12/T19): write, per work-group, the 4 KB pages its "+
		"vector loads read (gzip CSV). Does not change timing.")
var wgFootprintFileFlag = flag.String("wg-footprint-file", "",
	"Output file of -report-wg-footprint. Defaults to "+
		"<metric-file-name>_wg_footprint.csv.gz (kernels in ..._wg_kernels.csv).")
var wgFootprintMaxPagesFlag = flag.Int("wg-footprint-max-pages", 4096,
	"-report-wg-footprint: distinct pages recorded per WG; further new pages "+
		"are only counted and the WG is marked truncated.")
var m1SharingFlag = flag.Bool("report-m1-sharing", false,
	"Statistics only (P12/T18): remote-read sharing across GPMs in 1 and 10 us "+
		"windows and in-flight duplicate remote reads (requester and owner side). "+
		"Does not change timing.")
var m1SharingFileFlag = flag.String("m1-sharing-file", "",
	"Output prefix of -report-m1-sharing. Defaults to <metric-file-name>_m1 "+
		"(_m1_windows.csv, _m1_pairhops.csv, _m1_inflight.csv).")

// gpmMemBytes is the physical memory of one GPM: GPM index i (driver ID
// i+1) owns [ (i+1)*8 GB, (i+2)*8 GB ) (timingplatform.go createGPU).
const gpmMemBytes = 8 << 30

var p12TileRe = regexp.MustCompile(`GPU\[(\d+)\]`)

// p12GPMs gives each GPM index its mesh coordinates.
type p12GPMs struct {
	x, y      []int
	tileToIdx map[int]int
	width     int
	height    int
}

func newP12GPMs(names []string) *p12GPMs {
	g := &p12GPMs{tileToIdx: map[int]int{}, width: gpmTraceMeshWidth}
	for i, n := range names {
		m := p12TileRe.FindStringSubmatch(n)
		if m == nil {
			panic(fmt.Sprintf("p12 trace: cannot parse tile from %q", n))
		}
		t, _ := strconv.Atoi(m[1])
		g.tileToIdx[t] = i
		g.x = append(g.x, t%g.width)
		g.y = append(g.y, t/g.width)
		if t/g.width+1 > g.height {
			g.height = t/g.width + 1
		}
	}
	return g
}

func (g *p12GPMs) hops(a, b int) int {
	return meshHops(g.x[a], g.y[a], g.x[b], g.y[b])
}

// indexOfName returns the GPM index of a component name, or -1.
func (g *p12GPMs) indexOfName(name string) int {
	m := p12TileRe.FindStringSubmatch(name)
	if m == nil {
		return -1
	}
	t, _ := strconv.Atoi(m[1])
	if i, ok := g.tileToIdx[t]; ok {
		return i
	}
	return -1
}

// ---------------------------------------------------------------------------
// WG footprint

type wgFPKernel struct {
	id       int
	name     string
	cmd      string
	totalWG  int
	launchNS float64
}

type wgFPRec struct {
	kernel        int
	flat          uint64
	gpm, cu       int
	startNS       float64
	firstReadNS   float64
	lastReadNS    float64
	reads         uint64
	overflowReads uint64
	truncated     bool
	pages         map[uint64]struct{}
}

type wgFootprintAgg struct {
	sync.Mutex

	maxPages int
	kernels  []*wgFPKernel
	byCmd    map[string]int
	byPacket map[*kernels.HsaKernelDispatchPacket]int
	active   map[string]*wgFPRec // by MapWGReq ID

	w       io.Writer
	written uint64
	err     error
}

var wgFootprintHeader = "kernel,wg,gpm,cu,start_ns,end_ns,first_read_ns,last_read_ns," +
	"reads,pages,truncated,overflow_reads,page_list"

func newWGFootprintAgg(w io.Writer, maxPages int) *wgFootprintAgg {
	a := &wgFootprintAgg{
		maxPages: maxPages,
		byCmd:    map[string]int{},
		byPacket: map[*kernels.HsaKernelDispatchPacket]int{},
		active:   map[string]*wgFPRec{},
		w:        w,
	}
	_, a.err = fmt.Fprintln(w, wgFootprintHeader)
	return a
}

// launch maps a LaunchKernelReq's packet to a kernel. A unified launch is
// one kernel per driver command (each GPM gets its own packet copy); any
// other launch is a kernel of its own.
func (a *wgFootprintAgg) launch(cmd, name string, pkt *kernels.HsaKernelDispatchPacket,
	unified bool, nowNS float64) {
	a.Lock()
	defer a.Unlock()
	k, ok := -1, false
	if unified {
		k, ok = a.byCmd[cmd]
	}
	if !ok {
		k = len(a.kernels)
		a.kernels = append(a.kernels, &wgFPKernel{
			id: k, name: name, cmd: cmd, totalWG: totalWGs(pkt), launchNS: nowNS})
		if unified {
			a.byCmd[cmd] = k
		}
	}
	a.byPacket[pkt] = k
}

func flatWGID(wg *kernels.WorkGroup) uint64 {
	flat := uint64(wg.IDX)
	if p := wg.Packet; p != nil && p.WorkgroupSizeX > 0 && p.WorkgroupSizeY > 0 {
		nx := (uint64(p.GridSizeX)-1)/uint64(p.WorkgroupSizeX) + 1
		ny := (uint64(p.GridSizeY)-1)/uint64(p.WorkgroupSizeY) + 1
		flat = uint64(wg.IDZ)*nx*ny + uint64(wg.IDY)*nx + uint64(wg.IDX)
	}
	return flat
}

func (a *wgFootprintAgg) wgStart(req *protocol.MapWGReq, gpm, cuIdx int, nowNS float64) {
	if req == nil || req.WorkGroup == nil {
		return
	}
	a.Lock()
	defer a.Unlock()
	k, ok := a.byPacket[req.WorkGroup.Packet]
	if !ok {
		k = -1
	}
	a.active[req.ID] = &wgFPRec{
		kernel: k, flat: flatWGID(req.WorkGroup), gpm: gpm, cu: cuIdx,
		startNS: nowNS, pages: map[uint64]struct{}{},
	}
}

func (a *wgFootprintAgg) read(mapReqID string, addr uint64, nowNS float64) {
	a.Lock()
	defer a.Unlock()
	r := a.active[mapReqID]
	if r == nil {
		return
	}
	if r.reads == 0 {
		r.firstReadNS = nowNS
	}
	r.reads++
	r.lastReadNS = nowNS
	page := addr >> 12
	if _, ok := r.pages[page]; ok {
		return
	}
	if len(r.pages) >= a.maxPages {
		r.truncated = true
		r.overflowReads++
		return
	}
	r.pages[page] = struct{}{}
}

func (a *wgFootprintAgg) wgEnd(mapReqID string, nowNS float64, ended bool) {
	a.Lock()
	defer a.Unlock()
	r := a.active[mapReqID]
	if r == nil {
		return
	}
	delete(a.active, mapReqID)
	a.writeLocked(r, nowNS, ended)
}

// writeLocked writes one WG: page_list is the sorted page numbers in hex,
// the first absolute and the rest as differences from the previous one,
// separated by ';'.
func (a *wgFootprintAgg) writeLocked(r *wgFPRec, endNS float64, ended bool) {
	if a.err != nil {
		return
	}
	pages := make([]uint64, 0, len(r.pages))
	for p := range r.pages {
		pages = append(pages, p)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i] < pages[j] })
	var sb strings.Builder
	prev := uint64(0)
	for i, p := range pages {
		if i > 0 {
			sb.WriteByte(';')
		}
		sb.WriteString(strconv.FormatUint(p-prev, 16))
		prev = p
	}
	trunc := "0"
	if r.truncated {
		trunc = "1"
	}
	_, a.err = fmt.Fprintf(a.w, "%d,%d,%d,%d,%s,%s,%s,%s,%d,%d,%s,%d,%s\n",
		r.kernel, r.flat, r.gpm, r.cu,
		fmtNS(r.startNS, true), fmtNS(endNS, ended),
		fmtNS(r.firstReadNS, r.reads > 0), fmtNS(r.lastReadNS, r.reads > 0),
		r.reads, len(pages), trunc, r.overflowReads, sb.String())
	a.written++
}

// flushActive writes the WGs that never completed (e.g. -max-wg) with an
// empty end time.
func (a *wgFootprintAgg) flushActive() {
	a.Lock()
	defer a.Unlock()
	ids := make([]string, 0, len(a.active))
	for id := range a.active {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a.writeLocked(a.active[id], 0, false)
		delete(a.active, id)
	}
}

func (a *wgFootprintAgg) writeKernels(w io.Writer) error {
	a.Lock()
	defer a.Unlock()
	if _, err := fmt.Fprintln(w, "kernel,name,total_wg,launch_ns,cmd"); err != nil {
		return err
	}
	for _, k := range a.kernels {
		if _, err := fmt.Fprintf(w, "%d,%s,%d,%s,%s\n", k.id, k.name, k.totalWG,
			fmtNS(k.launchNS, true), k.cmd); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// M1 sharing

// m1Inflight tracks outstanding requests per line.
type m1LineState struct {
	total int
	cnt   map[int]int // requester GPM -> outstanding
}

type m1WinLine struct {
	mask  uint64
	owner int
	reqs  uint32
}

// m1KStats aggregates the (window, line) pairs with k requester GPMs.
type m1KStats struct {
	lines         uint64
	reqs          uint64
	sumOwnerHops  uint64 // over requesters
	sumPairHops   uint64
	pairs         uint64
	sumTreeLinks  uint64 // YX multicast tree from the owner
	sumMaxHops    uint64 // farthest requester from the owner
	sumMinPairHop uint64 // closest pair of requesters
}

type m1Window struct {
	widthNS  float64
	cur      int64
	started  bool
	lines    map[uint64]*m1WinLine
	byK      map[int]*m1KStats
	pairHops map[int]uint64
	windows  uint64
}

type m1Agg struct {
	sync.Mutex
	gpms *p12GPMs
	n    int

	// requester side (L1V send to response)
	reqLines       map[uint64]*m1LineState
	reqOf          map[string][2]uint64 // task ID -> (line, gpm)
	remote         uint64
	dupSame        uint64 // same GPM outstanding (with or without others)
	dupSameOnly    uint64
	dupCross       uint64 // only other GPMs outstanding
	dupBoth        uint64
	crossMinHops   map[int]uint64
	maxOutstanding int

	// owner side (owner RDMA to L2 until response)
	ownLines    map[uint64]*m1LineState
	ownOf       map[string][2]uint64
	ownReads    uint64
	ownDup      uint64
	ownDupOther uint64 // an outstanding request for the line came from another requester GPM
	ownDupSame  uint64 // only the same requester GPM outstanding

	windows []*m1Window
	links   []bool
}

func newM1Agg(gpms *p12GPMs, n int, widthsNS []float64) *m1Agg {
	a := &m1Agg{
		gpms: gpms, n: n,
		reqLines: map[uint64]*m1LineState{}, reqOf: map[string][2]uint64{},
		crossMinHops: map[int]uint64{},
		ownLines:     map[uint64]*m1LineState{}, ownOf: map[string][2]uint64{},
		links: make([]bool, gpms.width*gpms.height*4),
	}
	for _, w := range widthsNS {
		a.windows = append(a.windows, &m1Window{
			widthNS: w, lines: map[uint64]*m1WinLine{},
			byK: map[int]*m1KStats{}, pairHops: map[int]uint64{},
		})
	}
	return a
}

func ownerOf(addr uint64) int { return int(addr/gpmMemBytes) - 1 }

func (s *m1LineState) add(g int) {
	s.total++
	s.cnt[g]++
}

func (s *m1LineState) remove(g int) {
	s.total--
	if s.cnt[g]--; s.cnt[g] <= 0 {
		delete(s.cnt, g)
	}
}

// remoteIssue records a remote read leaving GPM g's L1V.
func (a *m1Agg) remoteIssue(id string, g int, addr uint64, nowNS float64) {
	a.Lock()
	defer a.Unlock()
	line := addr >> 6
	owner := ownerOf(addr)
	a.remote++

	st := a.reqLines[line]
	if st == nil {
		st = &m1LineState{cnt: map[int]int{}}
		a.reqLines[line] = st
	}
	same := st.cnt[g] > 0
	others := st.total - st.cnt[g]
	switch {
	case same && others > 0:
		a.dupSame++
		a.dupBoth++
	case same:
		a.dupSame++
		a.dupSameOnly++
	case others > 0:
		a.dupCross++
		minH := math.MaxInt32
		for o := range st.cnt {
			if o != g {
				if h := a.gpms.hops(g, o); h < minH {
					minH = h
				}
			}
		}
		a.crossMinHops[minH]++
	}
	st.add(g)
	a.reqOf[id] = [2]uint64{line, uint64(g)}
	if len(a.reqOf) > a.maxOutstanding {
		a.maxOutstanding = len(a.reqOf)
	}

	if g >= 64 || owner < 0 {
		return
	}
	for _, w := range a.windows {
		idx := int64(nowNS / w.widthNS)
		if !w.started {
			w.cur, w.started = idx, true
		}
		if idx != w.cur {
			a.flushWindow(w)
			w.cur = idx
		}
		l := w.lines[line]
		if l == nil {
			l = &m1WinLine{owner: owner}
			w.lines[line] = l
		}
		l.mask |= 1 << uint(g)
		l.reqs++
	}
}

func (a *m1Agg) remoteDone(id string) {
	a.Lock()
	defer a.Unlock()
	r, ok := a.reqOf[id]
	if !ok {
		return
	}
	delete(a.reqOf, id)
	if st := a.reqLines[r[0]]; st != nil {
		st.remove(int(r[1]))
		if st.total <= 0 {
			delete(a.reqLines, r[0])
		}
	}
}

// ownerIssue records a request from requester GPM req that the owner's RDMA
// forwards to its L2.
func (a *m1Agg) ownerIssue(id string, req int, addr uint64) {
	a.Lock()
	defer a.Unlock()
	line := addr >> 6
	a.ownReads++
	st := a.ownLines[line]
	if st == nil {
		st = &m1LineState{cnt: map[int]int{}}
		a.ownLines[line] = st
	}
	if st.total > 0 {
		a.ownDup++
		if st.total-st.cnt[req] > 0 {
			a.ownDupOther++
		} else {
			a.ownDupSame++
		}
	}
	st.add(req)
	a.ownOf[id] = [2]uint64{line, uint64(req)}
}

func (a *m1Agg) ownerDone(id string) {
	a.Lock()
	defer a.Unlock()
	r, ok := a.ownOf[id]
	if !ok {
		return
	}
	delete(a.ownOf, id)
	if st := a.ownLines[r[0]]; st != nil {
		st.remove(int(r[1]))
		if st.total <= 0 {
			delete(a.ownLines, r[0])
		}
	}
}

// treeLinks counts the directed mesh links of the YX-routed multicast tree
// from owner o to the GPMs in mask.
func (a *m1Agg) treeLinks(o int, mask uint64) int {
	for i := range a.links {
		a.links[i] = false
	}
	w := a.gpms.width
	n := 0
	mark := func(x, y, dir int) {
		i := (y*w+x)*4 + dir
		if !a.links[i] {
			a.links[i] = true
			n++
		}
	}
	ox, oy := a.gpms.x[o], a.gpms.y[o]
	for m := mask; m != 0; m &= m - 1 {
		r := bits.TrailingZeros64(m)
		rx, ry := a.gpms.x[r], a.gpms.y[r]
		x, y := ox, oy
		for y != ry { // Y first
			if ry > y {
				mark(x, y, 0)
				y++
			} else {
				mark(x, y, 1)
				y--
			}
		}
		for x != rx {
			if rx > x {
				mark(x, y, 2)
				x++
			} else {
				mark(x, y, 3)
				x--
			}
		}
	}
	return n
}

func (a *m1Agg) flushWindow(w *m1Window) {
	if len(w.lines) == 0 {
		return
	}
	w.windows++
	for _, l := range w.lines {
		k := bits.OnesCount64(l.mask)
		s := w.byK[k]
		if s == nil {
			s = &m1KStats{}
			w.byK[k] = s
		}
		s.lines++
		s.reqs += uint64(l.reqs)
		if l.owner >= a.n {
			continue
		}
		maxH := 0
		for m := l.mask; m != 0; m &= m - 1 {
			r := bits.TrailingZeros64(m)
			h := a.gpms.hops(l.owner, r)
			s.sumOwnerHops += uint64(h)
			if h > maxH {
				maxH = h
			}
		}
		s.sumMaxHops += uint64(maxH)
		if k < 2 {
			continue
		}
		minPair := math.MaxInt32
		for m := l.mask; m != 0; m &= m - 1 {
			r := bits.TrailingZeros64(m)
			for m2 := m & (m - 1); m2 != 0; m2 &= m2 - 1 {
				r2 := bits.TrailingZeros64(m2)
				h := a.gpms.hops(r, r2)
				s.sumPairHops += uint64(h)
				s.pairs++
				w.pairHops[h]++
				if h < minPair {
					minPair = h
				}
			}
		}
		s.sumMinPairHop += uint64(minPair)
		s.sumTreeLinks += uint64(a.treeLinks(l.owner, l.mask))
	}
	w.lines = map[uint64]*m1WinLine{}
}

func (a *m1Agg) write(prefix string) error {
	a.Lock()
	defer a.Unlock()
	for _, w := range a.windows {
		a.flushWindow(w)
	}

	f, err := os.Create(prefix + "_windows.csv")
	if err != nil {
		return err
	}
	fmt.Fprintln(f, "window_ns,windows,sharers,lines,requests,sum_owner_hops,"+
		"sum_max_owner_hops,pairs,sum_pair_hops,sum_min_pair_hops,sum_tree_links")
	for _, w := range a.windows {
		ks := make([]int, 0, len(w.byK))
		for k := range w.byK {
			ks = append(ks, k)
		}
		sort.Ints(ks)
		for _, k := range ks {
			s := w.byK[k]
			fmt.Fprintf(f, "%g,%d,%d,%d,%d,%d,%d,%d,%d,%d,%d\n", w.widthNS, w.windows, k,
				s.lines, s.reqs, s.sumOwnerHops, s.sumMaxHops, s.pairs, s.sumPairHops,
				s.sumMinPairHop, s.sumTreeLinks)
		}
	}
	if err := f.Close(); err != nil {
		return err
	}

	f, err = os.Create(prefix + "_pairhops.csv")
	if err != nil {
		return err
	}
	fmt.Fprintln(f, "window_ns,hops,pairs")
	for _, w := range a.windows {
		hs := make([]int, 0, len(w.pairHops))
		for h := range w.pairHops {
			hs = append(hs, h)
		}
		sort.Ints(hs)
		for _, h := range hs {
			fmt.Fprintf(f, "%g,%d,%d\n", w.widthNS, h, w.pairHops[h])
		}
	}
	if err := f.Close(); err != nil {
		return err
	}

	f, err = os.Create(prefix + "_inflight.csv")
	if err != nil {
		return err
	}
	fmt.Fprintln(f, "metric,value")
	rows := [][2]string{
		{"requester_remote_reads", strconv.FormatUint(a.remote, 10)},
		{"requester_dup_same_gpm", strconv.FormatUint(a.dupSame, 10)},
		{"requester_dup_same_gpm_only", strconv.FormatUint(a.dupSameOnly, 10)},
		{"requester_dup_same_and_other_gpm", strconv.FormatUint(a.dupBoth, 10)},
		{"requester_dup_other_gpm_only", strconv.FormatUint(a.dupCross, 10)},
		{"requester_max_outstanding", strconv.Itoa(a.maxOutstanding)},
		{"owner_reads", strconv.FormatUint(a.ownReads, 10)},
		{"owner_dup", strconv.FormatUint(a.ownDup, 10)},
		{"owner_dup_other_requester", strconv.FormatUint(a.ownDupOther, 10)},
		{"owner_dup_same_requester_only", strconv.FormatUint(a.ownDupSame, 10)},
	}
	hs := make([]int, 0, len(a.crossMinHops))
	for h := range a.crossMinHops {
		hs = append(hs, h)
	}
	sort.Ints(hs)
	for _, h := range hs {
		rows = append(rows, [2]string{fmt.Sprintf("requester_dup_other_gpm_only_min_hops_%d", h),
			strconv.FormatUint(a.crossMinHops[h], 10)})
	}
	for _, r := range rows {
		fmt.Fprintf(f, "%s,%s\n", r[0], r[1])
	}
	return f.Close()
}

// ---------------------------------------------------------------------------
// Hooks

type p12Tracer struct {
	fp       *wgFootprintAgg
	fpFile   *os.File
	fpGzip   *gzip.Writer
	fpBuf    *bufio.Writer
	fpPath   string
	m1       *m1Agg
	m1Prefix string
}

func (r *Runner) addP12Tracers() {
	if !*wgFootprintFlag && !*m1SharingFlag {
		return
	}
	names := make([]string, len(r.platform.GPUs))
	portToIdx := map[sim.Port]int{}
	for i, gpu := range r.platform.GPUs {
		names[i] = gpu.Domain.Name()
		if int(gpu.GPUID) != i+1 {
			panic(fmt.Sprintf("p12 trace: GPU %s has driver ID %d at index %d",
				names[i], gpu.GPUID, i))
		}
		portToIdx[gpu.Domain.GetPortByName("CommandProcessor")] = i
	}
	gpms := newP12GPMs(names)
	t := &p12Tracer{}
	r.p12 = t
	engine := r.platform.Engine

	if *wgFootprintFlag {
		t.fpPath = *wgFootprintFileFlag
		if t.fpPath == "" {
			t.fpPath = *filenameFlag + "_wg_footprint.csv.gz"
		}
		f, err := os.Create(t.fpPath)
		if err != nil {
			panic(fmt.Sprintf("wg footprint: %v", err))
		}
		t.fpFile = f
		t.fpGzip = gzip.NewWriter(f)
		t.fpBuf = bufio.NewWriterSize(t.fpGzip, 1<<20)
		t.fp = newWGFootprintAgg(t.fpBuf, *wgFootprintMaxPagesFlag)
		r.addWGFootprintHooks(t, portToIdx, engine)
	}
	if *m1SharingFlag {
		t.m1Prefix = *m1SharingFileFlag
		if t.m1Prefix == "" {
			t.m1Prefix = *filenameFlag + "_m1"
		}
		t.m1 = newM1Agg(gpms, len(names), []float64{1000, 10000})
		r.addM1Hooks(t, engine)
	}
}

func (r *Runner) addWGFootprintHooks(t *p12Tracer, portToIdx map[sim.Port]int,
	engine sim.TimeTeller) {
	tracing.CollectTrace(r.platform.Driver, &gpmTraceHook{
		engine: engine, gpm: -1,
		start: func(h *gpmTraceHook, task tracing.Task) {
			if task.Kind != "req_out" || task.What != "*protocol.LaunchKernelReq" {
				return
			}
			req, ok := task.Detail.(*protocol.LaunchKernelReq)
			if !ok || req.Packet == nil {
				return
			}
			name := ""
			if req.HsaCo != nil && req.HsaCo.Symbol != nil {
				name = req.HsaCo.Symbol.Name
			}
			t.fp.launch(task.ParentID, name, req.Packet, req.WGFilter != nil, h.nowNS())
		},
	})

	for i, gpu := range r.platform.GPUs {
		gpm := i
		for ci, c := range gpu.CUs {
			cuIdx := ci
			cuObj, _ := c.(*cu.ComputeUnit)
			tracing.CollectTrace(c.(tracing.NamedHookable), &gpmTraceHook{
				engine: engine, gpm: gpm,
				start: func(h *gpmTraceHook, task tracing.Task) {
					switch task.Kind {
					case "req_in":
						switch msg := task.Detail.(type) {
						case *protocol.MapWGReq:
							t.fp.wgStart(msg, h.gpm, cuIdx, h.nowNS())
						case *protocol.WGCompletionMsg:
							for _, id := range msg.RspTo {
								t.fp.wgEnd(id, h.nowNS(), true)
							}
						}
					case "req_out":
						read, ok := task.Detail.(*mem.ReadReq)
						if !ok || cuObj == nil {
							return
						}
						inflight := cuObj.InFlightVectorMemAccess
						for j := len(inflight) - 1; j >= 0; j-- {
							if inflight[j].Read != read {
								continue
							}
							wf := inflight[j].Wavefront
							if wf != nil && wf.WG != nil && wf.WG.MapReq != nil {
								t.fp.read(wf.WG.MapReq.ID, read.Address, h.nowNS())
							}
							return
						}
					}
				},
			})
		}
	}
}

func (r *Runner) addM1Hooks(t *p12Tracer, engine sim.TimeTeller) {
	for i, gpu := range r.platform.GPUs {
		gpm := i
		seen := map[string]bool{}
		for _, l1v := range gpu.L1VCaches {
			if !firstTraceForComponent(seen, l1v) {
				continue
			}
			tracing.CollectTrace(l1v, &gpmTraceHook{
				engine: engine, gpm: gpm,
				start: func(h *gpmTraceHook, task tracing.Task) {
					if task.Kind != "req_out" {
						return
					}
					read, ok := task.Detail.(*mem.ReadReq)
					if !ok || read.Dst == nil ||
						!strings.Contains(read.Dst.Name(), ".RDMA.") {
						return
					}
					t.m1.remoteIssue(task.ID, h.gpm, read.Address, h.nowNS())
				},
				end: func(h *gpmTraceHook, task tracing.Task) {
					t.m1.remoteDone(task.ID)
				},
			})
		}
		if gpu.RDMAEngine == nil {
			continue
		}
		tracing.CollectTrace(gpu.RDMAEngine, &gpmTraceHook{
			engine: engine, gpm: gpm,
			start: func(h *gpmTraceHook, task tracing.Task) {
				if task.Kind != "req_in" || !strings.HasSuffix(task.Where, ".OutsideIn") {
					return
				}
				read, ok := task.Detail.(*mem.ReadReq)
				if !ok {
					return
				}
				req := -1
				if read.Src != nil {
					req = t.m1.gpms.indexOfName(read.Src.Name())
				}
				t.m1.ownerIssue(task.ID, req, read.Address)
			},
			end: func(h *gpmTraceHook, task tracing.Task) {
				t.m1.ownerDone(task.ID)
			},
		})
	}
}

func (r *Runner) writeP12Traces() {
	t := r.p12
	if t == nil {
		return
	}
	if t.fp != nil {
		t.fp.flushActive()
		if t.fp.err != nil {
			fmt.Fprintf(os.Stderr, "[P12] write %s: %v\n", t.fpPath, t.fp.err)
		}
		t.fpBuf.Flush()
		t.fpGzip.Close()
		t.fpFile.Close()
		kpath := strings.TrimSuffix(strings.TrimSuffix(t.fpPath, ".gz"), ".csv")
		kpath = strings.TrimSuffix(kpath, "_wg_footprint") + "_wg_kernels.csv"
		if f, err := os.Create(kpath); err == nil {
			if err := t.fp.writeKernels(f); err != nil {
				fmt.Fprintf(os.Stderr, "[P12] write %s: %v\n", kpath, err)
			}
			f.Close()
		} else {
			fmt.Fprintf(os.Stderr, "[P12] cannot write %s: %v\n", kpath, err)
		}
	}
	if t.m1 != nil {
		if err := t.m1.write(t.m1Prefix); err != nil {
			fmt.Fprintf(os.Stderr, "[P12] write %s_*: %v\n", t.m1Prefix, err)
		}
	}
}
