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
