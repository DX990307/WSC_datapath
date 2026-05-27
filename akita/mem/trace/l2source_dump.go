package trace

import (
	"fmt"
	"os"
)

// DumpL2SourceStats writes the aggregate CSV files when tracking is enabled.
func DumpL2SourceStats() error {
	globalL2SourceStats.Lock()
	defer globalL2SourceStats.Unlock()

	if !globalL2SourceStats.enabled {
		return nil
	}

	if err := globalL2SourceStats.dumpSummary(); err != nil {
		return err
	}
	if err := globalL2SourceStats.dumpRemoteMatrix(); err != nil {
		return err
	}
	if err := globalL2SourceStats.dumpDataSource(); err != nil {
		return err
	}
	return globalL2SourceStats.dumpRemoteFillReuse()
}

func (s *l2SourceStats) dumpSummary() error {
	file, err := os.Create(s.prefix + "_summary.csv")
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprintln(file, summaryCSVHeader)

	for _, key := range s.sortedLocalKeys() {
		counter := s.localDRAM[key]
		fmt.Fprintf(file, "local_dram,%d,%d,0,%s,%s,%d,%d,%d\n",
			key.gpm, key.gpm, key.component, key.op,
			counter.accesses, counter.bytes, counter.avgLatencyNS())
	}

	for _, key := range s.sortedRemoteKeys() {
		counter := s.remoteGPM[key]
		component := key.requester + "->" + key.provider
		fmt.Fprintf(file, "remote_gpm,%d,%d,%d,%s,%s,%d,%d,%d\n",
			key.requesterGPM, key.providerGPM, key.hops, component, key.op,
			counter.accesses, counter.bytes, counter.avgLatencyNS())
	}

	return nil
}

func (s *l2SourceStats) dumpRemoteMatrix() error {
	file, err := os.Create(s.prefix + "_remote_matrix.csv")
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprintln(file, remoteMatrixCSVHeader)

	for _, key := range s.sortedRemoteKeys() {
		counter := s.remoteGPM[key]
		fmt.Fprintf(file, "%d,%d,%d,%s,%d,%d,%d\n",
			key.requesterGPM, key.providerGPM, key.hops, key.op,
			counter.accesses, counter.bytes, counter.avgLatencyNS())
	}

	return nil
}

func (s *l2SourceStats) dumpDataSource() error {
	file, err := os.Create(s.prefix + "_data_source.csv")
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprintln(file, dataSourceCSVHeader)

	for _, key := range s.sortedDataSourceKeys() {
		counter := s.dataSource[key]
		fmt.Fprintf(file, "%s,%d,%d,%d,%s,%s,%t,%d,%t,%d,%d,%d,%d\n",
			key.source, key.requesterGPM, key.providerGPM, key.hops,
			key.component, key.op, key.hasVAddr, key.vaddr,
			key.hasPAddr, key.paddr, counter.accesses, counter.bytes,
			counter.avgLatencyNS())
	}

	return nil
}

func (s *l2SourceStats) dumpRemoteFillReuse() error {
	file, err := os.Create(s.prefix + "_remote_fill_reuse.csv")
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprintln(file, remoteFillReuseCSVHeader)

	for _, key := range s.sortedRemoteFillKeys() {
		counter := s.remoteFillReuse[key]
		fmt.Fprintf(file, "%d,%d,%d,%s,%t,%d,%t,%d,%d,%d,%d,%d,%d,%d,%d,%d\n",
			key.requesterGPM, key.providerGPM,
			manhattanHops(key.requesterGPM, key.providerGPM, s.tileWidth),
			key.component, key.hasVAddr, key.vaddr, key.hasPAddr,
			key.paddr, counter.fills, counter.bytes,
			counter.firstFillTimeNS, counter.lastFillTimeNS,
			counter.localL2HitReuses, counter.localL2HitReuseBytes,
			counter.firstLocalReuseNS, counter.lastLocalReuseNS)
	}

	return nil
}
