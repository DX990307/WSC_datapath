package kernels

import "sync"

// Launch-index registry (ISCA27 wg-to-data). The driver numbers unified
// multi-GPU kernel launches 0, 1, ... in launch order and registers every
// per-GPM dispatch packet of a launch under that number, so that a passive
// statistic at a CU can tell which kernel a WG belongs to. Statistics only.

var launchIdxMu sync.RWMutex
var launchIdx = map[*HsaKernelDispatchPacket]int{}

// RegisterLaunchIndex records the kernel launch number of a packet.
func RegisterLaunchIndex(pkt *HsaKernelDispatchPacket, idx int) {
	if pkt == nil {
		return
	}
	launchIdxMu.Lock()
	launchIdx[pkt] = idx
	launchIdxMu.Unlock()
}

// LaunchIndex returns the kernel launch number of a packet, or -1.
func LaunchIndex(pkt *HsaKernelDispatchPacket) int {
	if pkt == nil {
		return -1
	}
	launchIdxMu.RLock()
	idx, ok := launchIdx[pkt]
	launchIdxMu.RUnlock()
	if !ok {
		return -1
	}
	return idx
}
