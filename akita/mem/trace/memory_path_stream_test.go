package trace

import (
	"compress/gzip"
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

func TestMemoryPathStreamingWritesAndReleasesRecord(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "memory_path")
	err := EnableMemoryPathTrace(prefix, 0, 0, 12, 7, true, true, false, nil)
	if err != nil {
		t.Fatalf("enable streaming memory-path trace: %v", err)
	}
	defer DisableMemoryPathTrace()

	info := L2AccessInfo{
		OriginalReqID: "req-1",
		HasVAddr:      true,
		VAddr:         0x1000,
		HasPAddr:      true,
		PAddr:         0x2000,
		IsRemote:      true,
		RequesterGPM:  0,
		ProviderGPM:   1,
	}

	RecordMemoryPathCacheStart(
		"GPU0.L1VCache", "req-1", info, 0x1000, 64, 1, "read",
		sim.VTimeInSec(1),
	)
	RecordMemoryPathCacheResult(
		"GPU0.L1VCache", "req-1", info, 0x1000, 64, 1, "read", "miss",
		sim.VTimeInSec(2),
	)
	RecordMemoryPathDataSource(
		"GPU1.L2Cache", info, 0x2000, 64,
		sim.VTimeInSec(1), sim.VTimeInSec(3), "read", "dram",
	)
	RecordMemoryPathCacheComplete(
		"GPU0.L1VCache", "req-1", info, 0x1000, 64, 1, "read",
		sim.VTimeInSec(4),
	)

	globalMemoryPathStats.Lock()
	records := len(globalMemoryPathStats.records)
	globalMemoryPathStats.Unlock()
	if records != 0 {
		t.Fatalf("streaming trace kept %d completed records in memory", records)
	}

	if err := DumpMemoryPathTrace(); err != nil {
		t.Fatalf("dump streaming memory-path trace: %v", err)
	}

	rawRows := readGzipCSVRows(t, prefix+"_raw.csv.gz")
	if got := len(rawRows); got != 2 {
		t.Fatalf("raw rows including header = %d, want 2", got)
	}
	if rawRows[1][0] != "1" || rawRows[1][11] != "true" {
		t.Fatalf("unexpected raw row: %#v", rawRows[1])
	}

	summaryRows := readCSVRows(t, prefix+"_l1v_path_summary.csv")
	if got := len(summaryRows); got != 2 {
		t.Fatalf("l1v summary rows including header = %d, want 2", got)
	}
	if summaryRows[1][0] != "req-1" || summaryRows[1][13] != "true" {
		t.Fatalf("unexpected l1v summary row: %#v", summaryRows[1])
	}
}

func readGzipCSVRows(t *testing.T, path string) [][]string {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("open gzip %s: %v", path, err)
	}
	defer gzipReader.Close()

	rows, err := csv.NewReader(gzipReader).ReadAll()
	if err != nil {
		t.Fatalf("read csv %s: %v", path, err)
	}
	return rows
}

func readCSVRows(t *testing.T, path string) [][]string {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer file.Close()

	rows, err := csv.NewReader(file).ReadAll()
	if err != nil {
		t.Fatalf("read csv %s: %v", path, err)
	}
	return rows
}
