package rdma

// RemoteReuseBoundaryStats counts the requester-side work of the M3
// kernel-boundary invalidation. All zero unless the invalidation is enabled.
type RemoteReuseBoundaryStats struct {
	Boundaries   uint64 // kernel boundaries seen by this GPM network interface
	StaleEntries uint64 // in-flight remote lines marked stale at a boundary
	// UncacheableResets counts remote lines whose own-write uncacheable
	// mark was cleared at a boundary (ResetUncacheableAtBoundary).
	UncacheableResets uint64
}

// GetRemoteReuseBoundaryStats returns the kernel-boundary counters.
func (c *Comp) GetRemoteReuseBoundaryStats() RemoteReuseBoundaryStats {
	return c.remoteReuseBoundaryStats
}

// MarkRemoteReuseKernelBoundary is the requester-side half of the M3
// (Remote Reuse) kernel-boundary invalidation; the requester L2 slices drop
// their replica lines in writeback.Cache.InvalidateRemoteReplicas at the same
// moment. A remote line still in flight here was requested before the
// boundary (by the time the next kernel launches, every demand read of the
// previous kernel has been answered, so these are prefetches or lines whose
// clean fill has not been sent yet). Such a line must not install its
// possibly stale data in the requester L2 after the boundary, and a read of
// the new kernel must not merge into it: the entry is marked stale (no
// fill), closed for M1 merging, and the line's epoch is advanced so a new
// demand opens a fresh entry, the same mechanism a requester write uses.
// Writes need no handling: a remote write is acknowledged by the owner
// before its wavefront can finish, so no unacknowledged write remains at a
// kernel boundary.
func (c *Comp) MarkRemoteReuseKernelBoundary() {
	c.remoteReuseBoundaryStats.Boundaries++
	c.m1wHubBoundary()
	if !c.remoteConfig.Enabled || c.remoteConfig.DisableRequesterL2 {
		return
	}
	c.ensureRemoteDataPathState()
	for _, entry := range c.remoteLines {
		if entry.boundaryStale {
			continue
		}
		entry.boundaryStale = true
		entry.m1Open = false
		c.remoteEpochs[entry.key.remoteLineIdentity]++
		c.remoteReuseBoundaryStats.StaleEntries++
	}
	if c.remoteConfig.ResetUncacheableAtBoundary {
		c.remoteReuseBoundaryStats.UncacheableResets +=
			uint64(len(c.remoteUncacheable))
		clear(c.remoteUncacheable)
	}
}
