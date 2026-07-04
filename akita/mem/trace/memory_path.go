package trace

import (
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/sarchlab/akita/v3/sim"
)

const (
	defaultMemoryPathPrefix = "memory_path"

	memoryPathRawHeader = "sequence,completion_time_ns,original_req_id,translated_req_id,translation_req_id,translation_task_id,access_type,pid,requester_gpm,owner_gpm,hops,is_remote,has_vaddr,vaddr,has_paddr,paddr,page_paddr,bytes,l1v_cache_component,l1v_cache_result,l1v_cache_latency_ns,l1v_tlb_component,l1v_tlb_result,l1v_tlb_latency_ns,l2tlb_component,l2tlb_result,l2tlb_latency_ns,l2_cache_component,l2_cache_result,l2_cache_latency_ns,source,data_source_latency_ns,remote_gpm_latency_ns"
)

type memoryPathRecord struct {
	sequence uint64
	traceSeq uint64

	originalReqID     string
	translatedReqID   string
	translationReqID  string
	translationTaskID string

	accessType string
	pid        uint64
	bytes      uint64

	requesterGPM int
	providerGPM  int
	hops         int
	isRemote     bool

	hasVAddr bool
	vaddr    uint64
	hasPAddr bool
	paddr    uint64

	l1vCacheComponent string
	l1vCacheResult    string
	l1vCacheStartNS   uint64
	l1vCacheLatencyNS uint64

	l2CacheComponent string
	l2CacheResult    string
	l2CacheStartNS   uint64
	l2CacheLatencyNS uint64

	l1vTLBComponent string
	l1vTLBResult    string
	l1vTLBStartNS   uint64
	l1vTLBLatencyNS uint64

	l2TLBComponent string
	l2TLBResult    string
	l2TLBStartNS   uint64
	l2TLBLatencyNS uint64

	source              string
	dataSourceLatencyNS uint64
	remoteGPMLatencyNS  uint64

	completionTimeNS uint64
	counted          bool

	parentCount       int
	route             string
	finalSource       string
	l1vPathHops       []l1vPathHop
	l1vPathStageSumNS map[string]uint64
	l1vPathStageCnt   map[string]uint64
	atToL1VMinNS      uint64
	atToL1VMaxNS      uint64
	atToL1VSumNS      uint64
	atToL1VCnt        uint64

	l1vCoalesceStartNS  uint64
	l1vCoalesceEmitNS   uint64
	l1vDirStartNS       uint64
	l1vDirResultNS      uint64
	l1vMSHRStartNS      uint64
	l1vBottomResponseNS uint64

	l2DirStartNS     uint64
	l2DirResultNS    uint64
	l2WriteBufferNS  uint64
	l2DramReturnNS   uint64
	l2MSHRStartNS    uint64
	l2ResponseSendNS uint64
	dramReceiveNS    uint64
}

type jointCounter struct {
	total       uint64
	tlbMiss     uint64
	tlbNotHit   uint64
	cacheMiss   uint64
	cacheNotHit uint64
	strictJoint uint64
	notHitJoint uint64
}

type stageCounter struct {
	accesses uint64
	sumNS    uint64
}

type memoryPathRawRow struct {
	sequence uint64
	row      []string
}

type memoryPathAggregate struct {
	total      uint64
	remote     uint64
	sourceHits map[string]uint64
	joint      map[string]*jointCounter
	stages     map[string]*stageCounter
}

type memoryPathStats struct {
	sync.Mutex

	enabled bool
	prefix  string

	warmupAccesses uint64
	maxRecords     uint64
	log2Page       uint64
	tileWidth      int
	remoteOnly     bool
	streaming      bool
	exitOnRawFull  bool

	observed uint64
	matched  uint64
	written  uint64

	file *os.File
	gzip *gzip.Writer
	csv  *csv.Writer
	raw  []memoryPathRawRow

	l1vHopsFile    *os.File
	l1vHopsGzip    *gzip.Writer
	l1vHopsCSV     *csv.Writer
	l1vSummaryFile *os.File
	l1vSummaryCSV  *csv.Writer
	cpFile         *os.File
	cpCSV          *csv.Writer
	streamErr      error

	records                   map[string]*memoryPathRecord
	translationReqToOriginal  map[string]string
	translationTaskToOriginal map[string]string
	translatedReqToOriginal   map[string]string
	networkMsgToOriginal      map[string]string
	networkMsgDirection       map[string]string

	full   memoryPathAggregate
	steady memoryPathAggregate

	l1vPathStageAggregates map[string]*l1vPathStageAggregate
	criticalPathAggregate  criticalPathAggregate

	stopped      bool
	doneNotified bool
	doneCallback func()
}

var globalMemoryPathStats = newMemoryPathStats()

func newMemoryPathStats() *memoryPathStats {
	s := &memoryPathStats{
		tileWidth: defaultL2SourceTileWidth,
		log2Page:  12,
	}
	s.resetMapsLocked()
	return s
}

func newMemoryPathAggregate() memoryPathAggregate {
	return memoryPathAggregate{
		sourceHits: make(map[string]uint64),
		joint:      make(map[string]*jointCounter),
		stages:     make(map[string]*stageCounter),
	}
}

