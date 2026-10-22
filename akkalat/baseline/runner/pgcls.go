package runner

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

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
		WindowNS: *pgclsWindowFlag})
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
