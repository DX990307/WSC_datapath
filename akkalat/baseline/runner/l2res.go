package runner

// ISCA'27 R4: L2 residency of remote data instead of page migration
// (akita/mem/cache/writeback/l2res.go, mgpusim/timing/pagemigration/l2res.go).
// Off by default.

import (
	"flag"
	"fmt"
	"log"

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
		for _, gpu := range b.gpus {
			dev := gpu.GPUID
			cost := func(addr uint64) float64 {
				if mode == "rrip" {
					return 1
				}
				return local + m.L2ResRTT(dev, addr>>33, mode == "blind")
			}
			for _, l2 := range gpu.pm.l2s {
				l2.EnableCostReplacement(cost)
			}
		}
		fmt.Printf("[Config] R4 cost-aware L2 replacement (%s, local %.0f ns)\n", mode, local)
	}
	if *l2resHoldersFlag == 0 {
		return nil
	}
	if retain == nil {
		log.Panic("-l2res-holders needs -a1-retain (untracked lines keep the A1 rule)")
	}
	t := writeback.NewHolderTable(writeback.HolderTableConfig{
		Log2PageSize: 12, Log2OwnerSize: 33, CapPerOwner: *l2resHoldersFlag,
	})
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
	if !*l2resCostReplFlag && *l2resHoldersFlag == 0 {
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
			s.Downgrades += x.Downgrades
			s.BoundaryTrackedKept += x.BoundaryTrackedKept
			s.BoundaryTrackedDropped += x.BoundaryTrackedDropped
			s.BoundaryUntrackedKept += x.BoundaryUntrackedKept
			s.BoundaryUntrackedDropped += x.BoundaryUntrackedDropped
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
	col("l2r_lines_inval_by_writes", float64(s.LinesInvalByWrites))
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
	if m := r.platform.PageMigration; m != nil {
		col("l2r_holder_msgs_net", float64(m.L2Res.HolderMsgs))
	}
}
