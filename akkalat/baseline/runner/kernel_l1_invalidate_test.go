package runner

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/cache/writearound"
	"github.com/sarchlab/akita/v3/mem/cache/writethrough"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/timing/cp"
)

// A kernel launch must invalidate L1V / L1S / L1I by default (Bug B fix);
// the legacy flag restores the old, non-invalidating model.
func TestKernelLaunchInvalidatesL1ByDefault(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		engine := sim.NewSerialEngine()
		b := MakeR9NanoGPUBuilder().WithLegacyNoKernelL1Invalidate(legacy)
		b.cp = new(cp.CommandProcessor)
		b.l1vCaches = []*writearound.Cache{
			writearound.NewBuilder().WithEngine(engine).Build("L1V"),
		}
		b.l1sCaches = []*writethrough.Cache{
			writethrough.NewBuilder().WithEngine(engine).Build("L1S"),
		}
		b.l1iCaches = []*writethrough.Cache{
			writethrough.NewBuilder().WithEngine(engine).Build("L1I"),
		}
		b.installKernelLaunchL1Invalidation()

		want := 1
		if legacy {
			want = 0
		}
		if got := len(b.cp.KernelLaunchHooks); got != want {
			t.Fatalf("legacy=%v: %d kernel-launch hooks, want %d",
				legacy, got, want)
		}
		for _, hook := range b.cp.KernelLaunchHooks {
			hook(0)
		}
	}
}
