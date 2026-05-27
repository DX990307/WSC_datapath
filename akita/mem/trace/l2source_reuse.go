package trace

func (s *l2SourceStats) recordRemoteFillLocked(
	dataKey l2DataSourceKey,
	bytes uint64,
	timeNS uint64,
) {
	fillKey := l2RemoteFillKey{
		requesterGPM: dataKey.requesterGPM,
		providerGPM:  dataKey.providerGPM,
		component:    dataKey.component,
		hasVAddr:     dataKey.hasVAddr,
		vaddr:        dataKey.vaddr,
		hasPAddr:     dataKey.hasPAddr,
		paddr:        dataKey.paddr,
	}
	counter := s.remoteFillReuse[fillKey]
	if counter == nil {
		counter = &l2RemoteFillCounter{}
		s.remoteFillReuse[fillKey] = counter
		s.addRemoteFillIndexLocked(fillKey)
	}

	if counter.fills == 0 {
		counter.firstFillTimeNS = timeNS
	}
	counter.fills++
	counter.bytes += bytes
	counter.lastFillTimeNS = timeNS
}

func (s *l2SourceStats) addRemoteFillIndexLocked(fillKey l2RemoteFillKey) {
	lookupKey := l2RemoteFillLookupKey{
		providerGPM: fillKey.providerGPM,
		component:   fillKey.component,
		hasPAddr:    fillKey.hasPAddr,
		paddr:       fillKey.paddr,
	}
	s.remoteFillIndex[lookupKey] = append(
		s.remoteFillIndex[lookupKey], fillKey)
}

func (s *l2SourceStats) recordLocalReuseOfRemoteFillLocked(
	dataKey l2DataSourceKey,
	bytes uint64,
	timeNS uint64,
) {
	lookupKey := l2RemoteFillLookupKey{
		providerGPM: dataKey.providerGPM,
		component:   dataKey.component,
		hasPAddr:    dataKey.hasPAddr,
		paddr:       dataKey.paddr,
	}

	for _, fillKey := range s.remoteFillIndex[lookupKey] {
		counter := s.remoteFillReuse[fillKey]
		if counter == nil || counter.fills == 0 {
			continue
		}
		if timeNS < counter.lastFillTimeNS {
			continue
		}

		if counter.localL2HitReuses == 0 {
			counter.firstLocalReuseNS = timeNS
		}
		counter.localL2HitReuses++
		counter.localL2HitReuseBytes += bytes
		counter.lastLocalReuseNS = timeNS
	}
}
