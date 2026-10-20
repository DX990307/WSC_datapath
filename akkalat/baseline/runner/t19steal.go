package runner

// T19: cuckoo-filter-guided nearby WG stealing (mechanism, off by default).
// -t19-steal turns on stealing between the GPMs of unified kernels
// (distance-only victim choice); -t19-filter adds the filter guidance. See
// mgpusim/wgsteal for the protocol. With both flags off nothing here runs:
// no port, no hook, no coordinator.

import (
	"flag"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/tracing"
	"github.com/sarchlab/mgpusim/v3/timing/cp"
	"github.com/sarchlab/mgpusim/v3/timing/cu"
	"github.com/sarchlab/mgpusim/v3/wgsteal"
)

var t19Defaults = wgsteal.DefaultConfig()

var (
	t19StealFlag = flag.Bool("t19-steal", false,
		"Mechanism (T19): GPMs steal not-yet-dispatched WGs of unified kernels "+
			"from nearby GPMs (distance-only victim choice). Off by default.")
	t19FilterFlag = flag.Bool("t19-filter", false,
		"Mechanism (T19): guide the victim/chunk choice with cuckoo filters "+
			"(recent pages per GPM, WG-group footprints). Needs -t19-steal.")
	t19ChunkFlag = flag.Int("t19-chunk-wgs", t19Defaults.ChunkWGs,
		"T19: WGs per steal (taken from the tail of the victim's queue).")
	t19RadiusFlag = flag.Int("t19-radius", t19Defaults.RadiusHops,
		"T19: hops within which GPMs advertise their queued-WG counts and are near victims.")
	t19FallbackFlag = flag.Bool("t19-fallback", t19Defaults.Fallback,
		"T19: steal from GPMs beyond -t19-radius when no near GPM has work.")
	t19MinVictimFlag = flag.Int("t19-min-victim-wgs", 0,
		"T19: a victim must hold at least this many queued WGs (0 = 2 x chunk).")
	t19LowWatermarkFlag = flag.Int("t19-low-watermark", t19Defaults.LowWatermark,
		"T19: start stealing when the own queue holds <= this many WGs.")
	t19AdvPeriodFlag = flag.Float64("t19-adv-period-ns", t19Defaults.AdvPeriodNS,
		"T19: period of the queued-WG-count advertisement to near GPMs (ns).")
	t19FarAdvPeriodFlag = flag.Float64("t19-far-adv-period-ns", t19Defaults.FarAdvPeriodNS,
		"T19: period of the advertisement to far GPMs (ns).")
	t19AdvHopFlag = flag.Float64("t19-adv-hop-ns", t19Defaults.AdvHopNS,
		"T19: one-way per-hop latency of an advertisement (ns).")
	t19AdvFixedFlag = flag.Float64("t19-adv-fixed-ns", t19Defaults.AdvFixedNS,
		"T19: fixed latency of an advertisement (ns).")
	t19VictimCyclesFlag = flag.Int("t19-victim-cycles", t19Defaults.VictimCycles,
		"T19: CP cycles a victim takes to serve a steal request.")
	t19SplitFlag = flag.String("t19-initial-split", t19Defaults.InitialSplit,
		"T19: initial WG split: static (MGPUSim's) or balanced (floor/ceil N/GPMs).")
	t19RPFBucketsFlag = flag.Uint("t19-rpf-log2-buckets", t19Defaults.RPFLog2Buckets,
		"T19 filter: log2 buckets of the per-GPM recent-page cuckoo filter.")
	t19SlotsFlag = flag.Int("t19-filter-slots", t19Defaults.FilterSlots,
		"T19 filter: entries per bucket.")
	t19FPBitsFlag = flag.Uint("t19-fp-bits", t19Defaults.FPBits,
		"T19 filter: fingerprint bits.")
	t19RPFWindowFlag = flag.Int("t19-rpf-window", t19Defaults.RPFWindow,
		"T19 filter: distinct recent pages kept per GPM (older ones are deleted).")
	t19GroupBucketsFlag = flag.Uint("t19-group-log2-buckets", t19Defaults.GroupLog2Buckets,
		"T19 filter: log2 buckets of a WG-group footprint filter.")
	t19GroupWGsFlag = flag.Int("t19-group-wgs", 0,
		"T19 filter: WGs per group without a benchmark hint (0 = chunk).")
	t19GroupTableFlag = flag.Int("t19-group-table", t19Defaults.GroupTablePerGPM,
		"T19 filter: group footprint filters per home GPM (LRU).")
	t19CandidatesFlag = flag.Int("t19-candidates", t19Defaults.CandidatesPerVictim,
		"T19 filter: chunks considered per victim (1 = tail chunk only).")
	t19ReplaceBelowFlag = flag.Float64("t19-replace-below", t19Defaults.ReplaceBelow,
		"T19 filter: replace a group's footprint history when fewer of its pages hit.")
	t19ProbeWidthFlag = flag.Int("t19-probe-width", t19Defaults.ProbeWidth,
		"T19 filter: footprint entries probed per CP cycle when choosing a victim.")
	t19FileFlag = flag.String("t19-file", "",
		"T19: per kernel x GPM CSV (default <metric-file-name>_t19.csv).")
)

