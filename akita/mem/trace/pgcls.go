package trace

// Page classification statistics (-report-pgcls, ISCA27 pgcls task).
//
// Read-only accounting of every L1-miss level memory access, per 4 KB
// physical page:
//   - remote reads/writes are recorded by the requester RDMA when it first
//     sees a request from an L1 (the same point as ObsAdmit), so the owner is
//     exact and every record is one network round trip in the baseline;
//   - local reads/writes are recorded by the L1V when it sends a miss / a
//     write to a local L2 slice (needed to evaluate moving a page away from
//     its owner).
//
// Nothing here sends messages, schedules events or touches component state,
// so enabling it does not change timing. All hooks return immediately when
// it is off.

import (
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"math/bits"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/sarchlab/akita/v3/sim"
)

const (
	pgclsLog2Page = 12
	pgclsLog2Line = 6
	pgclsSubs     = 8 // 512 B sub-blocks per 4 KB page
	pgclsLog2Sub  = 9
)

// PgclsConfig configures the page classification statistics.
type PgclsConfig struct {
	// Prefix of the output files.
	Prefix string
	// SampleEvery: raw-trace page sampling, keep every access of pages whose
	// hash is 0 mod SampleEvery. 0 disables the raw trace.
	SampleEvery uint64
	// WindowNS is the time window of the per-window reader sets.
	WindowNS float64
	// TraceExt (ISCA'27 P19 -pgcls-trace-ext) adds two row kinds to the raw
	// trace of sampled pages: op N = a remote read line leaving the
	// requester RDMA for the network (demand or prefetch), op D = the reply
	// to the L1V of a remote read (lat_ns = reply - RDMA admission; t_ns is
	// the reply time), plus the lat_ns column.
	TraceExt bool
}

type pgclsReader struct {
	reads    [pgclsSubs]uint32 // line reads per 512 B sub-block
	writes   uint32
	wbytes   uint64
	lineMask uint64 // lines ever read
	maskThis uint64 // lines read in the current own kernel (no write since)
	maskPrev uint64 // lines read in earlier own kernels (no write since)
	curK     int32
	nK       uint32 // own kernels with at least one read
	firstK   int32
	lastK    int32
	repIntra uint32 // re-read of a line already read in this kernel
	repCross uint32 // re-read of a line read in an earlier kernel, unwritten
	firstNS  float64
	lastNS   float64
}

type pgclsSet struct {
	cur      int64
	mask     uint64
	reads    uint64
	prevMask uint64
	n        uint32  // sets closed
	wsum     float64 // sum popcount(mask)*reads
	rsum     uint64  // sum reads
	jacc     float64 // sum Jaccard(prev, cur) over consecutive active sets
	trans    uint32
	maxPop   uint32
}

func (s *pgclsSet) add(idx int64, g int) {
	if s.reads > 0 && idx != s.cur {
		s.close()
	}
	s.cur = idx
	s.mask |= 1 << uint(g)
	s.reads++
}

func (s *pgclsSet) close() {
	if s.reads == 0 {
		return
	}
	p := uint32(bits.OnesCount64(s.mask))
	s.n++
	s.wsum += float64(p) * float64(s.reads)
	s.rsum += s.reads
	if p > s.maxPop {
		s.maxPop = p
	}
	if s.prevMask != 0 {
		u := bits.OnesCount64(s.prevMask | s.mask)
		i := bits.OnesCount64(s.prevMask & s.mask)
		s.jacc += float64(i) / float64(u)
		s.trans++
	}
	s.prevMask = s.mask
	s.mask = 0
	s.reads = 0
}

type pgclsPage struct {
	owner      int
	firstNS    float64
	lastNS     float64
	rdRemote   uint64
	rdLocal    uint64
	wrRemote   uint64
	wrLocal    uint64
	writerMask uint64
	subMask    [pgclsSubs]uint64
	readers    map[int]*pgclsReader
	lastWriter []int8  // per line, allocated on the first write
	lastWrK    []int32 // global kernel of the line's last write
	rdOtherWr  uint64  // reads of a line last written by another GPM ...
	rdOtherWrK uint64  // ... of which in the same (global) kernel as the write
	kset       pgclsSet
	wset       pgclsSet
	netLines   uint64  // remote read lines sent to the network (incl. prefetch)
	netHops    uint64  // ... x Manhattan hops requester-owner
	latSum     float64 // L1V remote read latency, RDMA admission -> reply to L1 (ns)
	latN       uint64
}

