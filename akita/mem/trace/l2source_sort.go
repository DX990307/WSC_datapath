package trace

import "sort"

func (s *l2SourceStats) sortedLocalKeys() []l2LocalKey {
	keys := make([]l2LocalKey, 0, len(s.localDRAM))
	for key := range s.localDRAM {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].gpm != keys[j].gpm {
			return keys[i].gpm < keys[j].gpm
		}
		if keys[i].component != keys[j].component {
			return keys[i].component < keys[j].component
		}
		return keys[i].op < keys[j].op
	})
	return keys
}

func (s *l2SourceStats) sortedRemoteKeys() []l2RemoteKey {
	keys := make([]l2RemoteKey, 0, len(s.remoteGPM))
	for key := range s.remoteGPM {
		keys = append(keys, key)
	}
	sortRemoteKeys(keys)
	return keys
}

func (s *l2SourceStats) sortedDataSourceKeys() []l2DataSourceKey {
	keys := make([]l2DataSourceKey, 0, len(s.dataSource))
	for key := range s.dataSource {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].requesterGPM != keys[j].requesterGPM {
			return keys[i].requesterGPM < keys[j].requesterGPM
		}
		if keys[i].providerGPM != keys[j].providerGPM {
			return keys[i].providerGPM < keys[j].providerGPM
		}
		if keys[i].source != keys[j].source {
			return keys[i].source < keys[j].source
		}
		if keys[i].component != keys[j].component {
			return keys[i].component < keys[j].component
		}
		if keys[i].paddr != keys[j].paddr {
			return keys[i].paddr < keys[j].paddr
		}
		if keys[i].vaddr != keys[j].vaddr {
			return keys[i].vaddr < keys[j].vaddr
		}
		return keys[i].op < keys[j].op
	})
	return keys
}

func (s *l2SourceStats) sortedPageSourceKeys() []l2PageSourceKey {
	keys := make([]l2PageSourceKey, 0, len(s.pageSource))
	for key := range s.pageSource {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].requesterGPM != keys[j].requesterGPM {
			return keys[i].requesterGPM < keys[j].requesterGPM
		}
		if keys[i].providerGPM != keys[j].providerGPM {
			return keys[i].providerGPM < keys[j].providerGPM
		}
		if keys[i].source != keys[j].source {
			return keys[i].source < keys[j].source
		}
		if keys[i].pagePAddr != keys[j].pagePAddr {
			return keys[i].pagePAddr < keys[j].pagePAddr
		}
		return keys[i].op < keys[j].op
	})
	return keys
}

func (s *l2SourceStats) sortedRemoteFillKeys() []l2RemoteFillKey {
	keys := make([]l2RemoteFillKey, 0, len(s.remoteFillReuse))
	for key := range s.remoteFillReuse {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].requesterGPM != keys[j].requesterGPM {
			return keys[i].requesterGPM < keys[j].requesterGPM
		}
		if keys[i].providerGPM != keys[j].providerGPM {
			return keys[i].providerGPM < keys[j].providerGPM
		}
		if keys[i].component != keys[j].component {
			return keys[i].component < keys[j].component
		}
		if keys[i].paddr != keys[j].paddr {
			return keys[i].paddr < keys[j].paddr
		}
		return keys[i].vaddr < keys[j].vaddr
	})
	return keys
}

func sortRemoteKeys(keys []l2RemoteKey) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].requesterGPM != keys[j].requesterGPM {
			return keys[i].requesterGPM < keys[j].requesterGPM
		}
		if keys[i].providerGPM != keys[j].providerGPM {
			return keys[i].providerGPM < keys[j].providerGPM
		}
		if keys[i].hops != keys[j].hops {
			return keys[i].hops < keys[j].hops
		}
		return keys[i].op < keys[j].op
	})
}
