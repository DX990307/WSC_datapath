package trace

import (
	"compress/gzip"
	"encoding/csv"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sarchlab/akita/v3/sim"
)

const (
	observationRemoteLineSize          = uint64(64)
	defaultObservationRemoteMaxRecords = uint64(1_000_000)
	defaultObservationL2SampleMax      = uint64(1_000_000)
)

var observationGPUNamePattern = regexp.MustCompile(`(?i)GPU\[(\d+)\]`)

// ObservationRemoteConfig controls the passive remote-request and L2-usage
// trace. A zero maximum selects a conservative finite default; observation
// state is therefore bounded even when a caller omits the limits.
type ObservationRemoteConfig struct {
	Prefix         string
	WarmupRequests uint64
	MaxRequests    uint64
	L2SampleMax    uint64
	TileWidth      int
}

// ObservationRemoteRequestStart is recorded at requester-side RDMA admission.
// Calls must follow logical arrival order so SameLineInflightAtArrival remains
// an architectural arrival-time measurement even if issue order later changes.
type ObservationRemoteRequestStart struct {
	LogicalRequestID string
	PID              uint64
	Operation        string
	Address          uint64
	ByteSize         uint64
	RequesterName    string
	OwnerName        string
	ArrivalTime      sim.VTimeInSec
}

// ObservationRemoteRequestIssue records the successful requester-side wire
// issue of a request that was previously admitted with StartRemoteRequest.
// Keeping issue separate from arrival is essential for measuring overlap in
// arrival order when output backpressure later changes issue order.
type ObservationRemoteRequestIssue struct {
	LogicalRequestID    string
	IssueTime           sim.VTimeInSec
	ForwardWireID       string
	ForwardTrafficBytes uint64
}

// ObservationRemoteRequestCompletion closes one logical request after its
// requester-visible response returns.
type ObservationRemoteRequestCompletion struct {
	LogicalRequestID   string
	CompletionTime     sim.VTimeInSec
	ReturnWireID       string
	ReturnTrafficBytes uint64
}

type observationRemoteLineKey struct {
	pid       uint64
	ownerGPU  int
	ownerName string
	line      uint64
}

// observationRemoteInflightKey is deliberately stricter than lineKey. Exact
// request deduplication is requester-component-local and cannot cross a write
// epoch. Using the component name (rather than only its parsed GPU ID) avoids
// merging independent requester engines that happen to reside on one GPU.
type observationRemoteInflightKey struct {
	lineKey       observationRemoteLineKey
	requesterName string
	writeEpoch    uint64
}

type observationRemoteRequest struct {
	sequence uint64
	selected bool

	id            string
	pid           uint64
	op            string
	address       uint64
	line          uint64
	byteSize      uint64
	requesterName string
	ownerName     string
	requesterGPU  int
	ownerGPU      int
	hops          int

	arrivalPS uint64
	issuePS   uint64
	issued    bool

	writeEpoch                uint64
	sameLineInflightAtArrival uint64
	lineKey                   observationRemoteLineKey
	inflightKey               observationRemoteInflightKey
	tracksReadInflight        bool

	forwardWireID       string
	forwardTrafficBytes uint64
	timeRegression      bool
}

type observationRemoteSummary struct {
	measuredStarts       uint64
	completed            uint64
	incomplete           uint64
	rowsWritten          uint64
	reads                uint64
	writes               uint64
	otherOps             uint64
	logicalBytes         uint64
	forwardTrafficBytes  uint64
	returnTrafficBytes   uint64
	sameLineInflightHits uint64
	validHopCount        uint64
	hopSum               uint64
	latencyCount         uint64
	queueWaitSumPS       uint64
	serviceSumPS         uint64
	totalLatencySumPS    uint64
}

type observationRemoteValidation struct {
	startCalls                 uint64
	issueCalls                 uint64
	droppedAfterMax            uint64
	emptyRequestIDs            uint64
	emptyIssueRequestIDs       uint64
	duplicateStarts            uint64
	duplicateIssues            uint64
	unknownIssues              uint64
	issuesAfterCompletion      uint64
	ignoredIssuesAfterMax      uint64
	unknownCompletions         uint64
	duplicateCompletions       uint64
	ignoredCompletionsAfterMax uint64
	completionsBeforeIssue     uint64
	timeRegressions            uint64
	malformedRequesterGPU      uint64
	malformedOwnerGPU          uint64
	inflightUnderflows         uint64
	maxActiveRequests          uint64
	maxInflightLines           uint64
	maxWriteEpochLines         uint64
	incompleteAtDump           uint64
	l2SampleCalls              uint64
	l2SamplesDropped           uint64
	l2InvalidSamples           uint64
	writerErrors               uint64
}

