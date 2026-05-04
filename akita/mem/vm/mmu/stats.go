package mmu

// WalkCoalescingEnabled reports whether upper-level page-walk coalescing is
// enabled in the MMU.
func (mmu *MMU) WalkCoalescingEnabled() bool {
	return mmu.walkCoalescingEnabled
}

// CoalescingStats reports how many requests in the MMU benefited from
// last-level and two-level page-walk coalescing.
func (mmu *MMU) CoalescingStats() (lastLevel int, twoLevel int) {
	return mmu.lastLevelCoalescedCount, mmu.twoLevelCoalescedCount
}