type pgclsPending struct {
	page  uint64
	t0    float64
	g, o  int    // TraceExt only
	off   uint64 // TraceExt only
	gk, k int32  // TraceExt only
}

type pgclsState struct {
	cfg      PgclsConfig
	tile     map[string]int
	pages    map[uint64]*pgclsPage
	kcount   [64]int32
	kepoch   [64]int32 // global kernel of the tile's last launch
	globalK  int32
	kernels  []string
	totals   map[string]uint64
	pending  map[string]pgclsPending
	raw      *csv.Writer
	rawGz    *gzip.Writer
	rawFile  *os.File
	rawCount uint64
}

var (
	pgclsMu sync.Mutex
	pgclsS  *pgclsState
	pgclsOn bool
)

// EnablePgcls turns the page classification statistics on.
func EnablePgcls(cfg PgclsConfig) error {
	pgclsMu.Lock()
	defer pgclsMu.Unlock()
	if cfg.WindowNS <= 0 {
		cfg.WindowNS = 10000
	}
	s := &pgclsState{cfg: cfg, tile: map[string]int{},
		pages: map[uint64]*pgclsPage{}, totals: map[string]uint64{},
		pending: map[string]pgclsPending{}}
	if cfg.SampleEvery > 0 {
		f, err := os.Create(cfg.Prefix + "_trace.csv.gz")
		if err != nil {
			return err
		}
		s.rawFile = f
		s.rawGz = gzip.NewWriter(f)
		s.raw = csv.NewWriter(s.rawGz)
		head := []string{"t_ns", "gk", "k", "req", "own", "page",
			"off", "bytes", "op", "src"}
		if cfg.TraceExt {
			head = append(head, "lat_ns")
		}
		_ = s.raw.Write(head)
	}
	pgclsS = s
	pgclsOn = true
	return nil
}

// DisablePgcls turns the statistics off and drops the state.
func DisablePgcls() {
	pgclsMu.Lock()
	defer pgclsMu.Unlock()
	pgclsS = nil
	pgclsOn = false
}

// PgclsEnabled reports whether the page classification hooks are active.
func PgclsEnabled() bool { return pgclsOn }

func (s *pgclsState) tileOf(name string) int {
	if t, ok := s.tile[name]; ok {
		return t
	}
	t := parseGPMID(name)
	s.tile[name] = t
	return t
}

// PgclsKernelLaunch records a kernel launch accepted by the GPM at tile.
func PgclsKernelLaunch(tile int, now sim.VTimeInSec) {
	if !pgclsOn {
		return
	}
	pgclsMu.Lock()
	defer pgclsMu.Unlock()
	s := pgclsS
	if s == nil || tile < 0 || tile >= 64 {
		return
	}
	s.kcount[tile]++
	// Kernels run one after another; a kernel launches on a GPM at most
	// once, so a second launch on a GPM within the current global kernel
	// starts the next global kernel.
	if s.globalK == 0 || s.kepoch[tile] == s.globalK {
		s.globalK++
	}
	s.kepoch[tile] = s.globalK
	s.kernels = append(s.kernels, fmt.Sprintf("%d,%d,%d,%.3f",
		tile, s.kcount[tile], s.globalK, float64(now)*1e9))
}

