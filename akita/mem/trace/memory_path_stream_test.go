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
	err := EnableMemoryPathTrace(prefix, 0, 0, 12, 7, true, true, false, false, nil)
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

func TestMemoryPathTailWindowKeepsLastRecords(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "memory_path_tail")
	err := EnableMemoryPathTrace(prefix, 0, 1, 12, 7, true, false, true, false, nil)
	if err != nil {
		t.Fatalf("enable tail-window memory-path trace: %v", err)
	}
	defer DisableMemoryPathTrace()

	recordRemoteAccess(t, "req-1", 0x1000, 0x2000, 1)
	recordRemoteAccess(t, "req-2", 0x1100, 0x2100, 10)

	globalMemoryPathStats.Lock()
	records := len(globalMemoryPathStats.records)
	globalMemoryPathStats.Unlock()
	if records != 1 {
		t.Fatalf("tail trace kept %d completed records in memory, want 1", records)
	}

	if err := DumpMemoryPathTrace(); err != nil {
		t.Fatalf("dump tail-window memory-path trace: %v", err)
	}

	rawRows := readGzipCSVRows(t, prefix+"_raw.csv.gz")
	if got := len(rawRows); got != 2 {
		t.Fatalf("raw rows including header = %d, want 2", got)
	}
	if rawRows[1][2] != "req-2" {
		t.Fatalf("tail raw row kept %q, want req-2", rawRows[1][2])
	}

	summaryRows := readCSVRows(t, prefix+"_l1v_path_summary.csv")
	if got := len(summaryRows); got != 2 {
		t.Fatalf("l1v summary rows including header = %d, want 2", got)
	}
	if summaryRows[1][0] != "req-2" {
		t.Fatalf("tail summary row kept %q, want req-2", summaryRows[1][0])
	}
}

func TestMemoryPathBatchInfoFansOutDRAMStages(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "memory_path_batch")
	err := EnableMemoryPathTrace(prefix, 0, 0, 12, 7, false, false, false, false, nil)
	if err != nil {
		t.Fatalf("enable memory-path trace: %v", err)
	}
	defer DisableMemoryPathTrace()

	infoA := localL2AccessInfo("req-a", 0x1000, 0x2000)
	infoB := localL2AccessInfo("req-b", 0x1040, 0x2040)
	recordLocalMissToWriteBuffer("req-a", infoA, 0x1000, 0x2000)
	recordLocalMissToWriteBuffer("req-b", infoB, 0x1040, 0x2040)

	batchInfo := WithMemoryPathBatchInfo(infoA, infoB)
	RecordMemoryPathDRAMRequestReceive(
		"GPU0.DRAM", batchInfo, "batch-read", ns(6), ns(7), nil, nil,
	)
	RecordMemoryPathDRAMResponse(
		"GPU0.DRAM", batchInfo, "batch-read", "batch-rsp", ns(20),
	)
	completeLocalMiss("req-a", infoA, 0x1000, 0x2000)
	completeLocalMiss("req-b", infoB, 0x1040, 0x2040)

	if err := DumpMemoryPathTrace(); err != nil {
		t.Fatalf("dump memory-path trace: %v", err)
	}

	stageRows := readCSVRows(t, prefix+"_l1v_path_stage_summary.csv")
	assertStageSummary(t, stageRows, "l2_bottom_send_to_dram", "2", "2", "2")
	assertStageSummary(t, stageRows, "dram_queue_and_service", "2", "2", "26")
}