func (s *memoryPathStats) resetMapsLocked() {
	s.records = make(map[string]*memoryPathRecord)
	s.translationReqToOriginal = make(map[string]string)
	s.translationTaskToOriginal = make(map[string]string)
	s.translatedReqToOriginal = make(map[string]string)
	s.networkMsgToOriginal = make(map[string]string)
	s.networkMsgDirection = make(map[string]string)
	s.full = newMemoryPathAggregate()
	s.steady = newMemoryPathAggregate()
	s.observed = 0
	s.matched = 0
	s.written = 0
	s.raw = nil
	s.streamErr = nil
	s.l1vPathStageAggregates = make(map[string]*l1vPathStageAggregate)
	s.criticalPathAggregate = newCriticalPathAggregate()
	s.stopped = false
	s.doneNotified = false
}

// EnableMemoryPathTrace enables request-level memory-path tracing.
func EnableMemoryPathTrace(
	prefix string,
	warmupAccesses uint64,
	maxRecords uint64,
	log2Page uint64,
	tileWidth int,
	remoteOnly bool,
	streaming bool,
	exitOnRawFull bool,
	doneCallback func(),
) error {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if err := globalMemoryPathStats.closeLocked(); err != nil {
		return err
	}

	if prefix == "" {
		prefix = defaultMemoryPathPrefix
	}
	if log2Page == 0 {
		log2Page = 12
	}
	if tileWidth <= 0 {
		tileWidth = defaultL2SourceTileWidth
	}

	if dir := filepath.Dir(prefix + "_raw.csv.gz"); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	globalMemoryPathStats.enabled = true
	globalMemoryPathStats.prefix = prefix
	globalMemoryPathStats.warmupAccesses = warmupAccesses
	globalMemoryPathStats.maxRecords = maxRecords
	globalMemoryPathStats.log2Page = log2Page
	globalMemoryPathStats.tileWidth = tileWidth
	globalMemoryPathStats.remoteOnly = remoteOnly
	globalMemoryPathStats.streaming = streaming
	globalMemoryPathStats.exitOnRawFull = exitOnRawFull
	globalMemoryPathStats.doneCallback = doneCallback
	globalMemoryPathStats.resetMapsLocked()
	if streaming {
		if err := globalMemoryPathStats.openStreamingLocked(); err != nil {
			_ = globalMemoryPathStats.closeLocked()
			globalMemoryPathStats.enabled = false
			globalMemoryPathStats.prefix = ""
			globalMemoryPathStats.streaming = false
			globalMemoryPathStats.resetMapsLocked()
			return err
		}
	}
	return nil
}

// DisableMemoryPathTrace disables request-level memory-path tracing.
func DisableMemoryPathTrace() {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	_ = globalMemoryPathStats.closeLocked()
	globalMemoryPathStats.enabled = false
	globalMemoryPathStats.prefix = ""
	globalMemoryPathStats.remoteOnly = false
	globalMemoryPathStats.streaming = false
	globalMemoryPathStats.exitOnRawFull = false
	globalMemoryPathStats.doneCallback = nil
	globalMemoryPathStats.resetMapsLocked()
}

// MemoryPathTraceEnabled reports whether request-level tracing is active.
func MemoryPathTraceEnabled() bool {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	return globalMemoryPathStats.collectingLocked()
}

// DumpMemoryPathTrace writes memory-path aggregate CSV files.
func DumpMemoryPathTrace() error {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.enabled {
		return nil
	}

	if err := globalMemoryPathStats.closeLocked(); err != nil {
		return err
	}
	if err := globalMemoryPathStats.dumpRawLocked(); err != nil {
		return err
	}
	if err := globalMemoryPathStats.dumpSummaryLocked(); err != nil {
		return err
	}
	if err := globalMemoryPathStats.dumpJointMissLocked(); err != nil {
		return err
	}
	if err := globalMemoryPathStats.dumpStageLatencyLocked(); err != nil {
		return err
	}
	if err := globalMemoryPathStats.dumpL1VPathTraceLocked(); err != nil {
		return err
	}
	return globalMemoryPathStats.dumpCriticalPathTraceLocked()
}

func (s *memoryPathStats) closeLocked() error {
	var err error
	if s.csv != nil {
		s.csv.Flush()
		if csvErr := s.csv.Error(); csvErr != nil {
			err = csvErr
		}
		s.csv = nil
	}
	if s.gzip != nil {
		if gzipErr := s.gzip.Close(); err == nil && gzipErr != nil {
			err = gzipErr
		}
		s.gzip = nil
	}
	if s.file != nil {
		if fileErr := s.file.Close(); err == nil && fileErr != nil {
			err = fileErr
		}
		s.file = nil
	}
	if s.l1vHopsCSV != nil {
		s.l1vHopsCSV.Flush()
		if csvErr := s.l1vHopsCSV.Error(); err == nil && csvErr != nil {
			err = csvErr
		}
		s.l1vHopsCSV = nil
	}
	if s.l1vHopsGzip != nil {
		if gzipErr := s.l1vHopsGzip.Close(); err == nil && gzipErr != nil {
			err = gzipErr
		}
		s.l1vHopsGzip = nil
	}
	if s.l1vHopsFile != nil {
		if fileErr := s.l1vHopsFile.Close(); err == nil && fileErr != nil {
			err = fileErr
		}
		s.l1vHopsFile = nil
	}
	if s.l1vSummaryCSV != nil {
		s.l1vSummaryCSV.Flush()
		if csvErr := s.l1vSummaryCSV.Error(); err == nil && csvErr != nil {
			err = csvErr
		}
		s.l1vSummaryCSV = nil
	}
	if s.l1vSummaryFile != nil {
		if fileErr := s.l1vSummaryFile.Close(); err == nil && fileErr != nil {
			err = fileErr
		}
		s.l1vSummaryFile = nil
	}
	if s.cpCSV != nil {
		s.cpCSV.Flush()
		if csvErr := s.cpCSV.Error(); err == nil && csvErr != nil {
			err = csvErr
		}
		s.cpCSV = nil
	}
	if s.cpFile != nil {
		if fileErr := s.cpFile.Close(); err == nil && fileErr != nil {
			err = fileErr
		}
		s.cpFile = nil
	}
	if err == nil && s.streamErr != nil {
		err = s.streamErr
	}
	return err
}

