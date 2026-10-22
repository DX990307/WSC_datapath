package runner

import (
	"flag"
	"fmt"
)

// Mesh size (P13 C2, scaling runs R5/R6). The wafer is an N x N tile mesh;
// the center tile (N/2, N/2) holds the driver (CPU) and the IOMMU, and the
// other N*N-1 tiles are GPMs, created row by row (createGPUs). The default 7
// is the paper configuration (48 GPMs) and builds exactly the platform that
// was hard-coded before this flag existed.
var meshSizeFlag = flag.Int("mesh-size", 7,
	"Wafer mesh is N x N tiles (odd N >= 3). The center tile holds the "+
		"CPU/driver and IOMMU; the other N*N-1 tiles are GPMs. Default 7 "+
		"(48 GPMs, the paper configuration).")

// MeshSize returns N of the N x N mesh (after -config files are applied).
func MeshSize() int {
	applyConfigFiles()
	n := *meshSizeFlag
	if n < 3 || n%2 == 0 {
		panic(fmt.Sprintf(
			"-mesh-size=%d: must be odd and >= 3 (the CPU/IOMMU tile is the center)", n))
	}
	return n
}

// NumGPMs returns the number of GPMs on the mesh (N*N-1).
func NumGPMs() int {
	n := MeshSize()
	return n*n - 1
}

// l2SourceTileWidth is the tile width the hop-counting statistics use. With
// the default 7x7 mesh it is -l2-source-tile-width unchanged. On another mesh
// the flag's default (7) follows the mesh; any other explicit value must
// match it.
func l2SourceTileWidth() int {
	n := MeshSize()
	w := *l2SourceTileWidthFlag
	if n == 7 || w == n {
		return w
	}
	if w == 7 {
		return n
	}
	panic(fmt.Sprintf("-l2-source-tile-width=%d does not match -mesh-size=%d", w, n))
}

// Problem size reference (ISCA'27 mesh9, user 10-22: strong scaling by
// default). Benchmarks whose size follows the GPM count (the T16 small
// wrappers size their grids as WGs-per-CU x total CUs; allreduce keeps one
// gradient copy per rank) size the problem for this many GPMs, not for the
// GPMs on the mesh. The default 48 keeps every problem at its 7x7 size on any
// mesh (strong scaling); weak scaling sets it to the mesh's GPM count.
var problemGPMsFlag = flag.Int("problem-gpms", 48,
	"GPM count that GPM-dependent problem sizes are computed for (T16 small "+
		"grids, allreduce ranks). Default 48 = the 7x7 problem on any mesh "+
		"(strong scaling); set to N*N-1 for weak scaling.")

var problemCUsPerGPMFlag = flag.Int("problem-cus-per-gpm", 0,
	"CUs per GPM that CU-dependent problem sizes are computed for (T16 small "+
		"grids, allreduce copy size); 0 (default) = the GPM preset's CUs. The "+
		"scaled 9x9 config (mi200s, 16 CUs) sets 28 to keep the 7x7 problems.")

// ProblemCUsPerGPM returns -problem-cus-per-gpm, or the preset's CUs per GPM.
func ProblemCUsPerGPM() int {
	applyConfigFiles()
	if *problemCUsPerGPMFlag > 0 {
		return *problemCUsPerGPMFlag
	}
	return CUsPerGPM()
}

// ProblemGPMs returns -problem-gpms (after -config files are applied).
func ProblemGPMs() int {
	applyConfigFiles()
	n := *problemGPMsFlag
	if n < 1 {
		panic(fmt.Sprintf("-problem-gpms=%d: must be >= 1", n))
	}
	return n
}

// WG split over GPMs (mesh9, user 10-22 option c): the problem stays the
// 48-GPM size; balanced spreads each kernel's WGs over every GPM.
var wgDistributionFlag = flag.String("wg-distribution", "ceil",
	"How the driver splits a kernel's contiguous WG range over the GPMs: "+
		"ceil (default) = cuCount x ceil(WGs / all CUs) per GPM, so small "+
		"kernels leave the last GPMs idle; balanced = proportional to the CU "+
		"count, every GPM gets WGs when WGs >= GPMs. Kernels and data are unchanged.")
var reportWGDistFlag = flag.Bool("report-wg-dist", false,
	"Print the per-GPM WG counts of every kernel (WGDIST lines on stdout).")

func (r *Runner) configureWGDistribution() {
	applyConfigFiles()
	switch *wgDistributionFlag {
	case "ceil":
	case "balanced":
		r.platform.Driver.SetBalancedWGDistribution(true)
	default:
		panic(fmt.Sprintf("-wg-distribution=%s: want ceil or balanced", *wgDistributionFlag))
	}
	if *reportWGDistFlag {
		r.platform.Driver.SetReportWGDistribution(true)
	}
}
