package rdma

import "testing"

func TestKernelBoundaryMarksInflightRemoteLinesStale(t *testing.T) {
	c, _, _, _, remoteGPU := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled: true, MaxBatchLines: 8, MaxBatches: 8,
			KernelBoundaryInvalidate: true,
		})
	identity := remoteLineIdentity{
		ownerName: remoteGPU.Name(), pid: 1, lineAddr: 0x1400,
	}
	key := remoteLineKey{remoteLineIdentity: identity}
	entry := c.newRemoteDemandEntry(key, remoteGPU, nil)
	entry.m1Open = true
	c.remoteLines[key] = entry

	c.MarkRemoteReuseKernelBoundary()
	if !entry.boundaryStale || entry.m1Open {
		t.Fatal("in-flight line not retired at the kernel boundary")
	}
	if c.remoteEpochs[identity] != 1 {
		t.Fatalf("line epoch = %d, want 1 (new demands open a fresh entry)",
			c.remoteEpochs[identity])
	}
	c.MarkRemoteReuseKernelBoundary()
	if c.remoteEpochs[identity] != 1 {
		t.Fatal("an already stale entry was counted twice")
	}
	s := c.GetRemoteReuseBoundaryStats()
	if s.Boundaries != 2 || s.StaleEntries != 1 {
		t.Fatalf("boundary stats = %+v", s)
	}
}

func TestKernelBoundaryIgnoredWithoutRequesterL2(t *testing.T) {
	c, _, _, _, remoteGPU := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled: true, MaxBatchLines: 8, MaxBatches: 8,
			DisableRequesterL2: true,
		})
	identity := remoteLineIdentity{
		ownerName: remoteGPU.Name(), pid: 1, lineAddr: 0x1400,
	}
	key := remoteLineKey{remoteLineIdentity: identity}
	entry := c.newRemoteDemandEntry(key, remoteGPU, nil)
	c.remoteLines[key] = entry
	c.MarkRemoteReuseKernelBoundary()
	if entry.boundaryStale || c.remoteEpochs[identity] != 0 {
		t.Fatal("boundary changed M2-only state")
	}
}
