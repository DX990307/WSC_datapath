package runner

// ISCA'27 geo-push ("geographic broadcast response"): see
// mgpusim/timing/rdma/geo_*.go. Everything is off by default; with the
// default flags no hook is installed and the simulator is unchanged.

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"

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

func geoPushConfigFromFlags() rdma.GeoPushConfig { return rdma.GeoPushConfig{} }

func (r *Runner) reportGeoPushStats() {}
