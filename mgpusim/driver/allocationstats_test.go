package driver

import "testing"

func TestAllocationStatsRoundUpToConfiguredPageSize(t *testing.T) {
	d := &Driver{Log2PageSize: 12}

	d.recordAllocation(AllocationClassWorkload, 4097, false)

	got := d.AllocationStatsSnapshot()
	if got.PageSize != 4096 || got.Overall.AllocatedPages != 2 ||
		got.Overall.RoundedBytes != 8192 || got.Overall.RequestedBytes != 4097 {
		t.Fatalf("unexpected rounded allocation stats: %+v", got)
	}
}

func TestAllocationStatsClassifiesWorkloadAndRuntime(t *testing.T) {
	d := &Driver{Log2PageSize: 10}

	d.recordAllocation(AllocationClassWorkload, 1, true)
	d.recordAllocation(AllocationClassRuntime, 1024, false)

	got := d.AllocationStatsSnapshot()
	if got.Overall.AllocationCalls != 2 || got.Overall.AllocatedPages != 2 {
		t.Fatalf("unexpected overall stats: %+v", got.Overall)
	}
	if got.Workload.AllocationCalls != 1 || got.Workload.UnifiedCalls != 1 ||
		got.Workload.RequestedBytes != 1 {
		t.Fatalf("unexpected workload stats: %+v", got.Workload)
	}
	if got.Runtime.AllocationCalls != 1 || got.Runtime.NormalCalls != 1 ||
		got.Runtime.RequestedBytes != 1024 {
		t.Fatalf("unexpected runtime stats: %+v", got.Runtime)
	}
}

func TestBeforeFirstKernelLaunchHookRunsOnce(t *testing.T) {
	d := &Driver{Log2PageSize: 12}
	d.recordAllocation(AllocationClassWorkload, 4097, false)

	calls := 0
	d.SetBeforeFirstKernelLaunchHook(func(got AllocationStatsSnapshot) {
		calls++
		if got.Workload.AllocatedPages != 2 || got.Runtime.AllocatedPages != 0 {
			t.Fatalf("unexpected pre-kernel snapshot: %+v", got)
		}
	})
	d.notifyBeforeFirstKernelLaunch()
	d.notifyBeforeFirstKernelLaunch()

	if calls != 1 {
		t.Fatalf("hook called %d times, want 1", calls)
	}
}
