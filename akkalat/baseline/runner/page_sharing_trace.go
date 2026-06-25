package runner

import (
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/sarchlab/akita/v3/mem/vm/addresstranslator"
)

type sharingTracePageKey struct {
	pid  uint32
	page uint64
}

type sharingTraceAccessCounter struct {
	total  uint64
	reads  uint64
	writes uint64
	bytes  uint64
}

type sharingTracePageStats struct {
	pid       uint32
	page      uint64
	pageVAddr uint64
	pagePAddr uint64
	pageBlock uint64
	pageSize  uint64

	firstCycle uint64
	lastCycle  uint64

	totalAccesses  uint64
	readAccesses   uint64
	writeAccesses  uint64
	totalBytes     uint64
	readBytes      uint64
	writeBytes     uint64
	localAccesses  uint64
	remoteAccesses uint64

	requesters map[uint64]*sharingTraceAccessCounter
	owners     map[uint64]uint64
}

type pageSharingTraceWriter struct {
	mu sync.Mutex

	path string
	file *os.File
	gzip *gzip.Writer
	csv  *csv.Writer

	sampleEvery uint64
	maxRecords  uint64
	log2Page    uint64
	gridWidth   int
	gridHeight  int

	seen        uint64
	written     uint64
	pages       map[sharingTracePageKey]struct{}
	pageStats   map[sharingTracePageKey]*sharingTracePageStats
	localPages  map[sharingTracePageKey]struct{}
	remotePages map[sharingTracePageKey]struct{}
	closed      bool
	err         error
}

func newPageSharingTraceWriter(
	path string,
	sampleEvery uint64,
	maxRecords uint64,
	log2Page uint64,
	gridWidth int,
	gridHeight int,
) (*pageSharingTraceWriter, error) {
	if sampleEvery == 0 {
		sampleEvery = 1
	}

	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	gzipWriter := gzip.NewWriter(file)
	csvWriter := csv.NewWriter(gzipWriter)
	writer := &pageSharingTraceWriter{
		path:        path,
		file:        file,
		gzip:        gzipWriter,
		csv:         csvWriter,
		sampleEvery: sampleEvery,
		maxRecords:  maxRecords,
		log2Page:    log2Page,
		gridWidth:   gridWidth,
		gridHeight:  gridHeight,
		pages:       make(map[sharingTracePageKey]struct{}),
		pageStats:   make(map[sharingTracePageKey]*sharingTracePageStats),
		localPages:  make(map[sharingTracePageKey]struct{}),
		remotePages: make(map[sharingTracePageKey]struct{}),
	}

	err = csvWriter.Write([]string{
		"cycle",
		"requester",
		"owner",
		"vaddr",
		"op",
		"distance",
		"pid",
		"page_id",
		"page_vaddr",
		"page_paddr",
		"page_block",
		"page_size",
		"paddr",
		"bytes",
		"is_write",
	})
	if err != nil {
		_ = writer.Close()
		return nil, err
	}

	return writer, nil
}

func (w *pageSharingTraceWriter) TraceSharingAccess(
	record addresstranslator.SharingAccess,
) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed || w.err != nil {
		return
	}

	w.seen++
	w.recordPage(record)
	if (w.seen-1)%w.sampleEvery != 0 {
		return
	}

	if w.maxRecords > 0 && w.written >= w.maxRecords {
		return
	}

	w.written++
	row := []string{
		strconv.FormatUint(cycle(float64(record.Time)), 10),
		strconv.FormatUint(record.RequesterID, 10),
		strconv.FormatUint(record.OwnerID, 10),
		strconv.FormatUint(record.VAddr, 10),
		record.AccessType,
		strconv.Itoa(w.distance(record.RequesterID, record.OwnerID)),
		strconv.FormatUint(uint64(record.PID), 10),
		strconv.FormatUint(w.pageID(record), 10),
		strconv.FormatUint(w.pageVAddr(record), 10),
		strconv.FormatUint(record.PagePAddr, 10),
		strconv.FormatUint(record.PageBlock, 10),
		strconv.FormatUint(record.PageSize, 10),
		strconv.FormatUint(record.PAddr, 10),
		strconv.FormatUint(record.Bytes, 10),
		strconv.FormatBool(record.AccessType == "W"),
	}

	if err := w.csv.Write(row); err != nil {
		w.err = err
		return
	}

	if w.written%4096 == 0 {
		w.csv.Flush()
		if err := w.csv.Error(); err != nil {
			w.err = err
			return
		}
		if err := w.gzip.Flush(); err != nil {
			w.err = err
		}
	}
}

