package runner

// ISCA'27 R4: L2 residency of remote data instead of page migration
// (akita/mem/cache/writeback/l2res.go, mgpusim/timing/pagemigration/l2res.go).
// Off by default.

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/timing/pagemigration"
)

var l2resCostReplFlag = flag.Bool("l2res-cost-repl", false,
	"R4: cost-aware L2 replacement (re-fetch latency x SRRIP re-use weight); "+
		"remote fills lose the M3 protection rules.")
var l2resCostModeFlag = flag.String("l2res-cost-mode", "dist",
	"R4: re-fetch cost: dist (hops to the home), blind (local / remote at the "+
		"mesh-average distance), rrip (all lines equal: plain SRRIP).")
var l2resLocalNSFlag = flag.Float64("l2res-local-ns", 130,
	"R4: re-fetch latency of a local line (ns); a remote line adds the round trip.")
var l2resHoldersFlag = flag.Int("l2res-holders", 0,
	"R4: owner-side holder table entries (pages) per GPM; 0 = off, -1 = unlimited. "+
		"Writes invalidate holders; tracked lines survive kernel boundaries.")

var l2resPullSlotsFlag = flag.Int("l2res-pull-slots", 4,
	"R4: readers with a line footprint per tracked page (action l2pull).")

var l2resDecideNSFlag = flag.Int("l2res-decide-ns", 500,
	"R4: hardware decision delay of the pull actions (ns).")

var l2resHolderModeFlag = flag.String("l2res-holder-mode", "drop",
	"R4: what a write does to the holders' lines of the page: drop (at once) or "+
		"mark (untracked: readable for the rest of the kernel like M3, A1 at the boundary).")

var l2resReuseFlag = flag.String("l2res-reuse", "measured",
	"R4: re-use probability of a line by RRPV: measured (per class local/remote, "+
		"online hit-vs-eviction counts) or fixed (1, 1/2, 1/4, 1/8).")

var l2resLocalMeasureFlag = flag.Bool("l2res-local-measure", true,
	"R4: the local re-fetch latency is the measured mean of the slice's demand read misses "+
		"(starting at -l2res-local-ns).")

var l2resFTProtectFlag = flag.String("l2res-ft-protect", "off",
	"R4: under cost replacement a first-touch remote fill may replace only invalid ways "+
		"and remote lines (no valid local line): off, on, or duel (set dueling per slice).")

var l2resNearestStatFlag = flag.Bool("l2res-nearest-stat", false,
	"R4 direction 3 offline bound (read-only): for every network fetch, the hops to the "+
		"nearest GPM that fetched the line earlier in the kernel (ideal) or holds an M3 "+
		"replica (real), against the hops to the home.")

// wireNearestStat installs the read-only nearest-copy statistics.
func (b *R9NanoPlatformBuilder) wireNearestStat(m *pagemigration.Manager,
	drv *driver.Driver) {
	if !*l2resNearestStatFlag {
		return
	}
	if m == nil {
		log.Panic("-l2res-nearest-stat needs -page-migration")
	}
	m.EnableNearestStats()
	l2of := map[uint64]func(uint64) *writeback.Cache{}
	for _, gpu := range b.gpus {
		l2of[gpu.GPUID] = gpu.pm.l2Of
	}
	holds := func(line uint64) func(g uint64) bool {
		return func(g uint64) bool {
			f, ok := l2of[g]
			return ok && f(line).HoldsRemoteLine(line)
		}
	}
	w := nearestWriteObs{m}
	for _, gpu := range b.gpus {
		dev := gpu.GPUID
		for _, l2 := range gpu.pm.l2s {
			l2.SetNetFetchHook(func(now, req sim.VTimeInSec, line uint64) {
				m.NearestNote(dev, line, holds(line))
			})
			l2.AddHomeWriteObserver(w)
		}
	}
	drv.AddPhysicalWriteHook(m.NearestWrite)
}

type nearestWriteObs struct{ m *pagemigration.Manager }

func (o nearestWriteObs) NoteWrite(now sim.VTimeInSec, addr, size uint64) {
	o.m.NearestWrite(now, addr, size)
}

