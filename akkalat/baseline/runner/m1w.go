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
	if !r.Timing || !*m1wBoundFlag {
		return
	}
	topo := r.m1wTopology()
	rdma.EnableM1WBound(topo, rdma.DefaultM1WBoundConfig())
	fmt.Printf("[Config] m1w bound statistic on\n")
	for _, gpu := range r.platform.GPUs {
		engine := gpu.RDMAEngine
		engine.SetM1WTopology(topo)
		proc, ok := gpu.CommandProcessor.(*cp.CommandProcessor)
		if !ok || proc == nil {
			panic("m1w: GPU has no timing command processor")
		}
		proc.KernelLaunchHooks = append(proc.KernelLaunchHooks,
			func(now sim.VTimeInSec) { engine.M1WKernelBoundary(now) })
	}
}

func (r *Runner) reportM1W() {
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