func recordRemoteAccess(
	t *testing.T,
	reqID string,
	vaddr uint64,
	paddr uint64,
	baseNS uint64,
) {
	t.Helper()

	info := L2AccessInfo{
		OriginalReqID: reqID,
		HasVAddr:      true,
		VAddr:         vaddr,
		HasPAddr:      true,
		PAddr:         paddr,
		IsRemote:      true,
		RequesterGPM:  0,
		ProviderGPM:   1,
	}

	RecordMemoryPathCacheStart(
		"GPU0.L1VCache", reqID, info, vaddr, 64, 1, "read",
		sim.VTimeInSec(baseNS),
	)
	RecordMemoryPathCacheResult(
		"GPU0.L1VCache", reqID, info, vaddr, 64, 1, "read", "miss",
		sim.VTimeInSec(baseNS+1),
	)
	RecordMemoryPathDataSource(
		"GPU1.L2Cache", info, paddr, 64,
		sim.VTimeInSec(baseNS), sim.VTimeInSec(baseNS+2), "read", "dram",
	)
	RecordMemoryPathCacheComplete(
		"GPU0.L1VCache", reqID, info, vaddr, 64, 1, "read",
		sim.VTimeInSec(baseNS+3),
	)
}

func localL2AccessInfo(reqID string, vaddr uint64, paddr uint64) L2AccessInfo {
	return L2AccessInfo{
		OriginalReqID: reqID,
		HasVAddr:      true,
		VAddr:         vaddr,
		HasPAddr:      true,
		PAddr:         paddr,
		IsRemote:      false,
		RequesterGPM:  0,
		ProviderGPM:   0,
	}
}

func recordLocalMissToWriteBuffer(reqID string, info L2AccessInfo, vaddr uint64, paddr uint64) {
	l2ReqID := "l2-" + reqID
	RecordMemoryPathCacheStart(
		"GPU0.L1VCache", reqID, info, vaddr, 64, 1, "read", ns(1),
	)
	RecordMemoryPathCacheResult(
		"GPU0.L1VCache", reqID, info, vaddr, 64, 1, "read", "miss", ns(2),
	)
	RecordMemoryPathL2TopReceive(
		"GPU0.L2[0]", l2ReqID, info, ns(2), ns(3), nil, nil,
	)
	RecordMemoryPathCacheStart(
		"GPU0.L2[0]", l2ReqID, info, paddr, 64, 1, "read", ns(3),
	)
	RecordMemoryPathL2DirStart("GPU0.L2[0]", l2ReqID, info, ns(4))
	RecordMemoryPathCacheResult(
		"GPU0.L2[0]", l2ReqID, info, paddr, 64, 1, "read", "miss", ns(5),
	)
	RecordMemoryPathL2WriteBufferSend("GPU0.L2[0]", info, "batch-read", ns(6))
}

func completeLocalMiss(reqID string, info L2AccessInfo, vaddr uint64, paddr uint64) {
	l2ReqID := "l2-" + reqID
	RecordMemoryPathL2DRAMResponse(
		"GPU0.L2[0]", info, "batch-read", "batch-rsp", ns(20), ns(21), nil, nil,
	)
	RecordMemoryPathDataSource(
		"GPU0.L2[0]", info, paddr, 64, ns(18), ns(21), "read", "dram",
	)
	RecordMemoryPathCacheComplete(
		"GPU0.L2[0]", l2ReqID, info, paddr, 64, 1, "read", ns(22),
	)
	RecordMemoryPathCacheComplete(
		"GPU0.L1VCache", reqID, info, vaddr, 64, 1, "read", ns(23),
	)
}

func ns(value uint64) sim.VTimeInSec {
	return sim.VTimeInSec(float64(value) / 1e9)
}

func assertStageSummary(
	t *testing.T,
	rows [][]string,
	stage string,
	wantPaths string,
	wantHops string,
	wantTotal string,
) {
	t.Helper()
	for _, row := range rows[1:] {
		if len(row) < 7 || row[0] != stage {
			continue
		}
		if row[1] != wantPaths || row[2] != wantHops || row[6] != wantTotal {
			t.Fatalf(
				"stage %s row = %#v, want paths=%s hops=%s total=%s",
				stage, row, wantPaths, wantHops, wantTotal,
			)
		}
		return
	}
	t.Fatalf("missing stage summary for %s in %#v", stage, rows)
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