type observationL2Aggregate struct {
	validSamples uint64
	validSum     uint64
	totalSum     uint64
	dirtySum     uint64
	lockedSum    uint64
	mshrSum      uint64
	underHalf    uint64
}

type observationRemoteStats struct {
	sync.Mutex

	enabled       bool
	selectionFull bool
	frozen        bool
	drainClosed   bool
	drainCh       chan struct{}
	prefix        string
	tileWidth     int
	warmup        uint64
	maxRecords    uint64
	l2SampleMax   uint64

	observed       uint64
	l2Observed     uint64
	l2Written      uint64
	maxSeenPS      uint64
	active         map[string]*observationRemoteRequest
	completedIDs   map[string]struct{}
	inflightByLine map[observationRemoteInflightKey]uint64
	writeEpoch     map[observationRemoteLineKey]uint64
	uniqueLines    map[observationRemoteLineKey]struct{}

	remoteFile *os.File
	remoteGZIP *gzip.Writer
	remoteCSV  *csv.Writer
	l2File     *os.File
	l2GZIP     *gzip.Writer
	l2CSV      *csv.Writer
	firstErr   error

	summary    observationRemoteSummary
	validation observationRemoteValidation
	l2Summary  observationL2Aggregate
}

var globalObservationRemoteStats = newObservationRemoteStats()
var observationRemoteActive atomic.Bool
var observationRemoteAdmissions atomic.Bool

func newObservationRemoteStats() *observationRemoteStats {
	s := &observationRemoteStats{}
	s.resetStateLocked()
	return s
}

func (s *observationRemoteStats) resetStateLocked() {
	s.selectionFull = false
	s.frozen = false
	s.drainClosed = false
	s.drainCh = make(chan struct{})
	s.observed = 0
	s.l2Observed = 0
	s.l2Written = 0
	s.maxSeenPS = 0
	s.active = make(map[string]*observationRemoteRequest)
	s.completedIDs = make(map[string]struct{})
	s.inflightByLine = make(map[observationRemoteInflightKey]uint64)
	s.writeEpoch = make(map[observationRemoteLineKey]uint64)
	s.uniqueLines = make(map[observationRemoteLineKey]struct{})
	s.summary = observationRemoteSummary{}
	s.validation = observationRemoteValidation{}
	s.l2Summary = observationL2Aggregate{}
	s.firstErr = nil
}

// EnableObservationRemoteTrace opens two bounded raw streams:
// <prefix>_remote_requests.csv.gz and <prefix>_l2_utilization.csv.gz.
func EnableObservationRemoteTrace(config ObservationRemoteConfig) error {
	observationRemoteAdmissions.Store(false)
	observationRemoteActive.Store(false)
	s := globalObservationRemoteStats
	s.Lock()
	defer s.Unlock()

	if err := s.closeRawLocked(); err != nil {
		return err
	}
	s.enabled = false
	s.resetStateLocked()

	prefix := config.Prefix
	if prefix == "" {
		prefix = "observation"
	}
	if dir := filepath.Dir(prefix); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	maxRecords := config.MaxRequests
	if maxRecords == 0 {
		maxRecords = defaultObservationRemoteMaxRecords
	}
	l2SampleMax := config.L2SampleMax
	if l2SampleMax == 0 {
		l2SampleMax = defaultObservationL2SampleMax
	}

	remoteFile, remoteGZIP, remoteCSV, err := openObservationGZIPCSV(
		prefix+"_remote_requests.csv.gz", observationRemoteHeader())
	if err != nil {
		return err
	}
	l2File, l2GZIP, l2CSV, err := openObservationGZIPCSV(
		prefix+"_l2_utilization.csv.gz", observationL2Header())
	if err != nil {
		_ = closeObservationGZIPCSV(remoteFile, remoteGZIP, remoteCSV)
		return err
	}

	s.enabled = true
	s.prefix = prefix
	s.tileWidth = config.TileWidth
	s.warmup = config.WarmupRequests
	s.maxRecords = maxRecords
	s.l2SampleMax = l2SampleMax
	s.remoteFile = remoteFile
	s.remoteGZIP = remoteGZIP
	s.remoteCSV = remoteCSV
	s.l2File = l2File
	s.l2GZIP = l2GZIP
	s.l2CSV = l2CSV
	observationRemoteActive.Store(true)
	observationRemoteAdmissions.Store(true)
	return nil
}

