package runner

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sarchlab/akita/v3/mem/cache/writearound"
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/timing/cp"
)

// Page classification statistics (ISCA27 pgcls). Read-only accounting, off
// by default; see akita/mem/trace/pgcls.go.
var pgclsFlag = flag.Bool("report-pgcls", false,
	"Per-page access statistics for page classification (L1-miss level: "+
		"remote at the requester RDMA, local at the L1V). Read-only.")
var pgclsFileFlag = flag.String("pgcls-file", "",
	"Output prefix of -report-pgcls (default <metric-file-name>_pgcls).")
var pgclsSampleFlag = flag.Uint64("pgcls-sample", 0,
	"-report-pgcls raw trace: keep every access of 1/N of the pages "+
		"(page-hash sampling); 0 = no raw trace.")
var pgclsWindowFlag = flag.Float64("pgcls-window-ns", 10000,
	"-report-pgcls time window of the per-window reader sets (ns).")

type pgclsRunner struct{ prefix string }

// addPgcls enables the statistics (after the platform is built).
func (r *Runner) addPgcls() {
	memtrace.DisablePgcls()
	if !*pgclsFlag || !r.Timing {
		return
	}
	prefix := *pgclsFileFlag
	if prefix == "" {
		prefix = *filenameFlag + "_pgcls"
	}
	err := memtrace.EnablePgcls(memtrace.PgclsConfig{
		Prefix: prefix, SampleEvery: *pgclsSampleFlag,
		WindowNS: *pgclsWindowFlag, TraceExt: *pgclsTraceExtFlag})
	if err != nil {
		panic(err)
	}
	r.pgcls = &pgclsRunner{prefix: prefix}
	for _, gpu := range r.platform.GPUs {
		c, ok := gpu.CommandProcessor.(*cp.CommandProcessor)
		if !ok {
			continue
		}
		tile := pgclsTile(c.Name())
		c.KernelLaunchHooks = append(c.KernelLaunchHooks,
			func(now sim.VTimeInSec) { memtrace.PgclsKernelLaunch(tile, now) })
	}
	fmt.Printf("[Config] pgcls: prefix=%s sample=%d window=%gns\n",
		prefix, *pgclsSampleFlag, *pgclsWindowFlag)
}

func pgclsTile(name string) int {
	i := strings.Index(name, "GPU[")
	if i < 0 {
		return -1
	}
	rest := name[i+4:]
	j := strings.Index(rest, "]")
	if j < 0 {
		return -1
	}
	t, err := strconv.Atoi(rest[:j])
	if err != nil {
		return -1
	}
	return t
}

func (r *Runner) writePgcls() {
	if r.pgcls == nil {
		return
	}
	endNS := float64(r.platform.Engine.CurrentTime()) * 1e9
	if err := memtrace.PgclsWrite(endNS); err != nil {
		fmt.Fprintf(os.Stderr, "[pgcls] write %s_*: %v\n", r.pgcls.prefix, err)
	}
}

// ---------------------------------------------------------------------------
// Per-page oracle (-pgcls-oracle-pages): L1V misses and writes to the listed
// 4 KB physical pages go to the requester's own L2 slices (and its DRAM
// path) instead of the owner, i.e. the remote cost of those pages is removed
// the way -force-local-data-access removes it for every page. Data stays
// correct through the global storage; pages written by several GPMs can see
// stale copies in another GPM's L2 (an oracle, not a protocol), so check WG
// and instruction counts against the run without the oracle.

var pgclsOracleFlag = flag.String("pgcls-oracle-pages", "",
	"Oracle (analysis only): file with one 4 KB physical page number per "+
		"line; L1V accesses to these pages use the requester's local L2/DRAM "+
		"path. Empty = off.")

type pgclsOracleFinder struct {
	pages map[uint64]struct{}
	local mem.LowModuleFinder
	def   mem.LowModuleFinder
	hits  uint64
}

func (f *pgclsOracleFinder) Find(addr uint64) sim.Port {
	if _, ok := f.pages[addr>>12]; ok {
		f.hits++
		return f.local.Find(addr)
	}
	return f.def.Find(addr)
}

func (r *Runner) addPgclsOracle() {
	if *pgclsOracleFlag == "" || !r.Timing {
		return
	}
	data, err := os.ReadFile(*pgclsOracleFlag)
	if err != nil {
		panic(err)
	}
	pages := map[uint64]struct{}{}
	for _, l := range strings.Fields(string(data)) {
		p, err := strconv.ParseUint(l, 10, 64)
		if err != nil {
			panic(fmt.Sprintf("-pgcls-oracle-pages: bad page %q", l))
		}
		pages[p] = struct{}{}
	}
	n := 0
	for _, gpu := range r.platform.GPUs {
		for _, c := range gpu.L1VCaches {
			l1, ok := c.(*writearound.Cache)
			if !ok {
				panic("-pgcls-oracle-pages: L1V is not a writearound cache")
			}
			def, ok := l1.LowModuleFinder().(*mem.InterleavedLowModuleFinder)
			if !ok {
				panic("-pgcls-oracle-pages: unexpected L1V low module finder")
			}
			local := *def
			local.UseAddressSpaceLimitation = false
			local.ModuleForOtherAddresses = nil
			l1.SetLowModuleFinder(&pgclsOracleFinder{
				pages: pages, local: &local, def: def})
			n++
		}
	}
	fmt.Printf("[Config] pgcls oracle: %d pages local (%s), %d L1V caches\n",
		len(pages), *pgclsOracleFlag, n)
}
