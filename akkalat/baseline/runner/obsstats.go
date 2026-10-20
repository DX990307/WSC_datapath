package runner

// ISCA'27 P13 observation statistics (statistics only, all off by default).
// Passive hooks (akita/mem/trace/obsstats.go, mgpusim/timing/rdma/
// obs_occupancy.go, writeback L2, mesh channels); online aggregation, no
// per-request trace. With every -report-obs-* flag off nothing is enabled and
// the simulator is unchanged.
//
// Output files (prefix -obs-file, default <metric-file-name>_obs):
//   _latency.csv    E1 latency segments by hop count (A1)
//   _occupancy.csv  per-GPM RRT / owner occupancy, 1 us samples (A2)
//   _pairs.csv      per (requester, destination) in-flight samples (A2)
//   _links.csv      per directed mesh link bytes / utilization (A3)
//   _l2free.csv     per L2 slice free-capacity fractions (A4)
//   _summary.csv    scalar metrics: owner concurrency, aggregation (A6),
//                   reuse (A4), mesh totals (A3)

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
)

var obsLatencyFlag = flag.Bool("report-obs-latency", false,
	"Statistics only (P13 A1): remote-read latency segments t0..t6 by hop "+
		"count (mean/p50/p95), owner L2 hit/miss/merge, owner same-line "+
		"concurrency. Does not change timing.")
var obsOccupancyFlag = flag.Bool("report-obs-occupancy", false,
	"Statistics only (P13 A2): every -obs-sample-ns, the RRT entries in use, "+
		"requests waiting for one, owner in-flight count and in-flight per "+
		"destination, per GPM. Does not change timing.")
var obsLinksFlag = flag.Bool("report-obs-links", false,
	"Statistics only (P13 A3): bytes per directed mesh link and per-window "+
		"utilization. Does not change timing.")
var obsReuseFlag = flag.Bool("report-obs-reuse", false,
	"Statistics only (P13 A4): remote line reuse per requester GPM, reuse "+
		"intervals, multicast re-reads, L2 free capacity. Does not change timing.")
var obsReqAggFlag = flag.Bool("report-obs-reqagg", false,
	"Statistics only (P13 A6): requester same-line / same-page aggregation "+
		"opportunity (HPCA Fig. 5 definition) and same-cycle same-page "+
		"packets. Does not change timing.")
var obsAllFlag = flag.Bool("report-obs-all", false,
	"Statistics only (P13): all -report-obs-* statistics.")
var obsFileFlag = flag.String("obs-file", "",
	"Output prefix of the -report-obs-* statistics; default "+
		"<metric-file-name>_obs.")
var obsLabelFlag = flag.String("obs-label", "",
	"Workload label written in the -report-obs-* CSVs; default the "+
		"basename of -metric-file-name.")
var obsSampleNSFlag = flag.Float64("obs-sample-ns", 1000,
	"-report-obs-occupancy sample period and -report-obs-links window (ns).")
var obsAggWindowFlag = flag.Int("obs-agg-window-cycles", 16,
	"-report-obs-reqagg same-line / same-page window in RDMA cycles.")

type obsRunner struct {
	prefix, label string
	occupancy     bool
}

func obsAnyFlag() bool {
	return *obsAllFlag || *obsLatencyFlag || *obsOccupancyFlag ||
		*obsLinksFlag || *obsReuseFlag || *obsReqAggFlag
}

// addObsStats enables the P13 statistics (after the platform is built).
func (r *Runner) addObsStats() {
	memtrace.DisableObsStats()
	if !obsAnyFlag() || !r.Timing {
		return
	}
	all := *obsAllFlag
	cycleNS := 1.0
	for _, gpu := range r.platform.GPUs {
		if gpu.RDMAEngine != nil && gpu.RDMAEngine.Freq > 0 {
			cycleNS = float64(gpu.RDMAEngine.Freq.Period()) * 1e9
			break
		}
	}
	cfg := memtrace.ObsConfig{
		Latency:         all || *obsLatencyFlag,
		ReqAgg:          all || *obsReqAggFlag,
		Reuse:           all || *obsReuseFlag,
		Links:           all || *obsLinksFlag,
		TileWidth:       gpmTraceMeshWidth,
		CycleNS:         cycleNS,
		AggWindowCycles: *obsAggWindowFlag,
		WindowNS:        *obsSampleNSFlag,
		LinkBytesPerNS:  float64(*bandwidthFlag * *networkFlitSizeFlag),
		FlitBytes:       *networkFlitSizeFlag,
	}
	memtrace.EnableObsStats(cfg)

	o := &obsRunner{prefix: *obsFileFlag, label: *obsLabelFlag,
		occupancy: all || *obsOccupancyFlag}
	if o.prefix == "" {
		o.prefix = *filenameFlag + "_obs"
	}
	if o.label == "" {
		o.label = filepath.Base(*filenameFlag)
	}
	r.obs = o
	for _, gpu := range r.platform.GPUs {
		if cfg.Reuse {
			for _, l2 := range gpu.L2Caches {
				if c, ok := l2.(*writeback.Cache); ok {
					c.ObsRegisterL2()
				}
			}
		}
		if o.occupancy && gpu.RDMAEngine != nil {
			gpu.RDMAEngine.EnableObsOccupancy(*obsSampleNSFlag)
		}
	}
	fmt.Printf("[Config] P13 obs stats: latency=%v occupancy=%v links=%v "+
		"reuse=%v reqagg=%v prefix=%s label=%s sample=%gns agg-window=%dcyc "+
		"cycle=%gns link=%gB/ns\n", cfg.Latency, o.occupancy, cfg.Links,
		cfg.Reuse, cfg.ReqAgg, o.prefix, o.label, *obsSampleNSFlag,
		cfg.AggWindowCycles, cycleNS, cfg.LinkBytesPerNS)
}