func t19Config() wgsteal.Config {
	c := wgsteal.DefaultConfig()
	c.ChunkWGs = *t19ChunkFlag
	c.RadiusHops = *t19RadiusFlag
	c.Fallback = *t19FallbackFlag
	c.MinVictimWGs = *t19MinVictimFlag
	c.LowWatermark = *t19LowWatermarkFlag
	c.AdvPeriodNS = *t19AdvPeriodFlag
	c.FarAdvPeriodNS = *t19FarAdvPeriodFlag
	c.AdvHopNS = *t19AdvHopFlag
	c.AdvFixedNS = *t19AdvFixedFlag
	c.VictimCycles = *t19VictimCyclesFlag
	c.InitialSplit = *t19SplitFlag
	c.Filter = *t19FilterFlag
	c.RPFLog2Buckets = *t19RPFBucketsFlag
	c.FilterSlots = *t19SlotsFlag
	c.FPBits = *t19FPBitsFlag
	c.RPFWindow = *t19RPFWindowFlag
	c.GroupLog2Buckets = *t19GroupBucketsFlag
	c.GroupWGs = *t19GroupWGsFlag
	c.GroupTablePerGPM = *t19GroupTableFlag
	c.CandidatesPerVictim = *t19CandidatesFlag
	c.ReplaceBelow = *t19ReplaceBelowFlag
	c.ProbeWidth = *t19ProbeWidthFlag
	return c
}

// t19Enabled is read by the GPM builder (steal port on the mesh).
func t19Enabled() bool {
	if *t19FilterFlag && !*t19StealFlag {
		panic("-t19-filter needs -t19-steal")
	}
	return *t19StealFlag
}

// setupWGSteal creates the coordinator after the platform is built.
func (r *Runner) setupWGSteal() {
	if !t19Enabled() {
		return
	}
	if !r.Timing {
		panic("-t19-steal needs -timing")
	}
	gpms := make([]wgsteal.GPMInfo, len(r.platform.GPUs))
	cps := make([]*cp.CommandProcessor, len(r.platform.GPUs))
	for i, gpu := range r.platform.GPUs {
		if int(gpu.GPUID) != i+1 {
			panic(fmt.Sprintf("t19: GPU %d has driver ID %d", i, gpu.GPUID))
		}
		m := p12TileRe.FindStringSubmatch(gpu.Domain.Name())
		if m == nil {
			panic("t19: cannot parse the tile of " + gpu.Domain.Name())
		}
		t, _ := strconv.Atoi(m[1])
		c, ok := gpu.CommandProcessor.(*cp.CommandProcessor)
		if !ok || c.StealPort() == nil {
			panic("t19: command processor without a steal port")
		}
		cps[i] = c
		gpms[i] = wgsteal.GPMInfo{
			X: t % gpmTraceMeshWidth, Y: t / gpmTraceMeshWidth,
			Port: c.StealPort(), NumCU: len(gpu.CUs),
		}
	}
	coord, err := wgsteal.NewCoordinator(t19Config(), gpms)
	if err != nil {
		panic(err)
	}
	for i, c := range cps {
		c.EnableWGSteal(coord, i)
	}
	r.platform.Driver.SetWGSteal(coord)
	r.wgSteal = coord
	if coord.Config().Filter {
		r.addT19ReadHooks(coord)
	}
}

// addT19ReadHooks feeds every vector load to the coordinator (recent-page
// filter of the GPM, footprint of the WG's group), observed at the CU when
// the load leaves the vector memory unit, like P12's footprint tracer.
func (r *Runner) addT19ReadHooks(coord *wgsteal.Coordinator) {
	engine := r.platform.Engine
	for i, gpu := range r.platform.GPUs {
		gpm := i
		for _, c := range gpu.CUs {
			cuObj, _ := c.(*cu.ComputeUnit)
			if cuObj == nil {
				continue
			}
			tracing.CollectTrace(c.(tracing.NamedHookable), &gpmTraceHook{
				engine: engine, gpm: gpm,
				start: func(h *gpmTraceHook, task tracing.Task) {
					if task.Kind != "req_out" {
						return
					}
					read, ok := task.Detail.(*mem.ReadReq)
					if !ok {
						return
					}
					inflight := cuObj.InFlightVectorMemAccess
					for j := len(inflight) - 1; j >= 0; j-- {
						if inflight[j].Read != read {
							continue
						}
						wf := inflight[j].Wavefront
						if wf != nil && wf.WG != nil && wf.WG.WorkGroup != nil {
							coord.OnRead(h.gpm, wf.WG.WorkGroup, read.Address)
						}
						return
					}
				},
			})
		}
	}
}