func (s *memoryPathStats) openStreamingLocked() error {
	if err := s.openRawStreamLocked(); err != nil {
		return err
	}
	if err := s.openL1VPathStreamLocked(); err != nil {
		return err
	}
	if err := s.openCriticalPathStreamLocked(); err != nil {
		return err
	}
	return nil
}

func (s *memoryPathStats) openRawStreamLocked() error {
	path := s.prefix + "_raw.csv.gz"
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	s.file = file
	s.gzip = gzip.NewWriter(file)
	s.csv = csv.NewWriter(s.gzip)
	return s.csv.Write(strings.Split(memoryPathRawHeader, ","))
}

func (s *memoryPathStats) dumpRawLocked() error {
	if s.streaming {
		return s.streamErr
	}
	path := s.prefix + "_raw.csv.gz"
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()

	csvWriter := csv.NewWriter(gzipWriter)
	defer csvWriter.Flush()

	if err := csvWriter.Write(strings.Split(memoryPathRawHeader, ",")); err != nil {
		return err
	}

	sort.SliceStable(s.raw, func(i, j int) bool {
		return s.raw[i].sequence < s.raw[j].sequence
	})
	for _, raw := range s.raw {
		if err := csvWriter.Write(raw.row); err != nil {
			return err
		}
	}
	return csvWriter.Error()
}

// RecordMemoryPathCacheStart records when a memory request enters a cache.
func RecordMemoryPathCacheStart(
	cacheName string,
	reqID string,
	info interface{},
	address uint64,
	bytes uint64,
	pid uint64,
	op string,
	now sim.VTimeInSec,
) {
	tier := memoryPathCacheTier(cacheName)
	if tier == "" {
		return
	}

	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}

	rec := globalMemoryPathStats.recordForCacheLocked(
		tier, reqID, info, address, bytes, pid, op)
	if rec == nil {
		return
	}

	nowNS := timeToNS(now)
	switch tier {
	case "l1v":
		rec.l1vCacheComponent = cacheName
		if rec.l1vCacheStartNS == 0 {
			rec.l1vCacheStartNS = nowNS
		}
	case "l2":
		rec.l2CacheComponent = cacheName
		if rec.l2CacheStartNS == 0 {
			rec.l2CacheStartNS = nowNS
		}
	}
}

// RecordMemoryPathCacheResult records hit/miss/MSHR-hit at a cache directory.
func RecordMemoryPathCacheResult(
	cacheName string,
	reqID string,
	info interface{},
	address uint64,
	bytes uint64,
	pid uint64,
	op string,
	result string,
	now sim.VTimeInSec,
) {
	tier := memoryPathCacheTier(cacheName)
	if tier == "" {
		return
	}

	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}

	rec := globalMemoryPathStats.recordForCacheLocked(
		tier, reqID, info, address, bytes, pid, op)
	if rec == nil {
		return
	}

	normalized := normalizeMemoryPathResult(result)
	switch tier {
	case "l1v":
		rec.l1vCacheComponent = cacheName
		rec.l1vCacheResult = normalized
	case "l2":
		rec.l2CacheComponent = cacheName
		rec.l2CacheResult = normalized
	}
	globalMemoryPathStats.noteCacheResultLocked(
		tier, cacheName, rec, normalized, timeToNS(now))
}

// RecordMemoryPathCacheComplete records end-to-end cache service latency.
func RecordMemoryPathCacheComplete(
	cacheName string,
	reqID string,
	info interface{},
	address uint64,
	bytes uint64,
	pid uint64,
	op string,
	now sim.VTimeInSec,
) {
	tier := memoryPathCacheTier(cacheName)
	if tier == "" {
		return
	}

	globalMemoryPathStats.Lock()

	if !globalMemoryPathStats.collectingLocked() {
		globalMemoryPathStats.Unlock()
		return
	}

	rec := globalMemoryPathStats.recordForCacheLocked(
		tier, reqID, info, address, bytes, pid, op)
	if rec == nil {
		globalMemoryPathStats.Unlock()
		return
	}

	var doneCallback func()
	nowNS := timeToNS(now)
	switch tier {
	case "l1v":
		rec.l1vCacheComponent = cacheName
		if rec.l1vCacheStartNS > 0 && nowNS >= rec.l1vCacheStartNS {
			rec.l1vCacheLatencyNS = nowNS - rec.l1vCacheStartNS
		}
		globalMemoryPathStats.noteCacheCompleteLocked(
			tier, cacheName, rec, nowNS)
		rec.completionTimeNS = nowNS
		if globalMemoryPathStats.completeRecordLocked(rec) {
			doneCallback = globalMemoryPathStats.doneCallback
		}
	case "l2":
		rec.l2CacheComponent = cacheName
		if rec.l2CacheStartNS > 0 && nowNS >= rec.l2CacheStartNS {
			rec.l2CacheLatencyNS = nowNS - rec.l2CacheStartNS
		}
		globalMemoryPathStats.noteCacheCompleteLocked(
			tier, cacheName, rec, nowNS)
	}
	globalMemoryPathStats.Unlock()

	if doneCallback != nil {
		go doneCallback()
	}
}

