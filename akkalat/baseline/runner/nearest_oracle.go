package runner

import (
	"flag"
	"fmt"
	"log"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/timing/cp"
	"github.com/sarchlab/mgpusim/v3/timing/rdma"
)

var nearestOracleFlag = flag.Bool("nearest-oracle", false,
	"r5-distance A (timing upper bound): a single-line remote read goes to the GPM nearest "+
		"to the requester whose L2 holds the line as an M3 replica fetched in this kernel after "+
		"the last write (if closer than the home); real mesh and queueing, free holder lookup; "+
		"the holder reads its L2 (lookup only) and forwards to the home on a miss.")

// wireNearestOracle installs the nearest-copy oracle on every RDMA engine.
func (b *R9NanoPlatformBuilder) wireNearestOracle(drv *driver.Driver) {
	if !*nearestOracleFlag {
		return
	}
	if !b.remoteDataPath.Enabled || b.remoteDataPath.DisableRequesterL2 {
		log.Panic("-nearest-oracle needs the remote data path with requester-L2 replicas (M3)")
	}
	engines := make([]*rdma.Comp, len(b.gpus))
	l2s := make(map[*rdma.Comp][]*writeback.Cache, len(b.gpus))
	for i, gpu := range b.gpus {
		engines[i] = gpu.RDMAEngine
		for _, t := range gpu.L2Caches {
			c, ok := t.(*writeback.Cache)
			if !ok {
				log.Panic("-nearest-oracle: unexpected L2 type")
			}
			l2s[gpu.RDMAEngine] = append(l2s[gpu.RDMAEngine], c)
		}
	}
	o := rdma.NewNearestOracle(engines)
	byTop := make(map[sim.Port]*writeback.Cache)
	for _, cs := range l2s {
		for _, c := range cs {
			byTop[c.GetPortByName("Top")] = c
		}
	}
	o.Holds = func(e *rdma.Comp, line uint64) bool {
		c := byTop[e.RemoteCacheSlice(line)]
		return c != nil && c.HoldsRemoteLine(line)
	}
	w := nearestOracleWrites{o}
	for _, gpu := range b.gpus {
		e := gpu.RDMAEngine
		for _, c := range l2s[e] {
			c.SetNetFetchHook(func(now, req sim.VTimeInSec, line uint64) {
				o.NoteFill(e, req, line)
			})
			c.AddHomeWriteObserver(w)
		}
		p, ok := gpu.CommandProcessor.(*cp.CommandProcessor)
		if !ok {
			log.Panic("-nearest-oracle: command processor type")
		}
		p.KernelLaunchHooks = append(p.KernelLaunchHooks, o.KernelBoundary)
	}
	drv.AddPhysicalWriteHook(o.NoteWrite)
}

type nearestOracleWrites struct{ o *rdma.NearestOracle }

func (w nearestOracleWrites) NoteWrite(now sim.VTimeInSec, addr, size uint64) {
	w.o.NoteWrite(now, addr, size)
}

// reportNearestOracle writes the oracle's counters.
func (r *Runner) reportNearestOracle() {
	if len(r.platform.GPUs) == 0 {
		return
	}
	o := r.platform.GPUs[0].RDMAEngine.NearestOracleOf()
	if o == nil {
		return
	}
	s := o.Stats
	col := func(what string, v float64) { r.metricsCollector.Collect("NearestOracle", what, v) }
	col("picks", float64(s.Picks))
	col("redirected", float64(s.Redirected))
	col("hops_home", s.HopsHome)
	col("hops_served", s.HopsServed)
	col("holder_hits", float64(s.HolderHits))
	col("holder_misses", float64(s.HolderMisses))
	col("fills_noted", float64(s.FillsNoted))
	col("fills_stale", float64(s.FillsStale))
	col("writes_noted", float64(s.WritesNoted))
	col("kernels", float64(s.Kernels))
	for g := range s.ServedByEngine {
		if s.ServedByEngine[g]+s.HomeByEngine[g] > 0 {
			name := fmt.Sprintf("GPU%d", g)
			r.metricsCollector.Collect(name, "no_served", float64(s.ServedByEngine[g]))
			r.metricsCollector.Collect(name, "no_home", float64(s.HomeByEngine[g]))
		}
	}
}
