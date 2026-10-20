package benchmarkselection

import "flag"

// Problem-size overrides for the remote-type workloads (P13 C2: weak scaling
// on 3x3 / 5x5 meshes). The defaults are the sizes that were hard-coded in
// SelectBenchmark, so runs that do not pass them are unchanged.
var (
	fwNodesFlag = flag.Uint("fw-nodes", 11584,
		"floydwarshall: number of nodes (adjacency matrix is N x N float32)")
	mtWidthFlag = flag.Int("mt-width", 11520,
		"matrixtranspose: matrix width (multiple of 64)")
	prNodesFlag       = flag.Uint("pr-nodes", 1<<20, "pagerank: number of nodes")
	prConnectionsFlag = flag.Uint("pr-connections", 132644864,
		"pagerank: number of edges (CSR non-zeros)")
)