// RecordMemoryPathTranslationStart links the original request to a translation task.
func RecordMemoryPathTranslationStart(
	translatorName string,
	originalReqID string,
	translationReqID string,
	translationTaskID string,
	vaddr uint64,
	pid uint64,
	bytes uint64,
	op string,
	now sim.VTimeInSec,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}

	rec := globalMemoryPathStats.recordByOriginalLocked(originalReqID)
	if rec == nil {
		return
	}
	rec.translationReqID = translationReqID
	rec.translationTaskID = translationTaskID
	rec.hasVAddr = true
	rec.vaddr = vaddr
	rec.pid = pid
	rec.bytes = bytes
	rec.accessType = normalizeOp(op)
	globalMemoryPathStats.translationReqToOriginal[translationReqID] = originalReqID
	globalMemoryPathStats.translationTaskToOriginal[translationTaskID] = originalReqID
	_ = translatorName
	_ = now
}

// RecordMemoryPathTranslatedReq links the translated L2 request to the original request.
func RecordMemoryPathTranslatedReq(
	originalReqID string,
	translatedReqID string,
	translationReqID string,
	translationTaskID string,
	paddr uint64,
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}

	rec := globalMemoryPathStats.recordByOriginalLocked(originalReqID)
	if rec == nil {
		return
	}
	rec.translatedReqID = translatedReqID
	rec.translationReqID = translationReqID
	rec.translationTaskID = translationTaskID
	rec.hasPAddr = true
	rec.paddr = paddr
	globalMemoryPathStats.translatedReqToOriginal[translatedReqID] = originalReqID
	if translationReqID != "" {
		globalMemoryPathStats.translationReqToOriginal[translationReqID] = originalReqID
	}
	if translationTaskID != "" {
		globalMemoryPathStats.translationTaskToOriginal[translationTaskID] = originalReqID
	}
}

// RecordMemoryPathCoalescedParents folds TLB outcomes from translated parent
// requests into a coalesced cacheline access record.
func RecordMemoryPathCoalescedParents(
	coalescedReqID string,
	parentInfos []interface{},
) {
	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}

	rec := globalMemoryPathStats.recordByOriginalLocked(coalescedReqID)
	if rec == nil {
		return
	}

	for _, info := range parentInfos {
		accessInfo, ok := GetL2AccessInfo(info)
		if !ok || accessInfo.OriginalReqID == "" {
			continue
		}
		parent := globalMemoryPathStats.records[accessInfo.OriginalReqID]
		if parent == nil {
			continue
		}
		rec.l1vTLBResult = worstResult(rec.l1vTLBResult, parent.l1vTLBResult)
		rec.l2TLBResult = worstResult(rec.l2TLBResult, parent.l2TLBResult)
		if rec.l1vTLBComponent == "" {
			rec.l1vTLBComponent = parent.l1vTLBComponent
		}
		if rec.l2TLBComponent == "" {
			rec.l2TLBComponent = parent.l2TLBComponent
		}
		if parent.l1vTLBLatencyNS > rec.l1vTLBLatencyNS {
			rec.l1vTLBLatencyNS = parent.l1vTLBLatencyNS
		}
		if parent.l2TLBLatencyNS > rec.l2TLBLatencyNS {
			rec.l2TLBLatencyNS = parent.l2TLBLatencyNS
		}
		if rec.translationReqID == "" {
			rec.translationReqID = parent.translationReqID
		}
		if rec.translationTaskID == "" {
			rec.translationTaskID = parent.translationTaskID
		}
		if rec.translatedReqID == "" {
			rec.translatedReqID = accessInfo.TranslatedReqID
		}
	}
}

// RecordMemoryPathTLBResult records a TLB hit/miss/MSHR-hit result.
func RecordMemoryPathTLBResult(
	tlbName string,
	translationTaskID string,
	translationReqID string,
	result string,
	start sim.VTimeInSec,
	now sim.VTimeInSec,
) {
	tier := memoryPathTLBTier(tlbName)
	if tier == "" {
		return
	}

	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}

	rec := globalMemoryPathStats.recordForTranslationLocked(
		translationTaskID, translationReqID)
	if rec == nil {
		return
	}

	startNS := timeToNS(start)
	switch tier {
	case "l1v_tlb":
		rec.l1vTLBComponent = tlbName
		rec.l1vTLBResult = normalizeMemoryPathResult(result)
		if rec.l1vTLBStartNS == 0 {
			rec.l1vTLBStartNS = startNS
		}
	case "l2_tlb":
		rec.l2TLBComponent = tlbName
		rec.l2TLBResult = normalizeMemoryPathResult(result)
		if rec.l2TLBStartNS == 0 {
			rec.l2TLBStartNS = startNS
		}
	}
	_ = now
}