func cycle(timeInSec float64) uint64 {
	return uint64(math.Round(timeInSec * 1e9))
}

func (w *pageSharingTraceWriter) recordPage(
	record addresstranslator.SharingAccess,
) {
	pageVAddr := w.pageVAddr(record)
	key := sharingTracePageKey{
		pid:  uint32(record.PID),
		page: pageVAddr >> w.log2Page,
	}

	w.pages[key] = struct{}{}
	stats := w.pageStats[key]
	if stats == nil {
		stats = &sharingTracePageStats{
			pid:        key.pid,
			page:       key.page,
			pageVAddr:  pageVAddr,
			pagePAddr:  record.PagePAddr,
			pageBlock:  record.PageBlock,
			pageSize:   record.PageSize,
			requesters: make(map[uint64]*sharingTraceAccessCounter),
			owners:     make(map[uint64]uint64),
		}
		w.pageStats[key] = stats
	}

	accessCycle := cycle(float64(record.Time))
	if stats.totalAccesses == 0 || accessCycle < stats.firstCycle {
		stats.firstCycle = accessCycle
	}
	if accessCycle > stats.lastCycle {
		stats.lastCycle = accessCycle
	}

	stats.totalAccesses++
	stats.totalBytes += record.Bytes
	requesterCounter := stats.requesters[record.RequesterID]
	if requesterCounter == nil {
		requesterCounter = &sharingTraceAccessCounter{}
		stats.requesters[record.RequesterID] = requesterCounter
	}
	requesterCounter.total++
	requesterCounter.bytes += record.Bytes
	stats.owners[record.OwnerID]++

	switch record.AccessType {
	case "W":
		stats.writeAccesses++
		stats.writeBytes += record.Bytes
		requesterCounter.writes++
	case "R":
		stats.readAccesses++
		stats.readBytes += record.Bytes
		requesterCounter.reads++
	}

	if record.RequesterID == record.OwnerID {
		w.localPages[key] = struct{}{}
		stats.localAccesses++
	} else {
		w.remotePages[key] = struct{}{}
		stats.remoteAccesses++
	}
}

func (w *pageSharingTraceWriter) pageVAddr(
	record addresstranslator.SharingAccess,
) uint64 {
	if record.PageVAddr != 0 {
		return record.PageVAddr
	}
	return (record.VAddr >> w.log2Page) << w.log2Page
}

func (w *pageSharingTraceWriter) pageID(
	record addresstranslator.SharingAccess,
) uint64 {
	return w.pageVAddr(record) >> w.log2Page
}

func (w *pageSharingTraceWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return w.err
	}

	w.csv.Flush()
	if err := w.csv.Error(); err != nil && w.err == nil {
		w.err = err
	}
	if err := w.gzip.Close(); err != nil && w.err == nil {
		w.err = err
	}
	if err := w.file.Close(); err != nil && w.err == nil {
		w.err = err
	}
	if err := w.writePageSummaryLocked(); err != nil && w.err == nil {
		w.err = err
	}

	w.closed = true
	fmt.Printf(
		"Sharing trace %s: observed_accesses=%d written_records=%d unique_pages=%d local_pages=%d remote_pages=%d page_size=%d page_summary=%s\n",
		w.path,
		w.seen,
		w.written,
		len(w.pages),
		len(w.localPages),
		len(w.remotePages),
		uint64(1)<<w.log2Page,
		sharingPageSummaryPath(w.path),
	)
	return w.err
}

