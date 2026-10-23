package driver

// WG distribution over the GPMs of a unified GPU (ISCA'27 mesh9).
//
// The default (ceil) gives every GPM cuCount * ceil(totalWG / totalCU)
// contiguous work-groups, so a kernel with fewer than one WG per CU of the
// whole wafer fills the first GPMs and leaves the last ones idle (1344 WGs on
// a 9x9 mesh: 48 of 80 GPMs). Balanced splits the same contiguous WG ranges
// in proportion to the CU counts, so every GPM gets a WG as long as there are
// at least as many WGs as GPMs. Kernels, problem sizes and data placement do
// not change; only which GPM runs which WG.

// SetBalancedWGDistribution selects the balanced WG split (default: ceil).
func (d *Driver) SetBalancedWGDistribution(on bool) { d.balancedWGDist = on }

// SetReportWGDistribution prints the per-GPM WG counts of every kernel.
func (d *Driver) SetReportWGDistribution(on bool) { d.reportWGDist = on }

// balancedWGDist returns the cumulative WG boundaries (len(cuCounts)+1) of a
// split of totalWG contiguous WGs proportional to cuCounts.
func balancedWGDist(totalWG int, cuCounts []int) []int {
	dist := make([]int, len(cuCounts)+1)
	totalCU := 0
	for _, c := range cuCounts {
		totalCU += c
	}
	cum := 0
	for i, c := range cuCounts {
		cum += c
		dist[i+1] = int(int64(totalWG) * int64(cum) / int64(totalCU))
	}
	return dist
}