// PgclsAccess records one L1-miss level access. requester is the name of
// the requesting component (GPU[n]...), owner the name of the owner RDMA
// port for a remote access or "" for a local one. src is the kind of the L1
// that issued it ("v", "s", "i"); only L1V accesses enter the page
// statistics.
func PgclsAccess(requester, owner, id string, addr, bytes uint64,
	isRead bool, src string, now sim.VTimeInSec) {
	if !pgclsOn {
		return
	}
	pgclsMu.Lock()
	defer pgclsMu.Unlock()
	s := pgclsS
	if s == nil {
		return
	}
	g := s.tileOf(requester)
	local := owner == ""
	o := g
	if !local {
		o = s.tileOf(owner)
	}
	op := "W"
	if isRead {
		op = "R"
	}
	where := "remote"
	if local {
		where = "local"
	}
	s.totals[where+"_"+op+"_"+src]++
	s.totals[where+"_"+op+"_"+src+"_bytes"] += bytes
	if src != "v" || g < 0 || g >= 64 || o < 0 || o >= 64 {
		return
	}
	t := float64(now) * 1e9
	pageNo := addr >> pgclsLog2Page
	off := addr & (1<<pgclsLog2Page - 1)
	line := off >> pgclsLog2Line
	sub := off >> pgclsLog2Sub
	k := s.kcount[g]

	p := s.pages[pageNo]
	if p == nil {
		p = &pgclsPage{owner: o, firstNS: t, readers: map[int]*pgclsReader{}}
		p.kset.cur, p.wset.cur = -1, -1
		s.pages[pageNo] = p
	}
	if t > p.lastNS {
		p.lastNS = t
	}
	if s.raw != nil && pgclsHash(pageNo)%s.cfg.SampleEvery == 0 {
		s.rawCount++
		_ = s.raw.Write([]string{strconv.FormatFloat(t, 'f', 1, 64),
			strconv.Itoa(int(s.globalK)), strconv.Itoa(int(k)),
			strconv.Itoa(g), strconv.Itoa(o),
			strconv.FormatUint(pageNo, 10), strconv.FormatUint(off, 10),
			strconv.FormatUint(bytes, 10), op, src, ""}[:10+s.extCols()])
	}
	r := p.readers[g]
	if r == nil {
		r = &pgclsReader{curK: k, firstK: k, lastK: k, firstNS: t}
		p.readers[g] = r
	}
	r.lastNS = t
	if !isRead {
		r.writes++
		r.wbytes += bytes
		if local {
			p.wrLocal++
		} else {
			p.wrRemote++
		}
		p.writerMask |= 1 << uint(g)
		if p.lastWriter == nil {
			p.lastWriter = make([]int8, 1<<(pgclsLog2Page-pgclsLog2Line))
			p.lastWrK = make([]int32, 1<<(pgclsLog2Page-pgclsLog2Line))
			for i := range p.lastWriter {
				p.lastWriter[i] = -1
			}
		}
		last := line
		if bytes > 0 {
			last = (off + bytes - 1) >> pgclsLog2Line
			if last > 63 {
				last = 63
			}
		}
		for l := line; l <= last; l++ {
			p.lastWriter[l] = int8(g)
			p.lastWrK[l] = s.globalK
			clr := ^(uint64(1) << l)
			for _, rr := range p.readers {
				rr.maskThis &= clr
				rr.maskPrev &= clr
			}
		}
		return
	}

	if local {
		p.rdLocal++
	} else {
		p.rdRemote++
		if id != "" {
			s.pending[id] = pgclsPending{page: pageNo, t0: t, g: g, o: o,
				off: off, gk: int32(s.globalK), k: int32(k)}
		}
	}
	if p.lastWriter != nil && p.lastWriter[line] >= 0 &&
		int(p.lastWriter[line]) != g {
		p.rdOtherWr++
		if p.lastWrK[line] == s.globalK {
			p.rdOtherWrK++
		}
	}
	p.subMask[sub] |= 1 << uint(g)
	p.kset.add(int64(s.globalK), g)
	p.wset.add(int64(t/s.cfg.WindowNS), g)

	if r.nK == 0 {
		r.nK = 1
		r.curK, r.firstK = k, k
	} else if k != r.curK {
		r.maskPrev |= r.maskThis
		r.maskThis = 0
		r.curK = k
		r.nK++
	}
	r.lastK = k
	bit := uint64(1) << line
	switch {
	case r.maskThis&bit != 0:
		r.repIntra++
	case r.maskPrev&bit != 0:
		r.repCross++
	}
	r.maskThis |= bit
	r.lineMask |= bit
	r.reads[sub]++
}

