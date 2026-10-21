package writearound

import "github.com/sarchlab/akita/v3/sim"

// InvalidateAll marks every valid, idle cache line invalid. It models the
// L1 invalidation a GPU performs at a kernel launch (the dispatch-time
// acquire), so a kernel never reads lines cached by an earlier kernel. Lines
// that are locked (fill in flight) or being read are left alone; at a kernel
// boundary there are none. It takes no simulated time. It returns the number
// of lines invalidated.
func (c *Cache) InvalidateAll(now sim.VTimeInSec) int {
	n := 0
	for _, set := range c.directory.GetSets() {
		for _, block := range set.Blocks {
			if !block.IsValid || block.IsLocked || block.ReadCount > 0 {
				continue
			}
			block.IsValid = false
			block.IsDirty = false
			n++
		}
	}
	return n
}