// RecordMemoryPathTLBComplete records TLB request completion latency.
func RecordMemoryPathTLBComplete(
	tlbName string,
	translationTaskID string,
	translationReqID string,
	start sim.VTimeInSec,
	now sim.VTimeInSec,
) {
	tier := memoryPathTLBTier(tlbName)
	if tier == "" {
		return
	}

	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}

	rec := globalMemoryPathStats.recordForTranslationLocked(
		translationTaskID, translationReqID)
	if rec == nil {
		return
	}

	startNS := timeToNS(start)
	nowNS := timeToNS(now)
	if startNS == 0 || nowNS < startNS {
		return
	}

	switch tier {
	case "l1v_tlb":
		rec.l1vTLBComponent = tlbName
		rec.l1vTLBLatencyNS = nowNS - startNS
	case "l2_tlb":
		rec.l2TLBComponent = tlbName
		rec.l2TLBLatencyNS = nowNS - startNS
	}
}

// RecordMemoryPathDataSource records the L2/data source that served a request.
func RecordMemoryPathDataSource(
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

	accessInfo, _ := GetL2AccessInfo(info)
	if accessInfo.OriginalReqID == "" {
		return
	}

	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}

	rec := globalMemoryPathStats.recordByOriginalLocked(accessInfo.OriginalReqID)
	if rec == nil {
		return
	}

	requesterGPM, providerGPM := requesterProviderGPM(cacheName, accessInfo)
	rec.requesterGPM = requesterGPM
	rec.providerGPM = providerGPM
	rec.hops = manhattanHops(requesterGPM, providerGPM, globalMemoryPathStats.tileWidth)
	rec.isRemote = accessInfo.IsRemote
	if accessInfo.IsRemote {
		rec.route = "remote"
	} else if rec.route == "" {
		rec.route = "local"
	}
	rec.source = qualifiedSource(accessInfo.IsRemote, normalizeSourceBase(sourceBase))
	rec.finalSource = rec.source
	rec.dataSourceLatencyNS = timeToNS(latency)
	rec.bytes = bytes
	rec.accessType = normalizeOp(op)
	rec.hasPAddr = true
	rec.paddr = address
	if vaddr, hasVAddr := vaddrForAddress(accessInfo, address); hasVAddr {
		rec.hasVAddr = true
		rec.vaddr = vaddr
	}
	if accessInfo.TranslatedReqID != "" {
		rec.translatedReqID = accessInfo.TranslatedReqID
	}
	if accessInfo.TranslationReqID != "" {
		rec.translationReqID = accessInfo.TranslationReqID
	}
	if accessInfo.TranslationTaskID != "" {
		rec.translationTaskID = accessInfo.TranslationTaskID
	}
	_ = receiveTime
}

// RecordMemoryPathRemoteGPM records requester-side RDMA latency for a remote access.
func RecordMemoryPathRemoteGPM(
	info interface{},
	requesterName string,
	providerName string,
	bytes uint64,
	latency sim.VTimeInSec,
	receiveTime sim.VTimeInSec,
	op string,
) {
	if bytes == 0 {
		return
	}

	accessInfo, _ := GetL2AccessInfo(info)
	if accessInfo.OriginalReqID == "" {
		return
	}

	globalMemoryPathStats.Lock()
	defer globalMemoryPathStats.Unlock()

	if !globalMemoryPathStats.collectingLocked() {
		return
	}

	rec := globalMemoryPathStats.recordByOriginalLocked(accessInfo.OriginalReqID)
	if rec == nil {
		return
	}

	requesterGPM := parseGPMID(requesterName)
	providerGPM := parseGPMID(providerName)
	rec.requesterGPM = requesterGPM
	rec.providerGPM = providerGPM
	rec.hops = manhattanHops(requesterGPM, providerGPM, globalMemoryPathStats.tileWidth)
	rec.isRemote = true
	rec.route = "remote"
	rec.remoteGPMLatencyNS = timeToNS(latency)
	rec.bytes = bytes
	rec.accessType = normalizeOp(op)
	if rec.source == "" {
		rec.source = "remote_gpm"
	}
	if rec.finalSource == "" {
		rec.finalSource = rec.source
	}
	_ = receiveTime
}

func (s *memoryPathStats) recordForCacheLocked(
	tier string,
	reqID string,
	info interface{},
	address uint64,
	bytes uint64,
	pid uint64,
	op string,
) *memoryPathRecord {
	switch tier {
	case "l1v":
		rec := s.recordByOriginalLocked(reqID)
		if rec == nil {
			return nil
		}
		if rec.sequence == 0 {
			s.observed++
			rec.sequence = s.observed
		}
		rec.originalReqID = reqID
		rec.hasVAddr = true
		rec.vaddr = address
		rec.pid = pid
		rec.bytes = bytes
		rec.accessType = normalizeOp(op)
		if requesterGPM := parseGPMIDFromInfoOrName(info, ""); requesterGPM >= 0 {
			rec.requesterGPM = requesterGPM
		}
		return rec
	case "l2":
		originalID := s.originalIDFromInfoOrReqLocked(info, reqID)
		if originalID == "" {
			return nil
		}
		rec := s.recordByOriginalLocked(originalID)
		if rec == nil {
			return nil
		}
		if accessInfo, ok := GetL2AccessInfo(info); ok {
			s.applyAccessInfoLocked(rec, accessInfo)
		}
		rec.hasPAddr = true
		rec.paddr = address
		if bytes > 0 {
			rec.bytes = bytes
		}
		rec.accessType = normalizeOp(op)
		return rec
	default:
		return nil
	}
}