func (w *pageSharingTraceWriter) writePageSummaryLocked() error {
	path := sharingPageSummaryPath(w.path)
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	if err := writer.Write([]string{
		"pid",
		"page_id",
		"page_vaddr",
		"page_paddr",
		"page_block",
		"page_size",
		"first_cycle",
		"last_cycle",
		"reuse_window_cycles",
		"total_accesses",
		"read_accesses",
		"write_accesses",
		"read_ratio_pct",
		"total_bytes",
		"read_bytes",
		"write_bytes",
		"local_accesses",
		"remote_accesses",
		"remote_access_ratio_pct",
		"sharer_count",
		"remote_sharer_count",
		"owner_count",
		"dominant_owner",
		"dominant_owner_accesses",
		"dominant_requester",
		"dominant_requester_accesses",
		"requester_counts",
		"owner_counts",
	}); err != nil {
		return err
	}

	keys := make([]sharingTracePageKey, 0, len(w.pageStats))
	for key := range w.pageStats {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].pid != keys[j].pid {
			return keys[i].pid < keys[j].pid
		}
		return keys[i].page < keys[j].page
	})

	for _, key := range keys {
		stats := w.pageStats[key]
		dominantOwner, dominantOwnerAccesses := dominantOwner(stats.owners)
		dominantRequester, dominantRequesterAccesses :=
			dominantRequester(stats.requesters)
		reuseWindow := uint64(0)
		if stats.lastCycle >= stats.firstCycle {
			reuseWindow = stats.lastCycle - stats.firstCycle
		}

		row := []string{
			strconv.FormatUint(uint64(stats.pid), 10),
			strconv.FormatUint(stats.page, 10),
			strconv.FormatUint(stats.pageVAddr, 10),
			strconv.FormatUint(stats.pagePAddr, 10),
			strconv.FormatUint(stats.pageBlock, 10),
			strconv.FormatUint(stats.pageSize, 10),
			strconv.FormatUint(stats.firstCycle, 10),
			strconv.FormatUint(stats.lastCycle, 10),
			strconv.FormatUint(reuseWindow, 10),
			strconv.FormatUint(stats.totalAccesses, 10),
			strconv.FormatUint(stats.readAccesses, 10),
			strconv.FormatUint(stats.writeAccesses, 10),
			formatPct(stats.readAccesses, stats.totalAccesses),
			strconv.FormatUint(stats.totalBytes, 10),
			strconv.FormatUint(stats.readBytes, 10),
			strconv.FormatUint(stats.writeBytes, 10),
			strconv.FormatUint(stats.localAccesses, 10),
			strconv.FormatUint(stats.remoteAccesses, 10),
			formatPct(stats.remoteAccesses, stats.totalAccesses),
			strconv.Itoa(len(stats.requesters)),
			strconv.Itoa(remoteRequesterCount(stats)),
			strconv.Itoa(len(stats.owners)),
			strconv.FormatUint(dominantOwner, 10),
			strconv.FormatUint(dominantOwnerAccesses, 10),
			strconv.FormatUint(dominantRequester, 10),
			strconv.FormatUint(dominantRequesterAccesses, 10),
			formatRequesterCounts(stats.requesters),
			formatOwnerCounts(stats.owners),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	return writer.Error()
}

func sharingPageSummaryPath(tracePath string) string {
	switch {
	case strings.HasSuffix(tracePath, ".csv.gz"):
		return strings.TrimSuffix(tracePath, ".csv.gz") + "_pages.csv"
	case strings.HasSuffix(tracePath, ".gz"):
		return strings.TrimSuffix(tracePath, ".gz") + "_pages.csv"
	default:
		return tracePath + "_pages.csv"
	}
}

func formatPct(numerator, denominator uint64) string {
	if denominator == 0 {
		return "0"
	}
	return strconv.FormatFloat(
		100*float64(numerator)/float64(denominator), 'f', 6, 64)
}

func dominantOwner(counts map[uint64]uint64) (uint64, uint64) {
	var bestGPU uint64
	var bestCount uint64
	for gpu, count := range counts {
		if count > bestCount || (count == bestCount && gpu < bestGPU) {
			bestGPU = gpu
			bestCount = count
		}
	}
	return bestGPU, bestCount
}

func dominantRequester(
	counts map[uint64]*sharingTraceAccessCounter,
) (uint64, uint64) {
	var bestGPU uint64
	var bestCount uint64
	for gpu, counter := range counts {
		if counter.total > bestCount ||
			(counter.total == bestCount && gpu < bestGPU) {
			bestGPU = gpu
			bestCount = counter.total
		}
	}
	return bestGPU, bestCount
}

func remoteRequesterCount(stats *sharingTracePageStats) int {
	count := 0
	for requester := range stats.requesters {
		if stats.owners[requester] == 0 {
			count++
			continue
		}
		// A requester can also be an owner in migration experiments. Count it
		// as remote when at least one access to the page was remote.
		if requester != mostCommonOwner(stats.owners) {
			count++
		}
	}
	return count
}

func mostCommonOwner(counts map[uint64]uint64) uint64 {
	owner, _ := dominantOwner(counts)
	return owner
}

func formatRequesterCounts(
	counts map[uint64]*sharingTraceAccessCounter,
) string {
	gpus := make([]int, 0, len(counts))
	for gpu := range counts {
		gpus = append(gpus, int(gpu))
	}
	sort.Ints(gpus)

	parts := make([]string, 0, len(gpus))
	for _, gpu := range gpus {
		counter := counts[uint64(gpu)]
		parts = append(parts, fmt.Sprintf(
			"%d:%d:%d:%d:%d",
			gpu,
			counter.total,
			counter.reads,
			counter.writes,
			counter.bytes,
		))
	}
	return strings.Join(parts, ";")
}

func formatOwnerCounts(counts map[uint64]uint64) string {
	gpus := make([]int, 0, len(counts))
	for gpu := range counts {
		gpus = append(gpus, int(gpu))
	}
	sort.Ints(gpus)

	parts := make([]string, 0, len(gpus))
	for _, gpu := range gpus {
		parts = append(parts, fmt.Sprintf(
			"%d:%d",
			gpu,
			counts[uint64(gpu)],
		))
	}
	return strings.Join(parts, ";")
}

func (w *pageSharingTraceWriter) distance(requester, owner uint64) int {
	if requester == owner {
		return 0
	}

	reqX, reqY, okReq := w.deviceCoord(requester)
	ownX, ownY, okOwner := w.deviceCoord(owner)
	if !okReq || !okOwner {
		return -1
	}

	dx := reqX - ownX
	if dx < 0 {
		dx = -dx
	}
	dy := reqY - ownY
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}

func (w *pageSharingTraceWriter) deviceCoord(deviceID uint64) (int, int, bool) {
	if deviceID == 0 || w.gridWidth <= 0 || w.gridHeight <= 0 {
		return 0, 0, false
	}

	remaining := int(deviceID) - 1
	centerX := w.gridWidth / 2
	centerY := w.gridHeight / 2
	for y := 0; y < w.gridHeight; y++ {
		for x := 0; x < w.gridWidth; x++ {
			if x == centerX && y == centerY {
				continue
			}
			if remaining == 0 {
				return x, y, true
			}
			remaining--
		}
	}

	return 0, 0, false
}

func (r *Runner) closeSharingTrace() {
	if r.sharingTraceWriter == nil {
		return
	}

	if err := r.sharingTraceWriter.Close(); err != nil {
		panic(err)
	}
}
