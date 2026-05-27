package trace

import "github.com/sarchlab/akita/v3/sim"

// RecordL2LocalDRAMAccess records traffic between a local L2 cache and its
// local DRAM controller.
func RecordL2LocalDRAMAccess(
	cacheName string,
	bytes uint64,
	latency sim.VTimeInSec,
	receiveTime sim.VTimeInSec,
	op string,
) {
	if bytes == 0 {
		return
	}
	if !isL2CacheName(cacheName) {
		return
	}

	globalL2SourceStats.Lock()
	defer globalL2SourceStats.Unlock()

	if !globalL2SourceStats.enabled {
		return
	}

	key := l2LocalKey{
		gpm:       parseGPMID(cacheName),
		component: cacheName,
		op:        normalizeOp(op),
	}
	counter := globalL2SourceStats.localCounter(key)
	counter.add(bytes, latency, receiveTime)
}

// RecordL2LocalDRAMFill records a cache-line fill from a local DRAM controller
// into a local L2 cache.
func RecordL2LocalDRAMFill(
	cacheName string,
	bytes uint64,
	latency sim.VTimeInSec,
	receiveTime sim.VTimeInSec,
	op string,
) {
	RecordL2LocalDRAMAccess(cacheName, bytes, latency, receiveTime, op)
}

// RecordRemoteGPMAccess records data returned through the RDMA path from a
// remote GPM. The requester and provider names can be component or port names;
// GPU[x] is extracted when present.
func RecordRemoteGPMAccess(
	requesterName, providerName string,
	bytes uint64,
	latency sim.VTimeInSec,
	receiveTime sim.VTimeInSec,
	op string,
) {
	if bytes == 0 {
		return
	}

	globalL2SourceStats.Lock()
	defer globalL2SourceStats.Unlock()

	if !globalL2SourceStats.enabled {
		return
	}

	requesterGPM := parseGPMID(requesterName)
	providerGPM := parseGPMID(providerName)
	key := l2RemoteKey{
		requesterGPM: requesterGPM,
		providerGPM:  providerGPM,
		requester:    requesterName,
		provider:     providerName,
		hops: manhattanHops(
			requesterGPM,
			providerGPM,
			globalL2SourceStats.tileWidth,
		),
		op: normalizeOp(op),
	}
	counter := globalL2SourceStats.remoteCounter(key)
	counter.add(bytes, latency, receiveTime)
}

// RecordL2AccessSource records whether an L2 request was served by a local or
// remote L2 cache/MSHR/DRAM. The sourceBase should be l2_cache, l2_mshr, dram,
// or write_allocate.
func RecordL2AccessSource(
	cacheName string,
	info interface{},
	address uint64,
	bytes uint64,
	latency sim.VTimeInSec,
	receiveTime sim.VTimeInSec,
	op string,
	sourceBase string,
) {
	if bytes == 0 {
		return
	}
	sourceBase = normalizeSourceBase(sourceBase)

	globalL2SourceStats.Lock()
	defer globalL2SourceStats.Unlock()

	if !globalL2SourceStats.enabled {
		return
	}

	accessInfo, _ := GetL2AccessInfo(info)
	key := globalL2SourceStats.dataSourceKey(
		cacheName, accessInfo, address, op, sourceBase)
	counter := globalL2SourceStats.dataSourceCounter(key)
	counter.add(bytes, latency, receiveTime)

	if accessInfo.IsRemote && sourceBase == sourceBaseDRAM {
		globalL2SourceStats.recordRemoteFillLocked(
			key, bytes, timeToNS(receiveTime))
	}

	if !accessInfo.IsRemote && sourceBase == sourceBaseL2Cache {
		globalL2SourceStats.recordLocalReuseOfRemoteFillLocked(
			key, bytes, timeToNS(receiveTime))
	}
}

func (s *l2SourceStats) localCounter(key l2LocalKey) *l2SourceCounter {
	counter := s.localDRAM[key]
	if counter == nil {
		counter = &l2SourceCounter{}
		s.localDRAM[key] = counter
	}
	return counter
}

func (s *l2SourceStats) remoteCounter(key l2RemoteKey) *l2SourceCounter {
	counter := s.remoteGPM[key]
	if counter == nil {
		counter = &l2SourceCounter{}
		s.remoteGPM[key] = counter
	}
	return counter
}

func (s *l2SourceStats) dataSourceCounter(key l2DataSourceKey) *l2SourceCounter {
	counter := s.dataSource[key]
	if counter == nil {
		counter = &l2SourceCounter{}
		s.dataSource[key] = counter
	}
	return counter
}

func (s *l2SourceStats) dataSourceKey(
	cacheName string,
	info L2AccessInfo,
	address uint64,
	op string,
	sourceBase string,
) l2DataSourceKey {
	requesterGPM, providerGPM := requesterProviderGPM(cacheName, info)
	vaddr, hasVAddr := vaddrForAddress(info, address)

	return l2DataSourceKey{
		source:       qualifiedSource(info.IsRemote, sourceBase),
		requesterGPM: requesterGPM,
		providerGPM:  providerGPM,
		hops:         manhattanHops(requesterGPM, providerGPM, s.tileWidth),
		component:    cacheName,
		op:           normalizeOp(op),
		hasVAddr:     hasVAddr,
		vaddr:        vaddr,
		hasPAddr:     true,
		paddr:        address,
	}
}

func requesterProviderGPM(cacheName string, info L2AccessInfo) (int, int) {
	providerGPM := parseGPMID(cacheName)
	requesterGPM := providerGPM

	if !info.IsRemote {
		return requesterGPM, providerGPM
	}

	requesterGPM = info.RequesterGPM
	if info.ProviderGPM >= 0 {
		providerGPM = info.ProviderGPM
	}

	return requesterGPM, providerGPM
}

func vaddrForAddress(info L2AccessInfo, paddr uint64) (uint64, bool) {
	if !info.HasVAddr {
		return 0, false
	}

	if !info.HasPAddr {
		return info.VAddr, true
	}

	if paddr >= info.PAddr {
		return info.VAddr + paddr - info.PAddr, true
	}

	delta := info.PAddr - paddr
	if info.VAddr < delta {
		return info.VAddr, true
	}

	return info.VAddr - delta, true
}

func qualifiedSource(isRemote bool, sourceBase string) string {
	if isRemote {
		return sourcePrefixRemote + sourceBase
	}
	return sourcePrefixLocal + sourceBase
}