// PgclsNetLine records one remote read line leaving the requester RDMA for
// the network (demand or prefetch).
func PgclsNetLine(requester, owner string, addr uint64, now sim.VTimeInSec) {
	if !pgclsOn {
		return
	}
	pgclsMu.Lock()
	defer pgclsMu.Unlock()
	s := pgclsS
	if s == nil {
		return
	}
	g, o := s.tileOf(requester), s.tileOf(owner)
	s.totals["net_lines"]++
	p := s.pages[addr>>pgclsLog2Page]
	if p == nil {
		s.totals["net_lines_unknown_page"]++
		return
	}
	p.netLines++
	if h := manhattanHops(g, o, 7); h > 0 {
		p.netHops += uint64(h)
	}
	pageNo := addr >> pgclsLog2Page
	if s.cfg.TraceExt && s.raw != nil &&
		pgclsHash(pageNo)%s.cfg.SampleEvery == 0 {
		s.rawCount++
		_ = s.raw.Write([]string{
			strconv.FormatFloat(float64(now)*1e9, 'f', 1, 64),
			strconv.Itoa(int(s.globalK)), "", strconv.Itoa(g), strconv.Itoa(o),
			strconv.FormatUint(pageNo, 10),
			strconv.FormatUint(addr&(1<<pgclsLog2Page-1), 10), "64", "N",
			"net", ""})
	}
}

// PgclsDone records the reply to the L1 of a remote read admitted by
// PgclsAccess (latency = reply time - RDMA admission time).
func PgclsDone(id string, now sim.VTimeInSec) {
	if !pgclsOn {
		return
	}
	pgclsMu.Lock()
	defer pgclsMu.Unlock()
	s := pgclsS
	if s == nil {
		return
	}
	pd, ok := s.pending[id]
	if !ok {
		return
	}
	delete(s.pending, id)
	if p := s.pages[pd.page]; p != nil {
		p.latSum += float64(now)*1e9 - pd.t0
		p.latN++
	}
	if s.cfg.TraceExt && s.raw != nil &&
		pgclsHash(pd.page)%s.cfg.SampleEvery == 0 {
		s.rawCount++
		t := float64(now) * 1e9
		_ = s.raw.Write([]string{strconv.FormatFloat(t, 'f', 1, 64),
			strconv.Itoa(int(pd.gk)), strconv.Itoa(int(pd.k)),
			strconv.Itoa(pd.g), strconv.Itoa(pd.o),
			strconv.FormatUint(pd.page, 10), strconv.FormatUint(pd.off, 10),
			"", "D", "v", strconv.FormatFloat(t-pd.t0, 'f', 1, 64)})
	}
}

