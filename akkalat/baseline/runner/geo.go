package runner

// ISCA'27 geo-push ("geographic broadcast response"): see
// mgpusim/timing/rdma/geo_*.go. Everything is off by default; with the
// default flags no hook is installed and the simulator is unchanged.

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/timing/cp"
	"github.com/sarchlab/mgpusim/v3/timing/rdma"
)

var geoPushBoundFlag = flag.Bool("report-geo-push-bound", false,
	"Statistics only (ISCA'27 geo-push upper bound): for every line an owner "+
		"returns to a requester R, count the later network reads of the same "+
		"line by R's 1-hop / 2-hop neighbors within 1 us / 10 us that a push "+
		"would have saved, the push copies used, and the bytes x hops. "+
		"Writes <metric-file-name>_geo_bound.csv. Does not change timing.")

// geoTopology builds the platform's geo topology (GPM order = creation order:
// row by row, skipping the center tile).
func (r *Runner) geoTopology() *rdma.GeoTopology {
	w, h := gpmTraceMeshWidth, gpmTraceMeshWidth
	var coords [][2]int
	var ports []sim.Port
	i := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if x == w/2 && y == h/2 {
				continue
			}
			if i >= len(r.platform.GPUs) {
				panic("geo topology: fewer GPUs than mesh tiles")
			}
			coords = append(coords, [2]int{x, y})
			ports = append(ports, r.platform.GPUs[i].RDMAEngine.ToOutside)
			i++
		}
	}
	if i != len(r.platform.GPUs) {
		panic("geo topology: GPU count does not match the mesh")
	}
	return rdma.NewGeoTopology(w, h, coords, ports)
}

// configureGeo installs the geo-push statistic and mechanism (timing only).
func (r *Runner) configureGeo() {
	rdma.DisableGeoBound()
	push := geoPushConfigFromFlags()
	if !r.Timing || (!*geoPushBoundFlag && !push.Enabled()) {
		return
	}
	topo := r.geoTopology()
	if *geoPushBoundFlag {
		rdma.EnableGeoBound(topo)
		fmt.Printf("[Config] geo-push bound statistic on (radius 1/2, " +
			"windows 1 us / 10 us)\n")
	}
	if push.Enabled() {
		fmt.Printf("[Config] geo-push radius=%d shared-filter=%v "+
			"filter-entries=%d fill-queue=%d\n", push.Radius,
			push.SharedFilter, push.FilterEntries, push.FillQueue)
	}
	for _, gpu := range r.platform.GPUs {
		engine := gpu.RDMAEngine
		engine.SetGeoTopology(topo)
		if push.Enabled() {
			engine.ConfigureGeoPush(push)
		}
		proc, ok := gpu.CommandProcessor.(*cp.CommandProcessor)
		if !ok || proc == nil {
			panic("geo-push: GPU has no timing command processor")
		}
		proc.KernelLaunchHooks = append(proc.KernelLaunchHooks,
			func(now sim.VTimeInSec) { engine.GeoKernelBoundary(now) })
	}
}

func (r *Runner) reportGeo() {
	if !r.Timing {
		return
	}
	r.reportGeoPushStats()
	if !*geoPushBoundFlag {
		return
	}
	g := rdma.CurrentGeoBound()
	if g == nil {
		return
	}
	rows := rdma.GeoBoundRows(g.Finish())
	for _, row := range rows {
		var v float64
		fmt.Sscan(row[1], &v)
		r.metricsCollector.Collect("GeoBound", "geo_bound_"+row[0], v)
	}
	f, err := os.Create(*filenameFlag + "_geo_bound.csv")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"metric", "value"})
	for _, row := range rows {
		w.Write([]string{row[0], row[1]})
	}
	w.Flush()
}

var geoPushRadiusFlag = flag.Int("geo-push-radius", 0,
	"ISCA'27 geo-push: when an owner returns a read response to requester R, "+
		"also multicast the line(s) to the GPMs within this many hops of R "+
		"(0 = off, 1 or 2); they keep it in their L2 (M3 fill path). "+
		"Requires the v3 configuration (remote data path with requester L2, T18).")
var geoPushFilterFlag = flag.String("geo-push-filter", "none",
	"geo-push: none (push every response) or shared (push only lines the "+
		"owner has seen read by >= 2 distinct GPMs; -geo-push-filter-entries).")
var geoPushFilterEntriesFlag = flag.Int("geo-push-filter-entries", 1024,
	"geo-push shared filter: owner sharing-table entries (4-way, LRU).")