// reportWGSteal writes the T19 statistics (only when enabled).
func (r *Runner) reportWGSteal() {
	c := r.wgSteal
	if c == nil {
		return
	}
	t := c.Totals()
	where := "T19"
	put := func(name string, v float64) { r.metricsCollector.Collect(where, name, v) }
	cfg := c.Config()
	put("t19_filter_enabled", b2f(cfg.Filter))
	put("t19_chunk_wgs", float64(cfg.ChunkWGs))
	put("t19_radius_hops", float64(cfg.RadiusHops))
	put("t19_launches", float64(t.Launches))
	put("t19_steal_requests", float64(t.StealReqs))
	put("t19_steals_ok", float64(t.StealsOK))
	put("t19_steals_nack", float64(t.StealsNack))
	put("t19_wgs_stolen", float64(t.WGsStolen))
	put("t19_wgs_completed", float64(t.WGsCompleted))
	put("t19_near_steals", float64(t.NearSteals))
	put("t19_far_steals", float64(t.FarSteals))
	if t.StealsOK > 0 {
		put("t19_steal_latency_avg_ns", t.LatSumNS/float64(t.StealsOK))
		put("t19_steal_hops_avg", float64(t.HopsSum)/float64(t.StealsOK))
	}
	put("t19_steal_latency_max_ns", t.LatMaxNS)
	put("t19_adv_near_msgs", float64(t.AdvNearMsgs))
	put("t19_adv_far_msgs", float64(t.AdvFarMsgs))
	if cfg.Filter {
		lookups, hits, fp, ins, ev, fails := c.RecentPageStats()
		put("t19_rpf_lookups", float64(lookups))
		put("t19_rpf_hits", float64(hits))
		put("t19_rpf_false_pos", float64(fp))
		put("t19_rpf_inserts", float64(ins))
		put("t19_rpf_evictions", float64(ev))
		put("t19_rpf_insert_fails", float64(fails))
		put("t19_probe_entries", float64(t.ProbeEntries))
		put("t19_filter_decided_steals", float64(t.FilterDecided))
		put("t19_affinity_decided_steals", float64(t.AffinityDecided))
		if t.ScoreCount > 0 {
			put("t19_overlap_score_avg", t.ScoreSum/float64(t.ScoreCount))
			put("t19_overlap_exact_avg", t.ExactSum/float64(t.ScoreCount))
		}
		put("t19_groups_recorded", float64(t.GroupsRecorded))
		put("t19_groups_kept", float64(t.GroupsKept))
		put("t19_groups_replaced", float64(t.GroupsReplaced))
		put("t19_groups_evicted", float64(t.GroupsEvicted))
	}

	// Per-GPM finish spread of the finished launches: for each launch,
	// (latest - launch) / mean over GPMs that ran a WG; reported as the
	// duration-weighted mean and the maximum.
	rows := c.Rows()
	byLaunch := map[int][]wgsteal.LaunchRow{}
	for _, x := range rows {
		byLaunch[x.Launch] = append(byLaunch[x.Launch], x)
	}
	ids := make([]int, 0, len(byLaunch))
	for id := range byLaunch {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var wSum, wRatio, maxRatio float64
	for _, id := range ids {
		var sum, mx float64
		n := 0
		for _, x := range byLaunch[id] {
			if x.Executed == 0 {
				continue
			}
			d := x.FinishNS - x.StartNS
			sum += d
			mx = math.Max(mx, d)
			n++
		}
		if n == 0 || sum == 0 {
			continue
		}
		ratio := mx / (sum / float64(n))
		wSum += mx
		wRatio += mx * ratio
		maxRatio = math.Max(maxRatio, ratio)
	}
	if wSum > 0 {
		put("t19_finish_max_over_mean_weighted", wRatio/wSum)
		put("t19_finish_max_over_mean_max", maxRatio)
	}

	path := *t19FileFlag
	if path == "" {
		path = *filenameFlag + "_t19.csv"
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[T19] cannot create %s: %v\n", path, err)
		return
	}
	defer f.Close()
	if err := c.WriteCSV(f); err != nil {
		fmt.Fprintf(os.Stderr, "[T19] write %s: %v\n", path, err)
	}
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