func (r *Runner) writeObsStats() {
	o := r.obs
	if o == nil {
		return
	}
	endNS := float64(r.platform.Engine.CurrentTime()) * 1e9
	if err := memtrace.ObsWrite(o.prefix, o.label, endNS); err != nil {
		fmt.Fprintf(os.Stderr, "[P13] write %s_*: %v\n", o.prefix, err)
	}
	if o.occupancy {
		if err := r.writeObsOccupancy(o, endNS); err != nil {
			fmt.Fprintf(os.Stderr, "[P13] write %s_occupancy: %v\n", o.prefix, err)
		}
	}
}

func (r *Runner) writeObsOccupancy(o *obsRunner, endNS float64) error {
	bytes, reads := memtrace.ObsLogicalBytes()
	f, err := os.Create(o.prefix + "_occupancy.csv")
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	fmt.Fprintln(w, "workload,gpm,samples,limit,frac_time_full,mean_used,"+
		"max_used,frac_used_ge32,mean_waiting,max_waiting,remote_bw,"+
		"remote_reads,owner_in_flight_mean,owner_in_flight_max,top_dest,"+
		"top_dest_share")
	pf, err := os.Create(o.prefix + "_pairs.csv")
	if err != nil {
		f.Close()
		return err
	}
	pw := bufio.NewWriter(pf)
	fmt.Fprintln(pw, "workload,gpm,dst,mean,max,frac_ge32")

	tiles := make([]int, 0, len(r.platform.GPUs))
	byTile := map[int]int{}
	for i, gpu := range r.platform.GPUs {
		t := obsTileOf(gpu.Domain.Name())
		tiles = append(tiles, t)
		byTile[t] = i
	}
	sort.Ints(tiles)
	for _, t := range tiles {
		gpu := r.platform.GPUs[byTile[t]]
		if gpu.RDMAEngine == nil {
			continue
		}
		gpu.RDMAEngine.ObsOccupancyFinish(endNS)
		occ := gpu.RDMAEngine.ObsOccupancyResult()
		if occ == nil {
			continue
		}
		top, topShare, total := -1, 0.0, 0.0
		topSum := 0.0
		for _, d := range occ.Dests {
			total += d.SumSample
			if d.SumSample > topSum {
				topSum = d.SumSample
				top = obsTileOf(d.Dst)
			}
		}
		if total > 0 {
			topShare = topSum / total
		}
		bw := 0.0
		if endNS > 0 {
			bw = float64(bytes[t]) / endNS
		}
		fmt.Fprintf(w, "%s,%d,%d,%d,%.6g,%.6g,%d,%.6g,%.6g,%d,%.6g,%d,%.6g,%d,%d,%.6g\n",
			o.label, t, occ.Samples, occ.Limit, occ.FracFull, occ.MeanUsed,
			occ.MaxUsed, occ.FracUsedGE32, occ.MeanWaiting, occ.MaxWaiting, bw,
			reads[t], occ.MeanOwner, occ.MaxOwner, top, topShare)
		for _, d := range occ.Dests {
			fmt.Fprintf(pw, "%s,%d,%d,%.6g,%d,%.6g\n", o.label, t,
				obsTileOf(d.Dst), d.Mean, d.Max, d.FracGE32)
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		pf.Close()
		return err
	}
	if err := pw.Flush(); err != nil {
		f.Close()
		pf.Close()
		return err
	}
	f.Close()
	return pf.Close()
}

// obsTileOf parses the mesh tile id from "GPU[t]...".
func obsTileOf(name string) int {
	i := strings.Index(name, "GPU[")
	if i < 0 {
		return -1
	}
	rest := name[i+4:]
	j := strings.IndexByte(rest, ']')
	if j < 0 {
		return -1
	}
	t := 0
	for _, ch := range rest[:j] {
		if ch < '0' || ch > '9' {
			return -1
		}
		t = t*10 + int(ch-'0')
	}
	return t
}
