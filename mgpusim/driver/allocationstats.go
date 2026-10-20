package driver

// AllocationClass identifies the source of a driver memory allocation.
type AllocationClass uint8

const (
	// AllocationClassWorkload is memory explicitly allocated by a benchmark.
	AllocationClassWorkload AllocationClass = iota
	// AllocationClassRuntime is memory allocated internally to launch kernels.
	AllocationClassRuntime
)

// AllocationStats contains counters for a group of memory allocations.
type AllocationStats struct {
	AllocationCalls uint64
	RequestedBytes  uint64
	AllocatedPages  uint64
	RoundedBytes    uint64
	NormalCalls     uint64
	UnifiedCalls    uint64
}

// AllocationStatsSnapshot is a consistent copy of the driver's allocation
// counters. Overall is the sum of Workload and Runtime.
type AllocationStatsSnapshot struct {
	PageSize uint64
	Overall  AllocationStats
	Workload AllocationStats
	Runtime  AllocationStats
}

// SetBeforeFirstKernelLaunchHook installs a one-shot callback that runs after
// workload setup allocations and immediately before the first kernel launch
// allocates its runtime metadata. It is intended for fast footprint profiling.
func (d *Driver) SetBeforeFirstKernelLaunchHook(
	hook func(AllocationStatsSnapshot),
) {
	d.firstKernelHook = hook
}

func (d *Driver) notifyBeforeFirstKernelLaunch() {
	d.firstKernelOnce.Do(func() {
		if d.firstKernelHook != nil {
			d.firstKernelHook(d.AllocationStatsSnapshot())
		}
	})
}

// AllocationStatsSnapshot returns a thread-safe, consistent counter snapshot.
func (d *Driver) AllocationStatsSnapshot() AllocationStatsSnapshot {
	d.allocationStatsMu.RLock()
	defer d.allocationStatsMu.RUnlock()

	snapshot := d.allocationStats
	snapshot.PageSize = uint64(1) << d.Log2PageSize
	return snapshot
}

func (d *Driver) recordAllocation(
	class AllocationClass,
	requestedBytes uint64,
	unified bool,
) {
	pageSize := uint64(1) << d.Log2PageSize
	pages := requestedBytes / pageSize
	if requestedBytes%pageSize != 0 {
		pages++
	}

	d.allocationStatsMu.Lock()
	defer d.allocationStatsMu.Unlock()

	recordAllocationStats(&d.allocationStats.Overall, requestedBytes, pages, pageSize, unified)
	switch class {
	case AllocationClassRuntime:
		recordAllocationStats(&d.allocationStats.Runtime, requestedBytes, pages, pageSize, unified)
	default:
		recordAllocationStats(&d.allocationStats.Workload, requestedBytes, pages, pageSize, unified)
	}
}

func recordAllocationStats(
	stats *AllocationStats,
	requestedBytes, pages, pageSize uint64,
	unified bool,
) {
	stats.AllocationCalls++
	stats.RequestedBytes += requestedBytes
	stats.AllocatedPages += pages
	stats.RoundedBytes += pages * pageSize
	if unified {
		stats.UnifiedCalls++
	} else {
		stats.NormalCalls++
	}
}
