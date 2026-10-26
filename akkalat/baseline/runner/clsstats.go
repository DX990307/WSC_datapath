package runner

// -l2-cls-stats (statistics only): remote reads by cause (cold / capacity /
// coherence / not admitted, classified at the owner) and L2 occupancy
// (valid ways, time-averaged and at kernel boundaries). See
// akita/mem/cache/writeback/clsstats.go. Does not change timing.

import (
	"flag"
	"fmt"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/timing/cp"
)

var clsStatsFlag = flag.Bool("l2-cls-stats", false,
	"Statistics only: classify network fetches of remote lines (cold, capacity, coherence, "+
		"not admitted) and report L2 occupancy. Does not change timing.")

type clsState struct {
	rc         *writeback.ReadClassifier
	l2s        [][]*writeback.Cache
	kV, kR, kT float64 // kernel-boundary snapshots (sum of blocks)
	kSnaps     int
	launched   []bool
}

var cls *clsState

func l2Slices(gpu *GPU) []*writeback.Cache {
	var out []*writeback.Cache
	for _, c := range gpu.L2Caches {
		if l2, ok := c.(*writeback.Cache); ok {
			out = append(out, l2)
		}
	}
	return out
}

func (r *Runner) configureClsStats() {
	if !*clsStatsFlag {
		return
	}
	cls = &clsState{rc: writeback.NewReadClassifier()}
	cls.launched = make([]bool, len(r.platform.GPUs))
	for i, gpu := range r.platform.GPUs {
		g := int(gpu.GPUID)
		l2s := l2Slices(gpu)
		cls.l2s = append(cls.l2s, l2s)
		for _, l2 := range l2s {
			l2.SetReadClassifier(cls.rc, g)
		}
		if gpu.RDMAEngine != nil {
			cls.rc.RegisterPort(gpu.RDMAEngine.ToOutside, g)
			gpu.RDMAEngine.ReadClass = cls.rc
			gpu.RDMAEngine.ReadClassGPM = g
		}
		c, ok := gpu.CommandProcessor.(*cp.CommandProcessor)
		if !ok {
			continue
		}
		idx := i
		snap := func(now sim.VTimeInSec) {
			// the occupancy at the end of the previous kernel, before the
			// boundary invalidation hooks run
			if !cls.launched[idx] {
				cls.launched[idx] = true
				return
			}
			for _, l2 := range cls.l2s[idx] {
				v, rr, t := l2.Occupancy()
				cls.kV += float64(v)
				cls.kR += float64(rr)
				cls.kT += float64(t)
			}
			cls.kSnaps++
		}
		c.KernelLaunchHooks = append([]func(sim.VTimeInSec){snap}, c.KernelLaunchHooks...)
	}
	fmt.Printf("[Config] L2 class statistics on (%d GPMs)\n", len(r.platform.GPUs))
}

func (r *Runner) reportClsStats() {
	if cls == nil {
		return
	}
	now := r.platform.Engine.CurrentTime()
	col := func(what string, v float64) { r.metricsCollector.Collect("Cls", what, v) }
	s := cls.rc.Stats
	col("cls_cold", float64(s.Cold))
	col("cls_capacity", float64(s.Capacity))
	col("cls_coherence", float64(s.Coherence))
	col("cls_notadmitted", float64(s.NotAdmitted))
	col("cls_resident", float64(s.Resident))
	col("cls_unknown_src", float64(s.Unknown))
	var tv, tr, tt, ev, er, et float64
	for _, l2s := range cls.l2s {
		for _, l2 := range l2s {
			v, rr, t := l2.OccupancyTimeAvg(now)
			tv += v
			tr += rr
			tt += t
			a, b, c := l2.Occupancy()
			ev += float64(a)
			er += float64(b)
			et += float64(c)
		}
	}
	if tt > 0 {
		col("occ_time_valid_frac", tv/tt)
		col("occ_time_remote_frac", tr/tt)
	}
	if cls.kT > 0 {
		col("occ_kernel_valid_frac", cls.kV/cls.kT)
		col("occ_kernel_remote_frac", cls.kR/cls.kT)
		col("occ_kernel_snaps", float64(cls.kSnaps))
	}
	if et > 0 {
		col("occ_end_valid_frac", ev/et)
		col("occ_end_remote_frac", er/et)
	}
}