func pgclsHash(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// PgclsWrite writes the statistics (<prefix>_pages.csv.gz,
// _readers.csv.gz, _kernels.csv, _totals.csv) and closes the raw trace.
func PgclsWrite(endNS float64) error {
	pgclsMu.Lock()
	defer pgclsMu.Unlock()
	s := pgclsS
	if s == nil {
		return nil
	}
	pgclsOn = false
	if s.raw != nil {
		s.raw.Flush()
		_ = s.rawGz.Close()
		_ = s.rawFile.Close()
	}
	keys := make([]uint64, 0, len(s.pages))
	for k := range s.pages {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	pw, pclose, err := pgclsGz(s.cfg.Prefix + "_pages.csv.gz")
	if err != nil {
		return err
	}
	rw, rclose, err := pgclsGz(s.cfg.Prefix + "_readers.csv.gz")
	if err != nil {
		return err
	}
	_ = pw.Write([]string{"page", "owner", "first_ns", "last_ns",
		"rd_remote", "rd_local", "wr_remote", "wr_local", "writer_mask",
		"sub_masks", "rd_other_written", "rd_other_written_same_k",
		"k_n", "k_wsum", "k_rsum", "k_jacc", "k_trans", "k_maxpop",
		"w_n", "w_wsum", "w_rsum", "w_jacc", "w_trans", "w_maxpop",
		"net_lines", "net_hops", "lat_sum_ns", "lat_n"})
	_ = rw.Write([]string{"page", "reader", "sub_reads", "reads", "writes",
		"wbytes", "line_mask", "n_k", "first_k", "last_k", "rep_intra",
		"rep_cross", "first_ns", "last_ns"})
	hex := func(x uint64) string { return strconv.FormatUint(x, 16) }
	u := func(x uint64) string { return strconv.FormatUint(x, 10) }
	f := func(x float64) string { return strconv.FormatFloat(x, 'f', 3, 64) }
	for _, pg := range keys {
		p := s.pages[pg]
		p.kset.close()
		p.wset.close()
		subs := make([]string, pgclsSubs)
		for i, m := range p.subMask {
			subs[i] = hex(m)
		}
		_ = pw.Write([]string{u(pg), strconv.Itoa(p.owner), f(p.firstNS),
			f(p.lastNS), u(p.rdRemote), u(p.rdLocal), u(p.wrRemote),
			u(p.wrLocal), hex(p.writerMask), strings.Join(subs, ";"),
			u(p.rdOtherWr), u(p.rdOtherWrK),
			u(uint64(p.kset.n)), f(p.kset.wsum), u(p.kset.rsum),
			f(p.kset.jacc), u(uint64(p.kset.trans)), u(uint64(p.kset.maxPop)),
			u(uint64(p.wset.n)), f(p.wset.wsum), u(p.wset.rsum),
			f(p.wset.jacc), u(uint64(p.wset.trans)), u(uint64(p.wset.maxPop)),
			u(p.netLines), u(p.netHops), f(p.latSum), u(p.latN)})
		gs := make([]int, 0, len(p.readers))
		for g := range p.readers {
			gs = append(gs, g)
		}
		sort.Ints(gs)
		for _, g := range gs {
			r := p.readers[g]
			rs := make([]string, pgclsSubs)
			var tot uint64
			for i, v := range r.reads {
				rs[i] = u(uint64(v))
				tot += uint64(v)
			}
			_ = rw.Write([]string{u(pg), strconv.Itoa(g), strings.Join(rs, ";"),
				u(tot), u(uint64(r.writes)), u(r.wbytes), hex(r.lineMask),
				u(uint64(r.nK)), strconv.Itoa(int(r.firstK)),
				strconv.Itoa(int(r.lastK)), u(uint64(r.repIntra)),
				u(uint64(r.repCross)), f(r.firstNS), f(r.lastNS)})
		}
	}
	if err := pclose(); err != nil {
		return err
	}
	if err := rclose(); err != nil {
		return err
	}

	kf, err := os.Create(s.cfg.Prefix + "_kernels.csv")
	if err != nil {
		return err
	}
	fmt.Fprintln(kf, "tile,k,global_k,t_ns")
	for _, l := range s.kernels {
		fmt.Fprintln(kf, l)
	}
	_ = kf.Close()

	tf, err := os.Create(s.cfg.Prefix + "_totals.csv")
	if err != nil {
		return err
	}
	s.totals["pages"] = uint64(len(s.pages))
	s.totals["pending_at_end"] = uint64(len(s.pending))
	s.totals["raw_records"] = s.rawCount
	s.totals["raw_sample_every"] = s.cfg.SampleEvery
	s.totals["window_ns"] = uint64(s.cfg.WindowNS)
	s.totals["end_ns"] = uint64(endNS)
	tk := make([]string, 0, len(s.totals))
	for k := range s.totals {
		tk = append(tk, k)
	}
	sort.Strings(tk)
	fmt.Fprintln(tf, "key,value")
	for _, k := range tk {
		fmt.Fprintf(tf, "%s,%d\n", k, s.totals[k])
	}
	return tf.Close()
}

func pgclsGz(path string) (*csv.Writer, func() error, error) {
	fh, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	gz := gzip.NewWriter(fh)
	w := csv.NewWriter(gz)
	return w, func() error {
		w.Flush()
		if err := w.Error(); err != nil {
			return err
		}
		if err := gz.Close(); err != nil {
			return err
		}
		return fh.Close()
	}, nil
}

func (s *pgclsState) extCols() int {
	if s.cfg.TraceExt {
		return 1
	}
	return 0
}