var geoPushFillQueueFlag = flag.Int("geo-push-fill-queue", 64,
	"geo-push: pending push fills per RDMA before pushes are dropped.")

func geoPushConfigFromFlags() rdma.GeoPushConfig {
	c := rdma.GeoPushConfig{
		Radius:        *geoPushRadiusFlag,
		FilterEntries: *geoPushFilterEntriesFlag,
		FillQueue:     *geoPushFillQueueFlag,
	}
	switch *geoPushFilterFlag {
	case "none", "":
	case "shared":
		c.SharedFilter = true
	default:
		panic("-geo-push-filter must be none or shared")
	}
	if c.Radius < 0 || c.Radius > 2 {
		panic("-geo-push-radius must be 0, 1 or 2")
	}
	if !c.Enabled() {
		return rdma.GeoPushConfig{}
	}
	if !*remoteDataPathEnableFlag || !*remoteDataPathL2EnableFlag ||
		!t18ConfigFromFlagsQuiet().Enabled() {
		panic("-geo-push-radius needs the v3 configuration " +
			"(-remote-data-path-enable, -remote-data-path-l2-enable, T18)")
	}
	return c
}

// t18ConfigFromFlagsQuiet reports whether any T18 switch is on, without
// printing the configuration again.
func t18ConfigFromFlagsQuiet() rdma.T18Config {
	return rdma.T18Config{ReqMerge: *t18ReqMergeFlag, OwnerMerge: *t18OwnerMergeFlag,
		Window: *t18WindowFlag}
}

func (r *Runner) reportGeoPushStats() {
	var l2 writeback.RemoteReplicaStats
	any := false
	for _, gpu := range r.platform.GPUs {
		if gpu.RDMAEngine == nil {
			continue
		}
		st := gpu.RDMAEngine.GetGeoPushStats()
		if !st.Enabled {
			continue
		}
		any = true
		where := gpu.RDMAEngine.Name()
		put := func(name string, v uint64) {
			r.metricsCollector.Collect(where, "geo_push_"+name, float64(v))
		}
		put("owner_rsp_candidates", st.OwnerRspCandidates)
		put("owner_filtered", st.OwnerFiltered)
		put("owner_no_target", st.OwnerNoTarget)
		put("owner_push_packets", st.OwnerPushPackets)
		put("owner_push_dests", st.OwnerPushDests)
		put("owner_push_lines", st.OwnerPushLines)
		put("owner_extra_links", st.OwnerExtraLinks)
		put("owner_extra_link_bytes", st.OwnerExtraLinkByte)
		put("filter_lookups", st.FilterLookups)
		put("filter_shared", st.FilterShared)
		put("rcv_msgs", st.RcvMsgs)
		put("rcv_lines", st.RcvLines)
		put("drop_no_l2", st.DropNoL2)
		put("drop_stale", st.DropStale)
		put("drop_uncacheable", st.DropUncacheable)
		put("drop_have", st.DropHave)
		put("drop_inflight_no_waiter", st.DropInflightNoWtr)
		put("drop_probing", st.DropProbing)
		put("drop_queue_full", st.DropQueueFull)
		put("catch_inflight", st.CatchInflight)
		put("catch_queued", st.CatchQueued)
		put("catch_waiters", st.CatchWaiters)
		put("fills_sent", st.FillsSent)
		put("fills_installed", st.FillsInstalled)
		for _, c := range gpu.L2Caches {
			if wb, ok := c.(*writeback.Cache); ok {
				s := wb.GetRemoteReplicaStats()
				l2.PushFills += s.PushFills
				l2.PushInstalled += s.PushInstalled
				l2.PushDropped += s.PushDropped
				l2.PushDroppedStale += s.PushDroppedStale
				l2.PushDroppedResident += s.PushDroppedResident
				l2.PushUseful += s.PushUseful
				l2.PushHits += s.PushHits
				l2.PushUnusedRetirements += s.PushUnusedRetirements
			}
		}
	}
	if !any {
		return
	}
	put := func(name string, v uint64) {
		r.metricsCollector.Collect("GeoPushL2", "geo_push_l2_"+name, float64(v))
	}
	put("fills", l2.PushFills)
	put("installed", l2.PushInstalled)
	put("dropped", l2.PushDropped)
	put("dropped_stale", l2.PushDroppedStale)
	put("dropped_resident", l2.PushDroppedResident)
	put("useful", l2.PushUseful)
	put("hits", l2.PushHits)
	put("unused_retirements", l2.PushUnusedRetirements)
}