func (s *memoryPathStats) recordByOriginalLocked(id string) *memoryPathRecord {
	if id == "" {
		return nil
	}
	rec := s.records[id]
	if rec == nil {
		rec = &memoryPathRecord{
			originalReqID: id,
			requesterGPM:  -1,
			providerGPM:   -1,
			hops:          -1,
		}
		s.records[id] = rec
	}
	return rec
}

func (s *memoryPathStats) recordForTranslationLocked(
	taskID string,
	reqID string,
) *memoryPathRecord {
	originalID := ""
	if taskID != "" {
		originalID = s.translationTaskToOriginal[taskID]
	}
	if originalID == "" && reqID != "" {
		originalID = s.translationReqToOriginal[reqID]
	}
	if originalID == "" {
		return nil
	}
	return s.recordByOriginalLocked(originalID)
}

func (s *memoryPathStats) originalIDFromInfoOrReqLocked(
	info interface{},
	reqID string,
) string {
	if accessInfo, ok := GetL2AccessInfo(info); ok {
		if accessInfo.OriginalReqID != "" {
			return accessInfo.OriginalReqID
		}
		if accessInfo.TranslatedReqID != "" {
			if originalID := s.translatedReqToOriginal[accessInfo.TranslatedReqID]; originalID != "" {
				return originalID
			}
		}
	}
	if originalID := s.translatedReqToOriginal[reqID]; originalID != "" {
		return originalID
	}
	return ""
}

func (s *memoryPathStats) applyAccessInfoLocked(
	rec *memoryPathRecord,
	info L2AccessInfo,
) {
	if info.OriginalReqID != "" {
		rec.originalReqID = info.OriginalReqID
	}
	if info.TranslatedReqID != "" {
		rec.translatedReqID = info.TranslatedReqID
		s.translatedReqToOriginal[info.TranslatedReqID] = rec.originalReqID
	}
	if info.TranslationReqID != "" {
		rec.translationReqID = info.TranslationReqID
		s.translationReqToOriginal[info.TranslationReqID] = rec.originalReqID
	}
	if info.TranslationTaskID != "" {
		rec.translationTaskID = info.TranslationTaskID
		s.translationTaskToOriginal[info.TranslationTaskID] = rec.originalReqID
	}
	if info.HasVAddr {
		rec.hasVAddr = true
		rec.vaddr = info.VAddr
	}
	if info.HasPAddr {
		rec.hasPAddr = true
		rec.paddr = info.PAddr
	}
	if info.IsRemote {
		rec.isRemote = true
		rec.route = "remote"
		rec.requesterGPM = info.RequesterGPM
		rec.providerGPM = info.ProviderGPM
		rec.hops = manhattanHops(rec.requesterGPM, rec.providerGPM, s.tileWidth)
	} else if rec.route == "" {
		rec.route = "local"
	}
}

func (s *memoryPathStats) completeRecordLocked(rec *memoryPathRecord) bool {
	if rec == nil || rec.counted || rec.sequence == 0 {
		return false
	}
	rec.counted = true

	if s.remoteOnly && !rec.isRemote {
		if s.streaming {
			s.releaseRecordLocked(rec)
		}
		return false
	}
	if s.remoteOnly {
		s.matched++
		rec.traceSeq = s.matched
	} else {
		rec.traceSeq = rec.sequence
	}

	s.full.add(rec)
	if s.inSteadyScope(rec.traceSeq) {
		s.steady.add(rec)
	}
	if s.inRawWindow(rec.traceSeq) {
		s.writeRawLocked(rec)
		if s.streaming {
			s.streamL1VPathRecordLocked(rec)
		}
	}
	if s.streaming {
		s.releaseRecordLocked(rec)
	}
	if s.traceDoneLocked() {
		s.stopped = true
		s.doneNotified = true
		return true
	}
	return false
}

func (s *memoryPathStats) releaseRecordLocked(rec *memoryPathRecord) {
	if rec == nil {
		return
	}
	if rec.originalReqID != "" {
		delete(s.records, rec.originalReqID)
	}
	if rec.translationReqID != "" {
		delete(s.translationReqToOriginal, rec.translationReqID)
	}
	if rec.translationTaskID != "" {
		delete(s.translationTaskToOriginal, rec.translationTaskID)
	}
	if rec.translatedReqID != "" {
		delete(s.translatedReqToOriginal, rec.translatedReqID)
	}
	for _, hop := range rec.l1vPathHops {
		if hop.requestMsgID != "" {
			delete(s.networkMsgToOriginal, hop.requestMsgID)
			delete(s.networkMsgDirection, hop.requestMsgID)
		}
		if hop.responseMsgID != "" {
			delete(s.networkMsgToOriginal, hop.responseMsgID)
			delete(s.networkMsgDirection, hop.responseMsgID)
		}
	}
}

func (s *memoryPathStats) outputSequence(rec *memoryPathRecord) uint64 {
	if rec == nil {
		return 0
	}
	if rec.traceSeq > 0 {
		return rec.traceSeq
	}
	return rec.sequence
}

func (s *memoryPathStats) inSteadyScope(sequence uint64) bool {
	if sequence <= s.warmupAccesses {
		return false
	}
	return true
}