// DisableObservationRemoteTrace closes the raw streams and drops in-memory
// state. Use DumpObservationRemoteTrace when summary and validation files are
// required.
func DisableObservationRemoteTrace() {
	observationRemoteAdmissions.Store(false)
	observationRemoteActive.Store(false)
	s := globalObservationRemoteStats
	s.Lock()
	defer s.Unlock()
	_ = s.closeRawLocked()
	s.enabled = false
	s.prefix = ""
	s.resetStateLocked()
}

// ObservationRemoteTraceEnabled reports whether the passive tracer accepts
// new events.
func ObservationRemoteTraceEnabled() bool {
	return observationRemoteAdmissions.Load()
}

// FreezeObservationRemoteTrace stops admitting new logical requests and
// returns a channel closed after every already admitted request completes.
// The simulation engine must keep running while the caller waits. This gives
// early-exit observation runs a clean, unbiased remote-request tail.
func FreezeObservationRemoteTrace() <-chan struct{} {
	observationRemoteAdmissions.Store(false)
	s := globalObservationRemoteStats
	s.Lock()
	defer s.Unlock()
	if !s.enabled {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	s.frozen = true
	if len(s.active) == 0 {
		s.closeDrainLocked()
	}
	return s.drainCh
}

func (s *observationRemoteStats) closeDrainLocked() {
	if s.drainClosed {
		return
	}
	close(s.drainCh)
	s.drainClosed = true
}

// StartRemoteRequest records one requester-side logical remote request.
func StartRemoteRequest(start ObservationRemoteRequestStart) {
	if !observationRemoteAdmissions.Load() {
		return
	}
	s := globalObservationRemoteStats
	s.Lock()
	defer s.Unlock()
	if !s.enabled || s.frozen {
		return
	}

	s.validation.startCalls++
	if start.LogicalRequestID == "" {
		s.validation.emptyRequestIDs++
		return
	}
	if _, ok := s.active[start.LogicalRequestID]; ok {
		s.validation.duplicateStarts++
		return
	}
	if _, ok := s.completedIDs[start.LogicalRequestID]; ok {
		s.validation.duplicateStarts++
		return
	}
	if s.selectionFull {
		s.validation.droppedAfterMax++
		return
	}

	s.observed++
	sequence := s.observed
	selected := sequence > s.warmup
	if selected && s.summary.measuredStarts >= s.maxRecords {
		s.selectionFull = true
		s.validation.droppedAfterMax++
		return
	}

	requesterGPU := parseObservationGPU(start.RequesterName)
	ownerGPU := parseObservationGPU(start.OwnerName)
	if selected && requesterGPU < 0 {
		s.validation.malformedRequesterGPU++
	}
	if selected && ownerGPU < 0 {
		s.validation.malformedOwnerGPU++
	}
	line := start.Address &^ (observationRemoteLineSize - 1)
	lineKey := observationRemoteLineKey{
		pid: start.PID, ownerGPU: ownerGPU,
		ownerName: start.OwnerName, line: line,
	}

	op := normalizeObservationRemoteOp(start.Operation)
	isWrite := observationRemoteIsWrite(op)
	isRead := observationRemoteIsRead(op) && !isWrite
	epoch := s.writeEpoch[lineKey]
	if isWrite {
		epoch++
		s.writeEpoch[lineKey] = epoch
		if uint64(len(s.writeEpoch)) > s.validation.maxWriteEpochLines {
			s.validation.maxWriteEpochLines = uint64(len(s.writeEpoch))
		}
	}
	arrivalPS := observationRemotePS(start.ArrivalTime)
	s.seeTimeLocked(arrivalPS)

	// Only read-read overlap within the same requester and write epoch can be
	// eliminated by requester-side exact-line deduplication. Writes still
	// advance the globally scoped PID+owner+line epoch, but they never occupy
	// the read inflight table themselves.
	inflightKey := observationRemoteInflightKey{
		lineKey: lineKey, requesterName: start.RequesterName,
		writeEpoch: epoch,
	}
	tracksReadInflight := isRead
	inflight := uint64(0)
	if tracksReadInflight {
		inflight = s.inflightByLine[inflightKey]
		s.inflightByLine[inflightKey] = inflight + 1
		if uint64(len(s.inflightByLine)) > s.validation.maxInflightLines {
			s.validation.maxInflightLines = uint64(len(s.inflightByLine))
		}
	}

	req := &observationRemoteRequest{
		sequence: sequence, selected: selected,
		id: start.LogicalRequestID, pid: start.PID, op: op,
		address: start.Address, line: line, byteSize: start.ByteSize,
		requesterName: start.RequesterName, ownerName: start.OwnerName,
		requesterGPU: requesterGPU, ownerGPU: ownerGPU,
		hops:       manhattanObservationHops(requesterGPU, ownerGPU, s.tileWidth),
		arrivalPS:  arrivalPS,
		writeEpoch: epoch, sameLineInflightAtArrival: inflight,
		lineKey: lineKey, inflightKey: inflightKey,
		tracksReadInflight: tracksReadInflight,
	}
	s.active[req.id] = req
	if uint64(len(s.active)) > s.validation.maxActiveRequests {
		s.validation.maxActiveRequests = uint64(len(s.active))
	}

	if selected {
		s.summary.measuredStarts++
		s.summary.logicalBytes += req.byteSize
		if req.sameLineInflightAtArrival > 0 {
			s.summary.sameLineInflightHits++
		}
		if req.hops >= 0 {
			s.summary.validHopCount++
			s.summary.hopSum += uint64(req.hops)
		}
		s.uniqueLines[lineKey] = struct{}{}
		switch {
		case isWrite:
			s.summary.writes++
		case isRead:
			s.summary.reads++
		default:
			s.summary.otherOps++
		}
		if s.summary.measuredStarts == s.maxRecords {
			s.selectionFull = true
		}
	}
}

// IssueRemoteRequest attaches successful wire-issue metadata to an admitted
// request. It intentionally continues to update active requests after Freeze:
// freeze stops new admissions but the admitted tail must still issue and drain.
func IssueRemoteRequest(issue ObservationRemoteRequestIssue) {
	if !observationRemoteActive.Load() {
		return
	}
	s := globalObservationRemoteStats
	s.Lock()
	defer s.Unlock()
	if !s.enabled {
		return
	}

	s.validation.issueCalls++
	if issue.LogicalRequestID == "" {
		s.validation.emptyIssueRequestIDs++
		return
	}
	req := s.active[issue.LogicalRequestID]
	if req == nil {
		if _, ok := s.completedIDs[issue.LogicalRequestID]; ok {
			s.validation.issuesAfterCompletion++
		} else if s.selectionFull || s.frozen {
			s.validation.ignoredIssuesAfterMax++
		} else {
			s.validation.unknownIssues++
		}
		return
	}
	if req.issued {
		s.validation.duplicateIssues++
		return
	}

	req.issued = true
	req.issuePS = observationRemotePS(issue.IssueTime)
	req.forwardWireID = issue.ForwardWireID
	req.forwardTrafficBytes = issue.ForwardTrafficBytes
	s.seeTimeLocked(req.issuePS)
	if req.issuePS < req.arrivalPS {
		req.timeRegression = true
		if req.selected {
			s.validation.timeRegressions++
		}
	}
	if req.selected {
		s.summary.forwardTrafficBytes += req.forwardTrafficBytes
	}
}

// CompleteRemoteRequest streams one completed row and immediately releases
// its active-request and same-line inflight state.
func CompleteRemoteRequest(completion ObservationRemoteRequestCompletion) {
	if !observationRemoteActive.Load() {
		return
	}
	s := globalObservationRemoteStats
	s.Lock()
	defer s.Unlock()
	if !s.enabled {
		return
	}

	req := s.active[completion.LogicalRequestID]
	if req == nil {
		if _, ok := s.completedIDs[completion.LogicalRequestID]; ok {
			s.validation.duplicateCompletions++
		} else if s.selectionFull || s.frozen {
			s.validation.ignoredCompletionsAfterMax++
		} else {
			s.validation.unknownCompletions++
		}
		return
	}
	delete(s.active, req.id)
	if req.tracksReadInflight {
		s.releaseInflightLocked(req.inflightKey)
	}
	if s.frozen && len(s.active) == 0 {
		s.closeDrainLocked()
	}

	completionPS := observationRemotePS(completion.CompletionTime)
	s.seeTimeLocked(completionPS)
	if !req.issued {
		s.validation.completionsBeforeIssue++
		if req.selected && !req.timeRegression {
			s.validation.timeRegressions++
		}
		req.timeRegression = true
	} else if completionPS < req.issuePS || completionPS < req.arrivalPS {
		if req.selected && !req.timeRegression {
			s.validation.timeRegressions++
		}
		req.timeRegression = true
	}
	s.completedIDs[req.id] = struct{}{}
	if !req.selected {
		return
	}

	s.summary.completed++
	s.summary.returnTrafficBytes += completion.ReturnTrafficBytes
	if !req.timeRegression {
		s.summary.latencyCount++
		s.summary.queueWaitSumPS += req.issuePS - req.arrivalPS
		s.summary.serviceSumPS += completionPS - req.issuePS
		s.summary.totalLatencySumPS += completionPS - req.arrivalPS
	}
	s.writeRemoteRowLocked(req, "complete", true, completionPS,
		completion.ReturnWireID, completion.ReturnTrafficBytes)
}

// RecordObservationL2Sample streams one instantaneous L2 utilization sample.
// Counts are signed so invalid instrumentation cannot silently wrap into a
// very large unsigned value; invalid rows remain visible with status=invalid.
func RecordObservationL2Sample(
	cacheName string,
	now sim.VTimeInSec,
	valid, total, dirty, locked, mshr int,
) {
	if !observationRemoteActive.Load() {
		return
	}
	s := globalObservationRemoteStats
	s.Lock()
	defer s.Unlock()
	if !s.enabled {
		return
	}
	s.validation.l2SampleCalls++
	s.l2Observed++
	if s.l2Written >= s.l2SampleMax {
		s.validation.l2SamplesDropped++
		return
	}

	status := "complete"
	validSample := valid >= 0 && total > 0 && valid <= total &&
		dirty >= 0 && dirty <= valid && locked >= 0 && locked <= valid &&
		mshr >= 0
	if !validSample {
		status = "invalid"
		s.validation.l2InvalidSamples++
	}
	free := total - valid
	occupancyPPM := int64(-1)
	if total > 0 && valid >= 0 {
		occupancyPPM = int64(valid) * 1_000_000 / int64(total)
	}
	timePS := observationRemotePS(now)
	s.seeTimeLocked(timePS)
	row := []string{
		strconv.FormatUint(s.l2Observed, 10),
		strconv.FormatUint(timePS, 10),
		cacheName,
		strconv.Itoa(parseObservationGPU(cacheName)),
		strconv.Itoa(valid),
		strconv.Itoa(total),
		strconv.Itoa(free),
		strconv.FormatInt(occupancyPPM, 10),
		strconv.Itoa(dirty),
		strconv.Itoa(locked),
		strconv.Itoa(mshr),
		status,
	}
	if err := s.l2CSV.Write(row); err != nil {
		s.recordWriterErrorLocked(err)
	}
	s.l2Written++
	if validSample {
		s.l2Summary.validSamples++
		s.l2Summary.validSum += uint64(valid)
		s.l2Summary.totalSum += uint64(total)
		s.l2Summary.dirtySum += uint64(dirty)
		s.l2Summary.lockedSum += uint64(locked)
		s.l2Summary.mshrSum += uint64(mshr)
		if valid*2 < total {
			s.l2Summary.underHalf++
		}
	}
}

// DumpObservationRemoteTrace writes incomplete requests explicitly, closes
// both raw streams, writes summary/validation CSVs, and clears all state.
func DumpObservationRemoteTrace() error {
	observationRemoteAdmissions.Store(false)
	observationRemoteActive.Store(false)
	s := globalObservationRemoteStats
	s.Lock()
	defer s.Unlock()
	if !s.enabled {
		return nil
	}

	incomplete := make([]*observationRemoteRequest, 0, len(s.active))
	for _, req := range s.active {
		if req.selected {
			incomplete = append(incomplete, req)
		}
	}
	sort.Slice(incomplete, func(i, j int) bool {
		return incomplete[i].sequence < incomplete[j].sequence
	})
	for _, req := range incomplete {
		s.summary.incomplete++
		s.validation.incompleteAtDump++
		s.writeRemoteRowLocked(req, "incomplete", false, 0, "", 0)
	}

	for _, req := range s.active {
		if req.tracksReadInflight {
			s.releaseInflightLocked(req.inflightKey)
		}
	}
	s.active = make(map[string]*observationRemoteRequest)

	rawErr := s.closeRawLocked()
	summaryErr := s.writeSummaryLocked()
	validationErr := s.writeValidationLocked()
	firstErr := s.firstErr
	s.enabled = false
	s.active = make(map[string]*observationRemoteRequest)
	s.completedIDs = make(map[string]struct{})
	s.inflightByLine = make(map[observationRemoteInflightKey]uint64)
	s.writeEpoch = make(map[observationRemoteLineKey]uint64)
	s.uniqueLines = make(map[observationRemoteLineKey]struct{})
	return errors.Join(firstErr, rawErr, summaryErr, validationErr)
}

func (s *observationRemoteStats) writeRemoteRowLocked(
	req *observationRemoteRequest,
	status string,
	hasCompletion bool,
	completionPS uint64,
	returnWireID string,
	returnTrafficBytes uint64,
) {
	completion := ""
	queueWait := ""
	service := ""
	total := ""
	if hasCompletion {
		completion = strconv.FormatUint(completionPS, 10)
		if !req.timeRegression {
			queueWait = strconv.FormatUint(req.issuePS-req.arrivalPS, 10)
			service = strconv.FormatUint(completionPS-req.issuePS, 10)
			total = strconv.FormatUint(completionPS-req.arrivalPS, 10)
		}
	}
	row := []string{
		strconv.FormatUint(req.sequence, 10),
		status,
		req.id,
		strconv.FormatUint(req.pid, 10),
		req.op,
		strconv.FormatUint(req.address, 10),
		strconv.FormatUint(req.line, 10),
		strconv.FormatUint(req.byteSize, 10),
		req.requesterName,
		req.ownerName,
		strconv.Itoa(req.requesterGPU),
		strconv.Itoa(req.ownerGPU),
		strconv.Itoa(req.hops),
		strconv.FormatUint(req.arrivalPS, 10),
		strconv.FormatUint(req.issuePS, 10),
		completion,
		queueWait,
		service,
		total,
		strconv.FormatUint(req.writeEpoch, 10),
		strconv.FormatUint(req.sameLineInflightAtArrival, 10),
		req.forwardWireID,
		strconv.FormatUint(req.forwardTrafficBytes, 10),
		returnWireID,
		strconv.FormatUint(returnTrafficBytes, 10),
		strconv.FormatUint(req.forwardTrafficBytes+returnTrafficBytes, 10),
		strconv.FormatBool(req.timeRegression),
	}
	if err := s.remoteCSV.Write(row); err != nil {
		s.recordWriterErrorLocked(err)
	}
	s.summary.rowsWritten++
}

func (s *observationRemoteStats) releaseInflightLocked(
	key observationRemoteInflightKey,
) {
	count := s.inflightByLine[key]
	if count == 0 {
		s.validation.inflightUnderflows++
		return
	}
	if count == 1 {
		delete(s.inflightByLine, key)
		return
	}
	s.inflightByLine[key] = count - 1
}

func (s *observationRemoteStats) seeTimeLocked(ps uint64) {
	if ps > s.maxSeenPS {
		s.maxSeenPS = ps
	}
}

func (s *observationRemoteStats) recordWriterErrorLocked(err error) {
	if err == nil {
		return
	}
	s.validation.writerErrors++
	if s.firstErr == nil {
		s.firstErr = err
	}
}

func (s *observationRemoteStats) closeRawLocked() error {
	var errs []error
	if s.remoteFile != nil || s.remoteGZIP != nil || s.remoteCSV != nil {
		errs = append(errs, closeObservationGZIPCSV(
			s.remoteFile, s.remoteGZIP, s.remoteCSV))
	}
	if s.l2File != nil || s.l2GZIP != nil || s.l2CSV != nil {
		errs = append(errs, closeObservationGZIPCSV(
			s.l2File, s.l2GZIP, s.l2CSV))
	}
	s.remoteFile, s.remoteGZIP, s.remoteCSV = nil, nil, nil
	s.l2File, s.l2GZIP, s.l2CSV = nil, nil, nil
	return errors.Join(errs...)
}

func (s *observationRemoteStats) writeSummaryLocked() error {
	rows := [][]string{{"metric", "value"}}
	add := func(metric string, value uint64) {
		rows = append(rows, []string{metric, strconv.FormatUint(value, 10)})
	}
	add("measured_requests", s.summary.measuredStarts)
	add("completed_requests", s.summary.completed)
	add("incomplete_requests", s.summary.incomplete)
	add("remote_rows_written", s.summary.rowsWritten)
	add("read_requests", s.summary.reads)
	add("write_requests", s.summary.writes)
	add("other_requests", s.summary.otherOps)
	add("unique_owner_lines", uint64(len(s.uniqueLines)))
	add("logical_request_bytes", s.summary.logicalBytes)
	add("forward_traffic_bytes", s.summary.forwardTrafficBytes)
	add("return_traffic_bytes", s.summary.returnTrafficBytes)
	add("total_traffic_bytes",
		s.summary.forwardTrafficBytes+s.summary.returnTrafficBytes)
	add("same_line_inflight_arrivals", s.summary.sameLineInflightHits)
	add("valid_hop_requests", s.summary.validHopCount)
	add("hop_sum", s.summary.hopSum)
	add("latency_samples", s.summary.latencyCount)
	add("queue_wait_sum_ps", s.summary.queueWaitSumPS)
	add("service_sum_ps", s.summary.serviceSumPS)
	add("total_latency_sum_ps", s.summary.totalLatencySumPS)
	add("l2_samples_written", s.l2Written)
	add("l2_valid_samples", s.l2Summary.validSamples)
	add("l2_valid_blocks_sum", s.l2Summary.validSum)
	add("l2_total_blocks_sum", s.l2Summary.totalSum)
	add("l2_dirty_blocks_sum", s.l2Summary.dirtySum)
	add("l2_locked_blocks_sum", s.l2Summary.lockedSum)
	add("l2_mshr_entries_sum", s.l2Summary.mshrSum)
	add("l2_under_50_percent_samples", s.l2Summary.underHalf)
	return writeObservationPlainCSV(s.prefix+"_remote_summary.csv", rows)
}

func (s *observationRemoteStats) writeValidationLocked() error {
	type validationRow struct {
		name  string
		value uint64
		error bool
	}
	rowsIn := []validationRow{
		{"start_calls", s.validation.startCalls, false},
		{"issue_calls", s.validation.issueCalls, false},
		{"dropped_after_max", s.validation.droppedAfterMax, false},
		{"empty_request_ids", s.validation.emptyRequestIDs, true},
		{"empty_issue_request_ids", s.validation.emptyIssueRequestIDs, true},
		{"duplicate_starts", s.validation.duplicateStarts, true},
		{"duplicate_issues", s.validation.duplicateIssues, true},
		{"unknown_issues", s.validation.unknownIssues, true},
		{"issues_after_completion", s.validation.issuesAfterCompletion, true},
		{"ignored_issues_after_max", s.validation.ignoredIssuesAfterMax, false},
		{"unknown_completions", s.validation.unknownCompletions, true},
		{"duplicate_completions", s.validation.duplicateCompletions, true},
		{"ignored_completions_after_max", s.validation.ignoredCompletionsAfterMax, false},
		{"completions_before_issue", s.validation.completionsBeforeIssue, true},
		{"time_regressions", s.validation.timeRegressions, true},
		{"malformed_requester_gpu_names", s.validation.malformedRequesterGPU, true},
		{"malformed_owner_gpu_names", s.validation.malformedOwnerGPU, true},
		{"inflight_underflows", s.validation.inflightUnderflows, true},
		{"max_active_requests", s.validation.maxActiveRequests, false},
		{"max_inflight_lines", s.validation.maxInflightLines, false},
		{"max_write_epoch_lines", s.validation.maxWriteEpochLines, false},
		{"incomplete_at_dump", s.validation.incompleteAtDump, false},
		{"l2_sample_calls", s.validation.l2SampleCalls, false},
		{"l2_samples_dropped", s.validation.l2SamplesDropped, false},
		{"l2_invalid_samples", s.validation.l2InvalidSamples, true},
		{"writer_errors", s.validation.writerErrors, true},
	}
	rows := [][]string{{"metric", "value", "status"}}
	for _, item := range rowsIn {
		status := "info"
		if item.error {
			status = "ok"
			if item.value != 0 {
				status = "error"
			}
		}
		rows = append(rows, []string{
			item.name, strconv.FormatUint(item.value, 10), status,
		})
	}
	return writeObservationPlainCSV(s.prefix+"_remote_validation.csv", rows)
}

func observationRemoteHeader() []string {
	return strings.Split(
		"sequence,status,logical_request_id,pid,operation,address,line_address,"+
			"bytes,requester_name,owner_name,requester_gpu,owner_gpu,"+
			"manhattan_hops,arrival_ps,issue_ps,completion_ps,queue_wait_ps,"+
			"service_ps,total_ps,write_epoch,same_line_inflight_at_arrival,"+
			"forward_wire_id,forward_traffic_bytes,return_wire_id,"+
			"return_traffic_bytes,total_traffic_bytes,time_regression", ",")
}

func observationL2Header() []string {
	return strings.Split(
		"sequence,time_ps,cache_name,gpu_id,valid_blocks,total_blocks,"+
			"free_blocks,occupancy_ppm,dirty_blocks,locked_blocks,"+
			"mshr_entries,status", ",")
}

func openObservationGZIPCSV(
	path string,
	header []string,
) (*os.File, *gzip.Writer, *csv.Writer, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, nil, err
	}
	gz := gzip.NewWriter(f)
	w := csv.NewWriter(gz)
	if err := w.Write(header); err != nil {
		_ = closeObservationGZIPCSV(f, gz, w)
		return nil, nil, nil, err
	}
	return f, gz, w, nil
}

