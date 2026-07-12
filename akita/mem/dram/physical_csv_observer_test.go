package dram

import (
	"compress/gzip"
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPhysicalDRAMCSVObserverWritesRawAndLocalitySummary(t *testing.T) {
	dir := t.TempDir()
	rawPath := filepath.Join(dir, "physical.csv.gz")
	summaryPath := filepath.Join(dir, "physical.summary.csv")
	observer, err := NewPhysicalDRAMCSVObserver(rawPath, summaryPath)
	if err != nil {
		t.Fatal(err)
	}

	events := []PhysicalDRAMEvent{
		physicalTestArrival(0, 0, 0, 10, 0, "st-0"),
		physicalTestArrival(1, 0, 0, 10, 0, "st-1"), // same unit
		physicalTestArrival(2, 0, 0, 10, 1, "st-2"), // same row
		physicalTestArrival(3, 0, 0, 11, 0, "st-3"), // same bank
		physicalTestArrival(4, 0, 1, 12, 0, "st-4"), // different bank
	}
	for _, event := range events {
		observer.ObservePhysicalDRAM(event)
	}
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}

	rawRows := readGzipCSV(t, rawPath)
	if got, want := len(rawRows), len(events)+1; got != want {
		t.Fatalf("raw row count %d, want %d", got, want)
	}
	if got := rawRows[1][0]; got != string(PhysicalDRAMSubtransactionArrive) {
		t.Fatalf("event kind %q", got)
	}
	if got := rawRows[1][22]; got != "10" {
		t.Fatalf("raw mapped row %q, want 10", got)
	}

	summaryRows := readCSV(t, summaryPath)
	assertSummaryCount(t, summaryRows, "same_access_unit", "cycles", 1, 1)
	assertSummaryCount(t, summaryRows,
		"same_row_different_column", "cycles", 1, 1)
	assertSummaryCount(t, summaryRows,
		"same_bank_different_row", "cycles", 1, 1)
	assertSummaryCount(t, summaryRows,
		"different_bank_same_controller", "cycles", 1, 1)
}

func TestPhysicalDRAMSummaryBoundsShortWindowState(t *testing.T) {
	observer, err := NewPhysicalDRAMCSVObserver(
		"", filepath.Join(t.TempDir(), "summary.csv"))
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < 10000; i++ {
		e := physicalTestArrival(i*65, 0, 0, i, 0, "stream")
		e.ExternalAddress = i * 128
		observer.ObservePhysicalDRAM(e)
	}
	if got := len(observer.cycleLocality.lastByUnit); got > 2 {
		t.Fatalf("cycle-window unit state grew to %d entries", got)
	}
	if got := len(observer.cycleLocality.lastByRow); got > 2 {
		t.Fatalf("cycle-window row state grew to %d entries", got)
	}
	// At most 65 intervening-request positions plus the current request are
	// needed to answer the <=64 request-distance window.
	if got := len(observer.requestLocality.lastByUnit); got > 66 {
		t.Fatalf("request-window unit state grew to %d entries", got)
	}
	if got := len(observer.requestLocality.lastByRow); got > 66 {
		t.Fatalf("request-window row state grew to %d entries", got)
	}
	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPhysicalDRAMLocalityUsesIndependentDistanceCandidates(t *testing.T) {
	summaryPath := filepath.Join(t.TempDir(), "summary.csv")
	observer, err := NewPhysicalDRAMCSVObserver("", summaryPath)
	if err != nil {
		t.Fatal(err)
	}

	observer.ObservePhysicalDRAM(physicalTestArrival(
		0, 0, 0, 10, 0, "st-0"))
	for i := uint64(0); i < 10; i++ {
		e := physicalTestArrival(
			10+i*10, 0, 0, 100+i, 0, "intervening-"+strconv.FormatUint(i, 10))
		e.ExternalAddress = (1000 + i) * 128
		observer.ObservePhysicalDRAM(e)
	}
	observer.ObservePhysicalDRAM(physicalTestArrival(
		200, 0, 0, 10, 0, "st-1"))

	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, summaryPath)
	assertSummaryCount(t, rows, "same_access_unit", "cycles", 64, 0)
	assertSummaryCount(t, rows,
		"same_access_unit", "intervening_requests", 8, 0)
	assertSummaryCount(t, rows,
		"same_access_unit", "intervening_requests", 16, 1)
}

func TestPhysicalDRAMLocalityControllerInterleaveDoesNotExpireRequestState(
	t *testing.T,
) {
	summaryPath := filepath.Join(t.TempDir(), "summary.csv")
	observer, err := NewPhysicalDRAMCSVObserver("", summaryPath)
	if err != nil {
		t.Fatal(err)
	}

	observer.ObservePhysicalDRAM(physicalTestArrival(
		0, 0, 0, 10, 0, "st-0"))
	for i := uint64(0); i < 100; i++ {
		e := physicalTestArrival(
			1+i, 0, 0, 100+i, 0, "other-"+strconv.FormatUint(i, 10))
		e.Owner = "GPU[1]"
		e.ControllerName = "GPU[1].DRAM[0]"
		e.Controller = 1
		e.ExternalAddress = (1000 + i) * 128
		observer.ObservePhysicalDRAM(e)
	}
	observer.ObservePhysicalDRAM(physicalTestArrival(
		101, 0, 0, 10, 0, "st-1"))

	if err := observer.Close(); err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, summaryPath)
	assertSummaryCount(t, rows, "same_access_unit", "cycles", 64, 0)
	// Intervening requests are controller-local. The 100 reads at GPU[1]'s
	// controller neither advance GPU[0]'s sequence nor expire its candidate.
	assertSummaryCount(t, rows,
		"same_access_unit", "intervening_requests", 0, 1)
}

func physicalTestArrival(
	cycle, channel, bank, row, column uint64,
	subtransactionID string,
) PhysicalDRAMEvent {
	return PhysicalDRAMEvent{
		Kind:             PhysicalDRAMSubtransactionArrive,
		Owner:            "GPU[0]",
		ControllerName:   "GPU[0].DRAM[0]",
		Controller:       0,
		Cycle:            cycle,
		TimePS:           cycle * 2000,
		RequestID:        "req-" + subtransactionID,
		SubtransactionID: subtransactionID,
		IsRead:           true,
		PhysicalBytes:    128,
		ExternalAddress: func() uint64 {
			if subtransactionID == "st-1" {
				return 64
			}
			return column * 128
		}(),
		Channel:   channel,
		Rank:      0,
		BankGroup: 0,
		Bank:      bank,
		Row:       row,
		Column:    column,
	}
}

func readGzipCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	rows, err := csv.NewReader(gz).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	return rows
}

func assertSummaryCount(
	t *testing.T,
	rows [][]string,
	relation, distance string,
	window, want uint64,
) {
	t.Helper()
	for _, row := range rows[1:] {
		if row[0] != relation || row[1] != distance {
			continue
		}
		parsedWindow, err := strconv.ParseUint(row[3], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if parsedWindow != window {
			continue
		}
		got, err := strconv.ParseUint(row[4], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s/%s <= %d count %d, want %d",
				relation, distance, window, got, want)
		}
		return
	}
	t.Fatalf("missing summary row %s/%s/%d", relation, distance, window)
}
