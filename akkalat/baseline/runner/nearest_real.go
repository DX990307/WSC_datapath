package runner

import (
	"flag"
	"fmt"
	"log"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/timing/cp"
	"github.com/sarchlab/mgpusim/v3/timing/pagemigration"
	"github.com/sarchlab/mgpusim/v3/timing/rdma"
)

var nearestModeFlag = flag.String("nearest", "",
	"isca27/nearest A (realistic read from the nearest holder, see rdma/nearest_real.go): "+
		"\"hint\" = A1 requester hint (the home's reply names the nearest holder of the page; "+
		"used when closer than the home by more than the expected miss detour), "+
		"\"fwd\" = A2 home forward (the home delegates the read to a holder). "+
		"Holders register with an install notice to the home.")
var nearestHintEntriesFlag = flag.Int("nearest-hint-entries", 1024,
	"isca27/nearest A1: hint table entries per GPM (FIFO; <= 0 unlimited).")
var nearestEvictNoticeFlag = flag.Bool("nearest-evict-notice", false,
	"isca27/nearest: holders also notify the home when they replace a line.")
var nearestFwdSlackFlag = flag.Int("nearest-fwd-slack", 0,
	"isca27/nearest A2: delegate when home->holder->reader is at most this many hops longer than home->reader.")

// nearestServedHook maps an engine index to its GPM for the R5 regret.
func (b *R9NanoPlatformBuilder) nearestServedHook(pm *pagemigration.Manager) func(int, uint64, int, int) {
	dev := make([]uint64, len(b.gpus))
	for i, gpu := range b.gpus {
		dev[i] = gpu.GPUID
	}
	return func(e int, line uint64, legs, hops int) {
		pm.NearestServed(dev[e], line, legs, hops)
	}
}

type nearestRealWrites struct {
	n  *rdma.NearestReal
	hi int
}

func (w nearestRealWrites) NoteWrite(now sim.VTimeInSec, addr, size uint64) {
	w.n.NoteWrite(now, w.hi, addr, size)
}

// wireNearestReal installs the realistic nearest-holder lookup.
func (b *R9NanoPlatformBuilder) wireNearestReal(drv *driver.Driver, pm *pagemigration.Manager) {
	mode := 0
	switch *nearestModeFlag {
	case "":
		return
	case "hint":
		mode = rdma.NearHint
	case "fwd":
		mode = rdma.NearFwd
	default:
		log.Panicf("-nearest %q: want hint or fwd", *nearestModeFlag)
	}
	if *nearestOracleFlag {
		log.Panic("-nearest and -nearest-oracle are exclusive")
	}
	if !b.remoteDataPath.Enabled || b.remoteDataPath.DisableRequesterL2 {
		log.Panic("-nearest needs the remote data path with requester-L2 replicas (M3)")
	}
	cfg := rdma.NearestRealConfig{
		Mode:         mode,
		HintEntries:  *nearestHintEntriesFlag,
		EvictNotice:  *nearestEvictNoticeFlag,
		FwdSlackHops: *nearestFwdSlackFlag,
		// the R3 zero-load model (adapt.go)
		BaseNS: 65.7,
		HopNS:  2 * (float64(*switchLatencyFlag) + 4.1),
	}
	engines := make([]*rdma.Comp, len(b.gpus))
	for i, gpu := range b.gpus {
		engines[i] = gpu.RDMAEngine
	}
	n := rdma.NewNearestReal(engines, cfg)
	for i, gpu := range b.gpus {
		e := gpu.RDMAEngine
		for _, t := range gpu.L2Caches {
			c, ok := t.(*writeback.Cache)
			if !ok {
				log.Panic("-nearest: unexpected L2 type")
			}
			c.SetReplicaObserver(e)
			c.AddHomeWriteObserver(nearestRealWrites{n, i})
		}
		p, ok := gpu.CommandProcessor.(*cp.CommandProcessor)
		if !ok {
			log.Panic("-nearest: command processor type")
		}
		p.KernelLaunchHooks = append(p.KernelLaunchHooks, n.KernelBoundary)
	}
	drv.AddPhysicalWriteHook(func(now sim.VTimeInSec, addr, size uint64) {
		n.NoteWrite(now, -1, addr, size)
	})
	if pm != nil && *pmAdaptR3ServedFlag {
		n.Served = b.nearestServedHook(pm)
	}
	fmt.Printf("[Config] isca27/nearest: mode=%s hint-entries=%d evict-notice=%v fwd-slack=%d base=%gns hop=%gns r3-served=%v\n",
		*nearestModeFlag, cfg.HintEntries, cfg.EvictNotice, cfg.FwdSlackHops, cfg.BaseNS, cfg.HopNS,
		n.Served != nil)
}

// reportNearestReal writes the lookup's counters.
func (r *Runner) reportNearestReal() {
	if len(r.platform.GPUs) == 0 {
		return
	}
	n := r.platform.GPUs[0].RDMAEngine.NearestRealOf()
	if n == nil {
		return
	}
	s := n.Stats
	col := func(what string, v float64) { r.metricsCollector.Collect("NearestReal", what, v) }
	col("eligible_reads", float64(s.EligibleReads))
	col("hints_given", float64(s.HintsGiven))
	col("hints_none", float64(s.HintsNone))
	col("hint_uses", float64(s.HintUses))
	col("hint_skips", float64(s.HintSkips))
	col("hint_absent", float64(s.HintAbsent))
	col("holder_hits", float64(s.HolderHits))
	col("holder_misses", float64(s.HolderMisses))
	col("delegated", float64(s.Delegated))
	col("install_notices", float64(s.InstallNotices))
	col("evict_notices", float64(s.EvictNotices))
	col("stale_notices", float64(s.StaleNotices))
	col("hint_inval_msgs", float64(s.HintInvalMsgs))
	col("hint_table_evicts", float64(s.HintTableEvicts))
	col("self_write_drops", float64(s.SelfWriteDrops))
	col("hops_home", s.HopsHome)
	col("path_hops", s.PathHops)
	col("home_hops2", s.HomeHops2)
	col("kernels", float64(s.Kernels))
	for g := range s.ServedByEngine {
		if s.ServedByEngine[g]+s.HomeByEngine[g] > 0 {
			name := fmt.Sprintf("GPU%d", g)
			r.metricsCollector.Collect(name, "nr_served", float64(s.ServedByEngine[g]))
			r.metricsCollector.Collect(name, "nr_home", float64(s.HomeByEngine[g]))
		}
	}
}