func (s *memoryPathStats) inRawWindow(sequence uint64) bool {
	if !s.inSteadyScope(sequence) {
		return false
	}
	if s.maxRecords == 0 {
		return true
	}
	return sequence <= s.warmupAccesses+s.maxRecords
}

func (s *memoryPathStats) writeRawLocked(rec *memoryPathRecord) {
	if s.maxRecords > 0 && s.written >= s.maxRecords {
		return
	}
	row := []string{
		strconv.FormatUint(s.outputSequence(rec), 10),
		strconv.FormatUint(rec.completionTimeNS, 10),
		rec.originalReqID,
		rec.translatedReqID,
		rec.translationReqID,
		rec.translationTaskID,
		rec.accessType,
		strconv.FormatUint(rec.pid, 10),
		strconv.Itoa(rec.requesterGPM),
		strconv.Itoa(rec.providerGPM),
		strconv.Itoa(rec.hops),
		strconv.FormatBool(rec.isRemote),
		strconv.FormatBool(rec.hasVAddr),
		strconv.FormatUint(rec.vaddr, 10),
		strconv.FormatBool(rec.hasPAddr),
		strconv.FormatUint(rec.paddr, 10),
		strconv.FormatUint(pageBaseForLog(rec.paddr, s.log2Page), 10),
		strconv.FormatUint(rec.bytes, 10),
		rec.l1vCacheComponent,
		rec.l1vCacheResult,
		strconv.FormatUint(rec.l1vCacheLatencyNS, 10),
		rec.l1vTLBComponent,
		rec.l1vTLBResult,
		strconv.FormatUint(rec.l1vTLBLatencyNS, 10),
		rec.l2TLBComponent,
		rec.l2TLBResult,
		strconv.FormatUint(rec.l2TLBLatencyNS, 10),
		rec.l2CacheComponent,
		rec.l2CacheResult,
		strconv.FormatUint(rec.l2CacheLatencyNS, 10),
		rec.source,
		strconv.FormatUint(rec.dataSourceLatencyNS, 10),
		strconv.FormatUint(rec.remoteGPMLatencyNS, 10),
	}
	if s.streaming {
		if s.csv == nil {
			s.streamErr = fmt.Errorf("memory-path raw stream is not open")
			return
		}
		if err := s.csv.Write(row); err != nil && s.streamErr == nil {
			s.streamErr = err
		}
		s.written++
		if s.written%4096 == 0 {
			s.csv.Flush()
			if err := s.csv.Error(); err != nil && s.streamErr == nil {
				s.streamErr = err
			}
			if s.gzip != nil {
				if err := s.gzip.Flush(); err != nil && s.streamErr == nil {
					s.streamErr = err
				}
			}
		}
		return
	}
	s.raw = append(s.raw, memoryPathRawRow{
		sequence: rec.sequence,
		row:      row,
	})
	s.written++
}

func (s *memoryPathStats) traceDoneLocked() bool {
	return s.exitOnRawFull &&
		!s.doneNotified &&
		s.maxRecords > 0 &&
		s.written >= s.maxRecords
}

func (s *memoryPathStats) collectingLocked() bool {
	return s.enabled && !s.stopped
}

func (a *memoryPathAggregate) add(rec *memoryPathRecord) {
	a.total++
	if rec.isRemote {
		a.remote++
	}
	if rec.source != "" {
		a.sourceHits[rec.source]++
	}

	a.addJoint("l1vtlb_vs_l1v_cache", rec.l1vTLBResult, rec.l1vCacheResult)
	a.addJoint("l2tlb_vs_l2_cache", rec.l2TLBResult, rec.l2CacheResult)
	a.addJoint("any_tlb_vs_l2_cache", worstResult(rec.l1vTLBResult, rec.l2TLBResult), rec.l2CacheResult)
	a.addJoint("any_tlb_vs_any_cache", worstResult(rec.l1vTLBResult, rec.l2TLBResult), worstResult(rec.l1vCacheResult, rec.l2CacheResult))

	a.addStage("l1v_cache_end_to_end", rec.l1vCacheLatencyNS)
	a.addStage("l2_cache_end_to_end", rec.l2CacheLatencyNS)
	a.addStage("l1v_tlb", rec.l1vTLBLatencyNS)
	a.addStage("l2_tlb", rec.l2TLBLatencyNS)
	a.addStage("data_source", rec.dataSourceLatencyNS)
	a.addStage("remote_gpm", rec.remoteGPMLatencyNS)
}

func (a *memoryPathAggregate) addJoint(pair, tlbResult, cacheResult string) {
	if tlbResult == "" || cacheResult == "" {
		return
	}
	counter := a.joint[pair]
	if counter == nil {
		counter = &jointCounter{}
		a.joint[pair] = counter
	}
	counter.total++
	if tlbResult == "miss" {
		counter.tlbMiss++
	}
	if memoryPathNotHit(tlbResult) {
		counter.tlbNotHit++
	}
	if cacheResult == "miss" {
		counter.cacheMiss++
	}
	if memoryPathNotHit(cacheResult) {
		counter.cacheNotHit++
	}
	if tlbResult == "miss" && cacheResult == "miss" {
		counter.strictJoint++
	}
	if memoryPathNotHit(tlbResult) && memoryPathNotHit(cacheResult) {
		counter.notHitJoint++
	}
}

