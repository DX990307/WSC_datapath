package writearound

import (
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

func TestInvalidateAllDropsIdleLinesOnly(t *testing.T) {
	c := NewBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithLowModuleFinder(nil).
		Build("L1")
	sets := c.directory.GetSets()
	valid := sets[0].Blocks[0]
	valid.IsValid = true
	locked := sets[0].Blocks[1]
	locked.IsValid = true
	locked.IsLocked = true
	reading := sets[1].Blocks[0]
	reading.IsValid = true
	reading.ReadCount = 1

	if n := c.InvalidateAll(0); n != 1 {
		t.Fatalf("invalidated %d lines, want 1", n)
	}
	if valid.IsValid {
		t.Fatal("idle valid line still valid")
	}
	if !locked.IsValid || !reading.IsValid {
		t.Fatal("in-flight lines must not be invalidated")
	}
	if n := c.InvalidateAll(1); n != 0 {
		t.Fatalf("second invalidation dropped %d lines, want 0", n)
	}
}