func closeObservationGZIPCSV(
	f *os.File,
	gz *gzip.Writer,
	w *csv.Writer,
) error {
	var errs []error
	if w != nil {
		w.Flush()
		if err := w.Error(); err != nil {
			errs = append(errs, err)
		}
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if f != nil {
		if err := f.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func writeObservationPlainCSV(path string, rows [][]string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			_ = f.Close()
			return err
		}
	}
	w.Flush()
	writeErr := w.Error()
	closeErr := f.Close()
	return errors.Join(writeErr, closeErr)
}

func parseObservationGPU(name string) int {
	match := observationGPUNamePattern.FindStringSubmatch(name)
	if len(match) != 2 {
		return -1
	}
	id, err := strconv.Atoi(match[1])
	if err != nil {
		return -1
	}
	return id
}

func manhattanObservationHops(a, b, tileWidth int) int {
	if a < 0 || b < 0 || tileWidth <= 0 {
		return -1
	}
	ax, ay := a%tileWidth, a/tileWidth
	bx, by := b%tileWidth, b/tileWidth
	dx, dy := ax-bx, ay-by
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}

func observationRemotePS(now sim.VTimeInSec) uint64 {
	if now <= 0 {
		return 0
	}
	return uint64(math.Round(float64(now) * 1e12))
}

func normalizeObservationRemoteOp(op string) string {
	op = strings.ToLower(strings.TrimSpace(op))
	if op == "" {
		return "unknown"
	}
	return op
}

func observationRemoteIsWrite(op string) bool {
	return strings.Contains(op, "write") || strings.Contains(op, "store") ||
		strings.Contains(op, "atomic") || strings.Contains(op, "rmw")
}

func observationRemoteIsRead(op string) bool {
	return strings.Contains(op, "read") || strings.Contains(op, "load")
}