func (a *memoryPathAggregate) addStage(stage string, latencyNS uint64) {
	if latencyNS == 0 {
		return
	}
	counter := a.stages[stage]
	if counter == nil {
		counter = &stageCounter{}
		a.stages[stage] = counter
	}
	counter.accesses++
	counter.sumNS += latencyNS
}

func (s *memoryPathStats) dumpSummaryLocked() error {
	file, err := os.Create(s.prefix + "_summary.csv")
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprintln(file, "scope,total_accesses,remote_accesses,remote_ratio,raw_records,warmup_accesses,max_records,top_source,top_source_accesses")
	for _, scope := range []struct {
		name string
		agg  memoryPathAggregate
	}{
		{"full", s.full},
		{"steady", s.steady},
	} {
		source, sourceAccesses := topSource(scope.agg.sourceHits)
		fmt.Fprintf(file, "%s,%d,%d,%.6f,%d,%d,%d,%s,%d\n",
			scope.name,
			scope.agg.total,
			scope.agg.remote,
			ratio(scope.agg.remote, scope.agg.total),
			s.written,
			s.warmupAccesses,
			s.maxRecords,
			source,
			sourceAccesses,
		)
	}
	return nil
}

func (s *memoryPathStats) dumpJointMissLocked() error {
	file, err := os.Create(s.prefix + "_joint_miss.csv")
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprintln(file, "scope,pair,total,tlb_miss,tlb_not_hit,cache_miss,cache_not_hit,strict_joint_miss,strict_joint_miss_ratio,not_hit_joint_miss,not_hit_joint_miss_ratio")
	for _, scope := range []struct {
		name string
		agg  memoryPathAggregate
	}{
		{"full", s.full},
		{"steady", s.steady},
	} {
		pairs := make([]string, 0, len(scope.agg.joint))
		for pair := range scope.agg.joint {
			pairs = append(pairs, pair)
		}
		sort.Strings(pairs)
		for _, pair := range pairs {
			counter := scope.agg.joint[pair]
			fmt.Fprintf(file, "%s,%s,%d,%d,%d,%d,%d,%d,%.6f,%d,%.6f\n",
				scope.name,
				pair,
				counter.total,
				counter.tlbMiss,
				counter.tlbNotHit,
				counter.cacheMiss,
				counter.cacheNotHit,
				counter.strictJoint,
				ratio(counter.strictJoint, counter.total),
				counter.notHitJoint,
				ratio(counter.notHitJoint, counter.total),
			)
		}
	}
	return nil
}

func (s *memoryPathStats) dumpStageLatencyLocked() error {
	file, err := os.Create(s.prefix + "_stage_latency.csv")
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprintln(file, "scope,stage,accesses,avg_latency_ns,total_latency_ns")
	for _, scope := range []struct {
		name string
		agg  memoryPathAggregate
	}{
		{"full", s.full},
		{"steady", s.steady},
	} {
		stages := make([]string, 0, len(scope.agg.stages))
		for stage := range scope.agg.stages {
			stages = append(stages, stage)
		}
		sort.Strings(stages)
		for _, stage := range stages {
			counter := scope.agg.stages[stage]
			fmt.Fprintf(file, "%s,%s,%d,%d,%d\n",
				scope.name,
				stage,
				counter.accesses,
				avg(counter.sumNS, counter.accesses),
				counter.sumNS,
			)
		}
	}
	return nil
}

func memoryPathCacheTier(name string) string {
	switch {
	case strings.Contains(name, ".L1VCache"):
		return "l1v"
	case isL2CacheName(name):
		return "l2"
	default:
		return ""
	}
}

func memoryPathTLBTier(name string) string {
	switch {
	case strings.Contains(name, ".L1VTLB"):
		return "l1v_tlb"
	case strings.Contains(name, ".L2TLB"):
		return "l2_tlb"
	default:
		return ""
	}
}

func normalizeMemoryPathResult(result string) string {
	switch {
	case strings.Contains(result, "mshr-hit"):
		return "mshr-hit"
	case strings.Contains(result, "miss"):
		return "miss"
	case strings.Contains(result, "hit"):
		return "hit"
	default:
		return normalizeOp(result)
	}
}

func memoryPathNotHit(result string) bool {
	return result == "miss" || result == "mshr-hit"
}

func worstResult(a, b string) string {
	if a == "miss" || b == "miss" {
		return "miss"
	}
	if a == "mshr-hit" || b == "mshr-hit" {
		return "mshr-hit"
	}
	if a == "hit" || b == "hit" {
		return "hit"
	}
	if a != "" {
		return a
	}
	return b
}

func pageBaseForLog(addr uint64, log2Page uint64) uint64 {
	if log2Page == 0 {
		return addr
	}
	pageSize := uint64(1) << log2Page
	return (addr / pageSize) * pageSize
}

func ratio(num, den uint64) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func avg(sum, count uint64) uint64 {
	if count == 0 {
		return 0
	}
	return sum / count
}

func topSource(sources map[string]uint64) (string, uint64) {
	top := ""
	var topCount uint64
	for source, count := range sources {
		if count > topCount || (count == topCount && source < top) {
			top = source
			topCount = count
		}
	}
	return top, topCount
}

func parseGPMIDFromInfoOrName(info interface{}, name string) int {
	if accessInfo, ok := GetL2AccessInfo(info); ok {
		if accessInfo.RequesterGPM >= 0 {
			return accessInfo.RequesterGPM
		}
	}
	return parseGPMID(name)
}