var l2resCongestFlag = flag.Bool("l2res-congest", false,
	"R4 direction 4: the re-fetch cost of a remote line adds the measured queueing to its "+
		"home (per GPM and home: EMA of fill arrival - read open, minus the zero-load round trip).")

func mean(v []float64) float64 {
	s, n := 0.0, 0
	for _, x := range v {
		if x > 0 {
			s += x
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return s / float64(n)
}

// wireL2Res installs the R4 mechanisms after the page-migration manager.
func (b *R9NanoPlatformBuilder) wireL2Res(m *pagemigration.Manager,
	drv *driver.Driver, retain *writeback.RemoteRetainTracker) *writeback.HolderTable {
	if !*l2resCostReplFlag && *l2resHoldersFlag == 0 {
		return nil
	}
	if m == nil {
		log.Panic("-l2res-* needs -page-migration (the manager's latency model)")
	}
	if *l2resCostReplFlag {
		mode := *l2resCostModeFlag
		if mode != "dist" && mode != "blind" && mode != "rrip" {
			log.Panicf("-l2res-cost-mode %q", mode)
		}
		local := *l2resLocalNSFlag
		congest := *l2resCongestFlag
		for _, gpu := range b.gpus {
			dev := gpu.GPUID
			q := make([]float64, len(b.gpus)+2) // measured queueing per home (ns), direction 4
			rtt := func(addr uint64) float64 {
				if mode == "rrip" {
					return 0
				}
				h := addr >> 33
				extra := 0.0
				if congest && int(h) < len(q) {
					extra = q[h]
					if mode == "blind" {
						extra = mean(q)
					}
				}
				return m.L2ResRTT(dev, h, mode == "blind") + extra
			}
			if congest {
				note := func(now, req sim.VTimeInSec, line uint64) {
					h := line >> 33
					if int(h) >= len(q) || h == dev {
						return
					}
					meas := float64(now-req) * 1e9
					w := meas - m.L2ResRTT(dev, h, false)
					if w < 0 {
						w = 0
					}
					q[h] += (w - q[h]) / 64
				}
				for _, l2 := range gpu.pm.l2s {
					l2.SetNetFetchHook(note)
				}
			}
			class := func(addr uint64) int {
				if mode == "rrip" || addr>>33 == dev {
					return 0
				}
				return 1
			}
			for _, l2 := range gpu.pm.l2s {
				l2.EnableCostReplacement(rtt, local, *l2resLocalMeasureFlag && mode != "rrip", class,
					*l2resReuseFlag == "fixed")
				l2.FirstTouchProtect = *l2resFTProtectFlag == "on"
				l2.FirstTouchDuel = *l2resFTProtectFlag == "duel"
			}
		}
		fmt.Printf("[Config] R4 cost-aware L2 replacement (%s, local %.0f ns, re-use %s)\n",
			mode, local, *l2resReuseFlag)
		b.wireLease(m)
	} else if *leaseFlag {
		log.Panic("-lease needs -l2res-cost-repl")
	}
	if *l2resHoldersFlag == 0 {
		return nil
	}
	if retain == nil {
		log.Panic("-l2res-holders needs -a1-retain (untracked lines keep the A1 rule)")
	}
	t := writeback.NewHolderTable(writeback.HolderTableConfig{
		Log2PageSize: 12, Log2OwnerSize: 33, CapPerOwner: *l2resHoldersFlag,
		MarkOnWrite: *l2resHolderModeFlag == "mark",
	})
	if m := *l2resHolderModeFlag; m != "drop" && m != "mark" {
		log.Panicf("-l2res-holder-mode %q", m)
	}
	t.Deliver = func(now sim.VTimeInSec, owner uint64, holder int, apply func()) {
		m.SendHolderMsg(now, owner, holder, apply)
	}
	obs := homeWriteFanout{retain, t}
	for _, gpu := range b.gpus {
		for _, l2 := range gpu.pm.l2s {
			l2.SetHolderTable(t, int(gpu.GPUID))
			l2.SetHomeWriteObserver(obs)
		}
	}
	drv.AddPhysicalWriteHook(t.NoteWrite)
	fmt.Printf("[Config] R4 holder table: %d pages per GPM\n", *l2resHoldersFlag)
	return t
}

type homeWriteFanout []writeback.HomeWriteObserver

func (f homeWriteFanout) NoteWrite(now sim.VTimeInSec, addr, size uint64) {
	for _, o := range f {
		o.NoteWrite(now, addr, size)
	}
}

// reportL2ResStats writes the R4 counters (summed over all L2 slices).
func (r *Runner) reportL2ResStats() {
	r.reportNearestStats()
	r.reportNearestOracle()
	if !*l2resCostReplFlag && *l2resHoldersFlag == 0 && !strings.Contains(*pmAdaptActionsFlag, "l2pull") {
		return
	}
	var s writeback.L2ResStats
	for _, gpu := range r.platform.GPUs {
		if gpu.pm == nil {
			continue
		}
		for _, l2 := range gpu.pm.l2s {
			x := l2.GetL2ResStats()
			s.CostAdmitDrops += x.CostAdmitDrops
			s.FillEvictLocal += x.FillEvictLocal
			s.FillEvictRemote += x.FillEvictRemote
			s.FillIntoInvalid += x.FillIntoInvalid
			s.TrackedInstalls += x.TrackedInstalls
			s.UntrackedInstalls += x.UntrackedInstalls
			s.PageInvals += x.PageInvals
			s.LinesInvalByWrites += x.LinesInvalByWrites
			s.LinesMarkedByWrites += x.LinesMarkedByWrites
			s.Downgrades += x.Downgrades
			s.BoundaryTrackedKept += x.BoundaryTrackedKept
			s.BoundaryTrackedDropped += x.BoundaryTrackedDropped
			s.BoundaryUntrackedKept += x.BoundaryUntrackedKept
			s.BoundaryUntrackedDropped += x.BoundaryUntrackedDropped
			s.PulledInstalled += x.PulledInstalled
			s.PulledDropped += x.PulledDropped
			s.PulledUsed += x.PulledUsed
			s.VictimScoreSum += x.VictimScoreSum
			s.VictimCount += x.VictimCount
			s.LocalMissSamples += x.LocalMissSamples
			s.LocalMissSumNS += x.LocalMissSumNS
		}
	}
	col := func(what string, v float64) { r.metricsCollector.Collect("L2Res", what, v) }
	col("l2r_cost_admit_drops", float64(s.CostAdmitDrops))
	col("l2r_fill_evict_local", float64(s.FillEvictLocal))
	col("l2r_fill_evict_remote", float64(s.FillEvictRemote))
	col("l2r_fill_into_invalid", float64(s.FillIntoInvalid))
	col("l2r_tracked_installs", float64(s.TrackedInstalls))
	col("l2r_untracked_installs", float64(s.UntrackedInstalls))
	col("l2r_page_invals", float64(s.PageInvals))
	r.reportLeaseStats()
	col("l2r_lines_inval_by_writes", float64(s.LinesInvalByWrites))
	col("l2r_lines_marked_by_writes", float64(s.LinesMarkedByWrites))
	col("l2r_downgrades", float64(s.Downgrades))
	col("l2r_boundary_tracked_kept", float64(s.BoundaryTrackedKept))
	col("l2r_boundary_tracked_dropped", float64(s.BoundaryTrackedDropped))
	col("l2r_boundary_untracked_kept", float64(s.BoundaryUntrackedKept))
	col("l2r_boundary_untracked_dropped", float64(s.BoundaryUntrackedDropped))
	if t := r.platform.Holders; t != nil {
		h := t.Stats()
		col("l2r_ht_registers", float64(h.Registers))
		col("l2r_ht_allocs", float64(h.Allocs))
		col("l2r_ht_replacements", float64(h.Replacements))
		col("l2r_ht_inval_msgs", float64(h.InvalMsgs))
		col("l2r_ht_downgrade_msgs", float64(h.DowngradeMsgs))
		col("l2r_ht_write_notes", float64(h.WriteNotes))
		col("l2r_ht_writes_with_holders", float64(h.WritesWithHolders))
		col("l2r_ht_peak_entries", float64(h.PeakEntries))
	}
	col("l2r_pulled_installed", float64(s.PulledInstalled))
	col("l2r_pulled_dropped", float64(s.PulledDropped))
	col("l2r_pulled_used", float64(s.PulledUsed))
	if s.LocalMissSamples > 0 {
		col("l2r_local_miss_mean_ns", s.LocalMissSumNS/float64(s.LocalMissSamples))
	}
	if s.VictimCount > 0 {
		col("l2r_victim_score_mean", s.VictimScoreSum/float64(s.VictimCount))
	}
	if m := r.platform.PageMigration; m != nil {
		col("l2r_holder_msgs_net", float64(m.L2Res.HolderMsgs))
		col("l2r_pulls", float64(m.L2Res.Pulls))
		col("l2r_pull_lines", float64(m.L2Res.PullLines))
		col("l2r_pull_installed", float64(m.L2Res.PullInstalled))
		col("l2r_pull_aborts", float64(m.L2Res.PullAborts))
		col("l2r_slot_replacements", float64(m.L2Res.SlotReplacements))
		col("l2r_prefetches", float64(m.L2Res.Prefetches))
		col("l2r_prefetch_skips", float64(m.L2Res.PrefetchSkips))
	}
}

func (r *Runner) reportNearestStats() {
	m := r.platform.PageMigration
	if m == nil || m.NearestStats() == nil {
		return
	}
	s := m.NearestStats()
	col := func(what string, v float64) { r.metricsCollector.Collect("L2Res", what, v) }
	col("nc_fetches", float64(s.Fetches))
	col("nc_hops_home", s.HopsHome)
	col("nc_hops_ideal", s.HopsIdeal)
	col("nc_hops_real", s.HopsReal)
	col("nc_rtt_home", s.RTTHome)
	col("nc_rtt_ideal", s.RTTIdeal)
	col("nc_rtt_real", s.RTTReal)
	col("nc_closer_ideal", float64(s.CloserIdeal))
	col("nc_closer_real", float64(s.CloserReal))
	for k := 0; k < 4; k++ {
		col(fmt.Sprintf("nc_cat%d_fetches", k), float64(s.Cat[k]))
		col(fmt.Sprintf("nc_cat%d_hops_home", k), s.CatHome[k])
		col(fmt.Sprintf("nc_cat%d_hops_ideal", k), s.CatIdeal[k])
		col(fmt.Sprintf("nc_cat%d_hops_real", k), s.CatReal[k])
	}
	maxOf := func(v []uint64) (mx uint64) {
		for _, x := range v {
			if x > mx {
				mx = x
			}
		}
		return
	}
	col("nc_owner_max_home", float64(maxOf(s.OwnerHome)))
	col("nc_owner_max_ideal", float64(maxOf(s.OwnerIdeal)))
	col("nc_owner_max_real", float64(maxOf(s.OwnerReal)))
	var ch, ci, cr float64
	for _, g := range m.NearestCorners() {
		ch += float64(s.OwnerHome[g])
		ci += float64(s.OwnerIdeal[g])
		cr += float64(s.OwnerReal[g])
	}
	col("nc_corner_home", ch)
	col("nc_corner_ideal", ci)
	col("nc_corner_real", cr)
	for g := range s.OwnerHome {
		if s.OwnerHome[g]+s.OwnerIdeal[g]+s.OwnerReal[g] > 0 {
			r.metricsCollector.Collect(fmt.Sprintf("GPU%d", g), "nc_served_home", float64(s.OwnerHome[g]))
			r.metricsCollector.Collect(fmt.Sprintf("GPU%d", g), "nc_served_ideal", float64(s.OwnerIdeal[g]))
			r.metricsCollector.Collect(fmt.Sprintf("GPU%d", g), "nc_served_real", float64(s.OwnerReal[g]))
		}
	}
}
