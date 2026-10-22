package runner

// ISCA'27 wafer-related M1 bounds (mgpusim/timing/rdma/m1w_bound.go).
// Statistics only, off by default; with the flag off nothing is installed.

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"

	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/timing/cp"
	"github.com/sarchlab/mgpusim/v3/timing/rdma"
)

var m1wBoundFlag = flag.Bool("report-m1w-bound", false,
	"Statistics only (ISCA'27 wafer M1 bounds): regional / turn-point / "+
		"in-network request combining, distance-aware batch holding, and "+
		"RTT-weighted window occupancy, from every requester network "+
		"transaction. Writes <metric-file-name>_m1w_bound.csv; the metrics "+
		"file is unchanged. Does not change timing.")

var m1wHubModeFlag = flag.String("m1w-hub-mode", "",
	"ISCA'27 wafer M1 combining hubs (off when empty): region = far reads go "+
		"through the requester region's hub GPM, which merges same-line reads "+
		"of its region while they are in flight (and retains them "+
		"-m1w-hub-retain-ns); turn = the hub is the GPM at the turn tile "+
		"(x_requester, y_owner) of the Y-X route (no detour). Requires the "+
		"remote data path with T18 composition (v4_complete).")
var m1wHubPartFlag = flag.Int("m1w-hub-part", 2,
	"m1w hub region mode: the mesh is split into part x part regions.")
var m1wHubMinHopsFlag = flag.Int("m1w-hub-min-hops", 2,
	"m1w hubs: only reads to owners more than this many hops away use a hub.")
var m1wHubRetainFlag = flag.Float64("m1w-hub-retain-ns", 0,
	"m1w hubs: keep a returned line at the hub this long for later reads "+
		"(kernel-scoped; 0 = merge only while in flight).")
var m1wHubEntriesFlag = flag.Int("m1w-hub-entries", 1024,
	"m1w hubs: line-table capacity of each hub (lines).")

func m1wHubConfigFromFlags() rdma.M1WHubConfig {
	c := rdma.M1WHubConfig{
		Mode:     *m1wHubModeFlag,
		Part:     *m1wHubPartFlag,
		MinHops:  *m1wHubMinHopsFlag,
		RetainNS: *m1wHubRetainFlag,
		Entries:  *m1wHubEntriesFlag,
	}
	if err := c.Validate(); err != nil {
		panic(err)
	}
	return c
}

// m1wTopology builds the mesh topology (GPM order = creation order: row by
// row, skipping the center tile).
func (r *Runner) m1wTopology() *rdma.M1WTopology {
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
				panic("m1w topology: fewer GPUs than mesh tiles")
			}
			coords = append(coords, [2]int{x, y})
			ports = append(ports, r.platform.GPUs[i].RDMAEngine.ToOutside)
			i++
		}
	}
	if i != len(r.platform.GPUs) {
		panic("m1w topology: GPU count does not match the mesh")
	}
	return rdma.NewM1WTopology(w, h, coords, ports)
}

// configureM1W installs the wafer M1 bound statistic (timing only).
func (r *Runner) configureM1W() {
	rdma.DisableM1WBound()
	hub := m1wHubConfigFromFlags()
	if !r.Timing || (!*m1wBoundFlag && !hub.Enabled()) {
		return
	}
	topo := r.m1wTopology()
	if *m1wBoundFlag {
		rdma.EnableM1WBound(topo, rdma.DefaultM1WBoundConfig())
		fmt.Printf("[Config] m1w bound statistic on\n")
	}
	if hub.Enabled() {
		if !*remoteDataPathEnableFlag || !*remoteReuseKernelInvalidateFlag {
			panic("-m1w-hub-mode needs the remote data path with M3 " +
				"kernel-boundary invalidation (v4_complete)")
		}
		fmt.Printf("[Config] m1w hub mode=%s part=%d min-hops=%d "+
			"retain=%gns entries=%d\n", hub.Mode, hub.Part, hub.MinHops,
			hub.RetainNS, hub.Entries)
	}
	for _, gpu := range r.platform.GPUs {
		engine := gpu.RDMAEngine
		engine.SetM1WTopology(topo)
		if hub.Enabled() {
			engine.ConfigureM1WHub(hub)
		}
		proc, ok := gpu.CommandProcessor.(*cp.CommandProcessor)
		if !ok || proc == nil {
			panic("m1w: GPU has no timing command processor")
		}
		proc.KernelLaunchHooks = append(proc.KernelLaunchHooks,
			func(now sim.VTimeInSec) { engine.M1WKernelBoundary(now) })
	}
}

func (r *Runner) reportM1W() {
	if r.Timing && *m1wHubModeFlag != "" {
		for _, gpu := range r.platform.GPUs {
			st := gpu.RDMAEngine.GetM1WHubStats()
			if !st.Enabled {
				continue
			}
			where := gpu.RDMAEngine.Name()
			put := func(name string, v uint64) {
				r.metricsCollector.Collect(where, "m1w_hub_"+name, float64(v))
			}
			put("routed_txns", st.RoutedTxns)
			put("routed_lines", st.RoutedLines)
			put("direct_written_txns", st.DirectTxns)
			put("reqs", st.HubReqs)
			put("lines", st.HubLines)
			put("inflight_hits", st.HubInflightHits)
			put("ready_hits", st.HubReadyHits)
			put("forward_txns", st.HubForwardTxns)
			put("forward_lines", st.HubForwardLines)
			put("untracked_lines", st.HubUntracked)
			put("responses", st.HubResponses)
			put("stale_at_boundary", st.HubStaleAtBound)
			put("peak_entries", st.HubPeakEntries)
			put("out_peak", st.HubOutPeak)
		}
	}
	b := rdma.CurrentM1WBound()
	if !r.Timing || b == nil {
		return
	}
	f, err := os.Create(*filenameFlag + "_m1w_bound.csv")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	w.Write([]string{"metric", "value"})
	for _, row := range b.Rows() {
		w.Write([]string{row[0], row[1]})
	}
	w.Flush()
}
