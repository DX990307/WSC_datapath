package trace

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
	"sync/atomic"

	"github.com/sarchlab/akita/v3/sim"
)

// ObservationParent describes one request merged into an L1 cache-line
// transaction. The observation path ID is deliberately not derived from a
// parent ID: it remains the immutable post-coalescing L1 transaction ID.
type ObservationParent struct {
	ID       string
	Address  uint64
	ByteSize uint64
	SendTime sim.VTimeInSec
}

// ObservationPathStart contains the immutable identity of one logical memory
// path. A path begins when the first parent reaches the L1 and finishes only
// after all of its parent responses have been sent.
type ObservationPathStart struct {
	PathID    string
	CacheName string
	Address   uint64
	ByteSize  uint64
	PID       uint64
	Operation string
	StartTime sim.VTimeInSec
	Parents   []ObservationParent
}

// These names form the exclusive latency stack written to the path CSV. A
// transition closes exactly one current stage and opens the next one, so stage
// durations never overlap by construction.
var observationStageNames = []string{
	"l1_coalesce",
	"l1_directory_queue",
	"l1_lookup",
	"l1_mshr_wait",
	"l1_bank",
	"l1_downstream",
	"requester_rdma",
	"remote_request_network",
	"owner_rdma_request",
	"owner_l2_link",
	"l2_queue",
	"l2_lookup",
	"l2_mshr_wait",
	"l2_bank",
	"l2_write_buffer",
	"l2_to_dram",
	"dram_queue_service",
	"dram_to_l2",
	"l2_fill_response",
	"owner_rdma_response",
	"remote_response_network",
	"requester_rdma_response",
	"requester_l1_link",
	"l2_response_link",
	"l2_to_l1",
	"l1_fill_response",
	"l1_response_fanout",
	"unattributed",
}

type observationEvent struct {
	name string
	ps   uint64
}

type observationPath struct {
	sequence uint64
	selected bool
	pathID   string
	cache    string
	address  uint64
	byteSize uint64
	pid      uint64
	op       string

	parents       []ObservationParent
	parentDone    map[string]struct{}
	leaderPathID  string
	l1Role        string
	l2Role        string
	l1Result      string
	l2Result      string
	source        string
	route         string
	remote        bool
	status        string
	componentPath []string

	startPS      uint64
	endPS        uint64
	lastPS       uint64
	currentStage string
	stagePS      map[string]uint64
	events       []observationEvent
	eventSet     map[string]struct{}
	messageIDs   map[string]string

	duplicateEvents uint64
	timeRegressions uint64
}

type observationValidation struct {
	startCalls             uint64
	duplicateStartCalls    uint64
	measuredStarts         uint64
	completed              uint64
	abortedAtDump          uint64
	duplicateEvents        uint64
	timeRegressions        uint64
	duplicateMessageLinks  uint64
	parentResponses        uint64
	duplicateParentRsp     uint64
	unknownParentRsp       uint64
	accountingMismatches   uint64
	completionWithoutParts uint64
	missingReadSource      uint64
	l1FollowerOwnedLower   uint64
	l2FollowerOwnedDRAM    uint64
	remoteMissingNetwork   uint64
	localOwnedNetwork      uint64
	l2HitOwnedDRAM         uint64
}

type observationStats struct {
	sync.Mutex

	enabled bool
	stopped bool
	prefix  string

	warmupAccesses uint64
	maxRecords     uint64
	exitOnFull     bool
	doneCallback   func()
	doneNotified   bool

	observed       uint64
	completedRows  uint64
	maxSeenPS      uint64
	paths          map[string]*observationPath
	messageToPaths map[string]map[string]struct{}

	file *os.File
	gzip *gzip.Writer
	csv  *csv.Writer

	validation  observationValidation
	routeCount  map[string]uint64
	sourceCount map[string]uint64
	stageSumPS  map[string]uint64
	stageCount  map[string]uint64
}

var globalObservationStats = newObservationStats()
var observationTraceActive atomic.Bool

func newObservationStats() *observationStats {
	s := &observationStats{}
	s.resetLocked()
	return s
}

func (s *observationStats) resetLocked() {
	s.stopped = false
	s.doneNotified = false
	s.observed = 0
	s.completedRows = 0
	s.maxSeenPS = 0
	s.paths = make(map[string]*observationPath)
	s.messageToPaths = make(map[string]map[string]struct{})
	s.validation = observationValidation{}
	s.routeCount = make(map[string]uint64)
	s.sourceCount = make(map[string]uint64)
	s.stageSumPS = make(map[string]uint64)
	s.stageCount = make(map[string]uint64)
}

func observationPS(now sim.VTimeInSec) uint64 {
	if now <= 0 {
		return 0
	}
	return uint64(math.Round(float64(now) * 1e12))
}

// EnableObservationTrace starts the independent observation tracer. It does
// not enable or consume any state from the legacy memory-path tracer.
func EnableObservationTrace(
	prefix string,
	warmupAccesses uint64,
	maxRecords uint64,
	exitOnFull bool,
	doneCallback func(),
) error {
	observationTraceActive.Store(false)
	s := globalObservationStats
	s.Lock()
	defer s.Unlock()

	if err := s.closeDataLocked(); err != nil {
		return err
	}
	if prefix == "" {
		prefix = "observation"
	}
	path := prefix + "_paths.csv.gz"
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	w := csv.NewWriter(gz)

	s.enabled = true
	s.prefix = prefix
	s.warmupAccesses = warmupAccesses
	s.maxRecords = maxRecords
	s.exitOnFull = exitOnFull
	s.doneCallback = doneCallback
	s.file = f
	s.gzip = gz
	s.csv = w
	s.resetLocked()

	if err := s.csv.Write(observationPathHeader()); err != nil {
		_ = s.closeDataLocked()
		s.enabled = false
		return err
	}
	observationTraceActive.Store(true)
	return nil
}

// DisableObservationTrace closes and discards the current trace state.
func DisableObservationTrace() {
	observationTraceActive.Store(false)
	s := globalObservationStats
	s.Lock()
	defer s.Unlock()
	_ = s.closeDataLocked()
	s.enabled = false
	s.prefix = ""
	s.doneCallback = nil
	s.resetLocked()
}

// ObservationTraceEnabled reports whether the new tracer accepts events.
func ObservationTraceEnabled() bool {
	return observationTraceActive.Load()
}

// StartObservationPath selects a bounded, post-warmup demand-read transaction
// and opens its first exclusive stage. Writes are tracked only as unmeasured
// context so a selected read can still identify an MSHR leader; O4--O6 record
// remote reads and writes separately.
func StartObservationPath(start ObservationPathStart) {
	if !observationTraceActive.Load() {
		return
	}
	s := globalObservationStats
	s.Lock()
	defer s.Unlock()
	if !s.enabled {
		return
	}

	s.validation.startCalls++
	if _, exists := s.paths[start.PathID]; exists {
		s.validation.duplicateStartCalls++
		return
	}
	if s.stopped {
		return
	}
	isDemandRead := strings.EqualFold(start.Operation, "read")
	sequence := uint64(0)
	selected := false
	if isDemandRead {
		s.observed++
		sequence = s.observed
		if s.maxRecords > 0 &&
			sequence > s.warmupAccesses+s.maxRecords {
			return
		}
		selected = sequence > s.warmupAccesses
	}

	parents := append([]ObservationParent(nil), start.Parents...)
	startPS := observationPS(start.StartTime)
	p := &observationPath{
		sequence:     sequence,
		selected:     selected,
		pathID:       start.PathID,
		cache:        start.CacheName,
		address:      start.Address,
		byteSize:     start.ByteSize,
		pid:          start.PID,
		op:           start.Operation,
		parents:      parents,
		parentDone:   make(map[string]struct{}),
		l1Role:       "leader",
		startPS:      startPS,
		lastPS:       startPS,
		currentStage: "l1_coalesce",
		stagePS:      make(map[string]uint64),
		eventSet:     make(map[string]struct{}),
		messageIDs:   make(map[string]string),
	}
	p.events = append(p.events, observationEvent{name: "path_start", ps: startPS})
	p.eventSet["path_start"] = struct{}{}
	s.paths[start.PathID] = p
	if p.selected {
		s.validation.measuredStarts++
	}
	s.seeTimeLocked(startPS)
}

// ObservationTransition closes the path's current stage at now and opens
// nextStage. event must name a one-shot architectural boundary.
func ObservationTransition(pathID, event, nextStage string, now sim.VTimeInSec) {
	if !observationTraceActive.Load() {
		return
	}
	s := globalObservationStats
	s.Lock()
	defer s.Unlock()
	if !s.enabled {
		return
	}
	p := s.paths[pathID]
	if p == nil {
		return
	}
	s.transitionLocked(p, event, nextStage, observationPS(now))
}

// ObservationTransitionByRequest applies a boundary to every logical path
// linked to a physical request/message. The one-to-many mapping also supports
// later passive analysis of deduplicated remote requests.
func ObservationTransitionByRequest(
	requestID, event, nextStage string,
	now sim.VTimeInSec,
) {
	if !observationTraceActive.Load() {
		return
	}
	s := globalObservationStats
	s.Lock()
	defer s.Unlock()
	if !s.enabled {
		return
	}
	pathIDs := s.messageToPaths[requestID]
	if len(pathIDs) == 0 {
		return
	}
	ps := observationPS(now)
	for pathID := range pathIDs {
		if p := s.paths[pathID]; p != nil {
			s.transitionLocked(p, event, nextStage, ps)
		}
	}
}

func (s *observationStats) transitionLocked(
	p *observationPath,
	event, nextStage string,
	ps uint64,
) {
	if event == "" {
		event = "unnamed_transition"
	}
	if _, exists := p.eventSet[event]; exists {
		p.duplicateEvents++
		s.validation.duplicateEvents++
		return
	}
	if ps < p.lastPS {
		p.timeRegressions++
		s.validation.timeRegressions++
		return
	}
	if nextStage == "" {
		nextStage = "unattributed"
	}
	p.stagePS[p.currentStage] += ps - p.lastPS
	p.lastPS = ps
	p.currentStage = nextStage
	p.eventSet[event] = struct{}{}
	p.events = append(p.events, observationEvent{name: event, ps: ps})
	s.seeTimeLocked(ps)
}

func (s *observationStats) seeTimeLocked(ps uint64) {
	if ps > s.maxSeenPS {
		s.maxSeenPS = ps
	}
}

// LinkObservationRequest associates a downstream physical request/message with
// a logical L1 path. role is diagnostic metadata, not an identity source.
func LinkObservationRequest(pathID, requestID, role string) {
	if !observationTraceActive.Load() {
		return
	}
	s := globalObservationStats
	s.Lock()
	defer s.Unlock()
	s.linkRequestLocked(pathID, requestID, role)
}

// LinkObservationRequestFromRequest copies all logical owners of parentID to a
// newly constructed downstream request.
func LinkObservationRequestFromRequest(parentID, requestID, role string) {
	if observationTraceActive.Load() {
		s := globalObservationStats
		s.Lock()
		if s.enabled && requestID != "" {
			for pathID := range s.messageToPaths[parentID] {
				s.linkRequestLocked(pathID, requestID, role)
			}
		}
		s.Unlock()
	}
	linkObservationRemoteRequest(parentID, requestID)
}

func (s *observationStats) linkRequestLocked(pathID, requestID, role string) {
	if !s.enabled || requestID == "" {
		return
	}
	p := s.paths[pathID]
	if p == nil {
		return
	}
	paths := s.messageToPaths[requestID]
	if paths == nil {
		paths = make(map[string]struct{})
		s.messageToPaths[requestID] = paths
	}
	if _, exists := paths[pathID]; exists {
		s.validation.duplicateMessageLinks++
		return
	}
	paths[pathID] = struct{}{}
	p.messageIDs[requestID] = role
}

// MarkObservationL1Result records the lookup outcome without altering time.
func MarkObservationL1Result(pathID, result string) {
	if !observationTraceActive.Load() {
		return
	}
	s := globalObservationStats
	s.Lock()
	defer s.Unlock()
	if p := s.paths[pathID]; p != nil {
		p.l1Result = result
		if strings.Contains(result, "hit") && !strings.Contains(result, "mshr") {
			p.source = "l1"
		}
	}
}

// MarkObservationL2Result records the lookup outcome for a physical request.
func MarkObservationL2Result(requestID, component, result string) {
	if observationTraceActive.Load() {
		s := globalObservationStats
		s.Lock()
		for pathID := range s.messageToPaths[requestID] {
			if p := s.paths[pathID]; p != nil {
				p.l2Result = result
				if p.l2Role == "" {
					p.l2Role = "leader"
				}
				s.appendComponentLocked(p, component)
				if result == "read-hit" || result == "write-hit" {
					p.source = "l2"
					s.propagateLeaderMetadataLocked(pathID, p.source, p.remote)
				}
			}
		}
		s.Unlock()
	}
	markObservationRemoteL2Result(requestID, result)
}

// MarkObservationSource marks the ultimate data source reached by a request.
func MarkObservationSource(requestID, source, component string) {
	if observationTraceActive.Load() {
		s := globalObservationStats
		s.Lock()
		for pathID := range s.messageToPaths[requestID] {
			if p := s.paths[pathID]; p != nil {
				p.source = source
				s.appendComponentLocked(p, component)
				s.propagateLeaderMetadataLocked(pathID, source, p.remote)
			}
		}
		s.Unlock()
	}
	if source == "dram" {
		markObservationRemoteHBMAccess(requestID)
	}
}

// MarkObservationRemote marks paths that cross an RDMA/NoC boundary.
func MarkObservationRemote(requestID string) {
	if !observationTraceActive.Load() {
		return
	}
	s := globalObservationStats
	s.Lock()
	defer s.Unlock()
	for pathID := range s.messageToPaths[requestID] {
		if p := s.paths[pathID]; p != nil {
			p.remote = true
			s.propagateLeaderMetadataLocked(pathID, p.source, true)
		}
	}
}

func (s *observationStats) propagateLeaderMetadataLocked(
	leaderPathID, source string,
	remote bool,
) {
	for _, follower := range s.paths {
		if !observationReferencesLeader(follower, leaderPathID) {
			continue
		}
		if follower.source == "" && source != "" {
			follower.source = source
		}
		// An L1 MSHR follower represents the same requester-side access as
		// its leader and therefore inherits the leader's route. An L2 MSHR
		// follower is a distinct access that merely shares the lower-memory
		// transaction. A local owner access can join an L2 miss created by a
		// remote requester, so it must not inherit the L2 leader's route.
		if follower.l1Role == "mshr_follower" {
			follower.remote = follower.remote || remote
		}
	}
}

func (s *observationStats) appendComponentLocked(
	p *observationPath,
	component string,
) {
	if component == "" {
		return
	}
	if len(p.componentPath) == 0 || p.componentPath[len(p.componentPath)-1] != component {
		p.componentPath = append(p.componentPath, component)
	}
}

// MarkObservationL1MSHRFollower records that this path owns no downstream
// physical request and waits on another L1 path instead.
func MarkObservationL1MSHRFollower(
	pathID, leaderPathID string,
	now sim.VTimeInSec,
) {
	if !observationTraceActive.Load() {
		return
	}
	s := globalObservationStats
	s.Lock()
	defer s.Unlock()
	p := s.paths[pathID]
	if p == nil {
		return
	}
	p.l1Role = "mshr_follower"
	p.leaderPathID = leaderPathID
	p.l1Result = "read-mshr-hit"
	if leader := s.paths[leaderPathID]; leader != nil {
		p.source = leader.source
		p.remote = leader.remote
	}
	s.transitionLocked(p, "l1_mshr_join", "l1_mshr_wait", observationPS(now))
}

// MarkObservationL2MSHRFollower makes the L2 wait explicit without assigning
// the leader's DRAM work to the follower.
func MarkObservationL2MSHRFollower(
	requestID, leaderRequestID string,
	now sim.VTimeInSec,
) {
	if !observationTraceActive.Load() {
		return
	}
	s := globalObservationStats
	s.Lock()
	defer s.Unlock()
	for pathID := range s.messageToPaths[requestID] {
		p := s.paths[pathID]
		if p == nil {
			continue
		}
		p.l2Result = "read-mshr-hit"
		p.l2Role = "mshr_follower"
		if leaders := s.messageToPaths[leaderRequestID]; len(leaders) > 0 {
			leaderIDs := make([]string, 0, len(leaders))
			for id := range leaders {
				leaderIDs = append(leaderIDs, id)
			}
			sort.Strings(leaderIDs)
			p.leaderPathID = strings.Join(leaderIDs, ";")
			for _, leaderID := range leaderIDs {
				if leader := s.paths[leaderID]; leader != nil {
					if p.source == "" {
						p.source = leader.source
					}
				}
			}
		}
		s.transitionLocked(p, "l2_mshr_join", "l2_mshr_wait", observationPS(now))
	}
}

// ObservationParentResponded records the CU-visible response send. The path is
// terminal only after every merged parent has received a response.
func ObservationParentResponded(
	pathID, parentID string,
	now sim.VTimeInSec,
) {
	if !observationTraceActive.Load() {
		return
	}
	s := globalObservationStats
	s.Lock()
	if !s.enabled {
		s.Unlock()
		return
	}
	p := s.paths[pathID]
	if p == nil {
		s.Unlock()
		return
	}
	if _, exists := p.parentDone[parentID]; exists {
		s.validation.duplicateParentRsp++
		s.Unlock()
		return
	}
	if !observationHasParent(p, parentID) {
		s.validation.unknownParentRsp++
		s.Unlock()
		return
	}
	ps := observationPS(now)
	if len(p.parentDone) == 0 {
		s.transitionLocked(p, "l1_first_parent_response", "l1_response_fanout", ps)
	}
	p.parentDone[parentID] = struct{}{}
	s.validation.parentResponses++
	if len(p.parentDone) < len(p.parents) {
		s.Unlock()
		return
	}
	callback := s.completeLocked(p, "complete", ps)
	s.Unlock()
	if callback != nil {
		go callback()
	}
}

func observationHasParent(p *observationPath, parentID string) bool {
	for _, parent := range p.parents {
		if parent.ID == parentID {
			return true
		}
	}
	return false
}

func (s *observationStats) completeLocked(
	p *observationPath,
	status string,
	ps uint64,
) func() {
	if ps < p.lastPS {
		p.timeRegressions++
		s.validation.timeRegressions++
		ps = p.lastPS
	}
	p.stagePS[p.currentStage] += ps - p.lastPS
	p.lastPS = ps
	p.endPS = ps
	p.status = status
	p.events = append(p.events, observationEvent{name: "path_complete", ps: ps})
	s.seeTimeLocked(ps)

	if p.route == "" {
		if p.remote {
			p.route = "remote"
		} else if p.source == "l1" {
			p.route = "l1_hit"
		} else {
			p.route = "local"
		}
	}
	if len(p.parentDone) != len(p.parents) && status == "complete" {
		s.validation.completionWithoutParts++
	}
	if observationAccountedPS(p) != p.endPS-p.startPS {
		s.validation.accountingMismatches++
	}
	s.validateCompletedPathLocked(p, status)
	for _, follower := range s.paths {
		if follower == p || !observationReferencesLeader(follower, p.pathID) {
			continue
		}
		if follower.source == "" {
			follower.source = p.source
		}
		if follower.l1Role == "mshr_follower" {
			follower.remote = follower.remote || p.remote
		}
	}

	if !p.selected {
		s.deletePathLocked(p)
		return nil
	}

	if s.csv != nil {
		if err := s.csv.Write(observationPathRow(p)); err != nil {
			panic(fmt.Errorf("write observation path: %w", err))
		}
	}
	s.completedRows++
	if status == "complete" {
		s.validation.completed++
	} else {
		s.validation.abortedAtDump++
	}
	s.routeCount[p.route]++
	s.sourceCount[p.source]++
	for stage, duration := range p.stagePS {
		s.stageSumPS[stage] += duration
		if duration > 0 {
			s.stageCount[stage]++
		}
	}
	s.deletePathLocked(p)

	if s.maxRecords > 0 && s.validation.completed >= s.maxRecords {
		s.stopped = true
		observationTraceActive.Store(false)
		if s.exitOnFull && !s.doneNotified && s.doneCallback != nil {
			s.doneNotified = true
			return s.doneCallback
		}
	}
	return nil
}

func observationReferencesLeader(
	follower *observationPath,
	leaderPathID string,
) bool {
	if follower == nil || leaderPathID == "" {
		return false
	}
	for _, candidate := range strings.Split(follower.leaderPathID, ";") {
		if candidate == leaderPathID {
			return true
		}
	}
	return false
}

func (s *observationStats) validateCompletedPathLocked(
	p *observationPath,
	status string,
) {
	if status != "complete" {
		return
	}
	// Followers do not own a complete physical data path. Their source is
	// determined by the leader and may become known after the follower has
	// already responded, so require a source only for physical leaders.
	if strings.EqualFold(p.op, "read") && p.source == "" &&
		p.l1Role != "mshr_follower" && p.l2Role != "mshr_follower" {
		s.validation.missingReadSource++
	}
	lowerStages := []string{
		"l1_downstream", "requester_rdma", "remote_request_network",
		"owner_rdma_request", "owner_l2_link", "l2_queue", "l2_lookup",
		"l2_mshr_wait", "l2_bank", "l2_write_buffer", "l2_to_dram",
		"dram_queue_service", "dram_to_l2", "l2_fill_response",
		"owner_rdma_response", "remote_response_network",
		"requester_rdma_response", "l2_response_link", "l2_to_l1",
	}
	if p.l1Role == "mshr_follower" && observationAnyStage(p, lowerStages) {
		s.validation.l1FollowerOwnedLower++
	}
	dramStages := []string{"l2_to_dram", "dram_queue_service", "dram_to_l2"}
	if p.l2Role == "mshr_follower" && observationAnyStage(p, dramStages) {
		s.validation.l2FollowerOwnedDRAM++
	}
	requestNetwork := p.stagePS["remote_request_network"]
	responseNetwork := p.stagePS["remote_response_network"]
	// An L1 MSHR follower inherits the leader's remote source classification,
	// but deliberately owns only its local MSHR wait and response fanout. The
	// physical leader, not the follower, owns both network traversals.
	if p.remote && p.l1Role != "mshr_follower" &&
		p.l2Role != "mshr_follower" &&
		(requestNetwork == 0 || responseNetwork == 0) {
		s.validation.remoteMissingNetwork++
	}
	if !p.remote && (requestNetwork != 0 || responseNetwork != 0) {
		s.validation.localOwnedNetwork++
	}
	if p.source == "l2" && observationAnyStage(p, dramStages) {
		s.validation.l2HitOwnedDRAM++
	}
}

func observationAnyStage(p *observationPath, stages []string) bool {
	for _, stage := range stages {
		if p.stagePS[stage] != 0 {
			return true
		}
	}
	return false
}

func observationAccountedPS(p *observationPath) uint64 {
	var total uint64
	for _, duration := range p.stagePS {
		total += duration
	}
	return total
}

func (s *observationStats) deletePathLocked(p *observationPath) {
	delete(s.paths, p.pathID)
	for requestID := range p.messageIDs {
		paths := s.messageToPaths[requestID]
		delete(paths, p.pathID)
		if len(paths) == 0 {
			delete(s.messageToPaths, requestID)
		}
	}
}

func observationPathHeader() []string {
	header := []string{
		"schema_version", "sequence", "path_id", "leader_path_id",
		"l1_role", "l2_role", "l1_cache", "pid", "address", "bytes", "operation",
		"parent_count", "parent_ids", "start_ps", "end_ps", "total_ps",
		"accounted_ps", "residual_ps", "route", "remote", "source",
		"l1_result", "l2_result", "status", "component_path",
		"physical_messages", "events", "duplicate_events", "time_regressions",
	}
	for _, stage := range observationStageNames {
		header = append(header, stage+"_ps")
	}
	return header
}

func observationPathRow(p *observationPath) []string {
	parentIDs := make([]string, 0, len(p.parents))
	for _, parent := range p.parents {
		parentIDs = append(parentIDs, parent.ID)
	}
	messageIDs := make([]string, 0, len(p.messageIDs))
	for id, role := range p.messageIDs {
		messageIDs = append(messageIDs, role+"="+id)
	}
	sort.Strings(messageIDs)
	events := make([]string, 0, len(p.events))
	for _, event := range p.events {
		events = append(events, event.name+"@"+strconv.FormatUint(event.ps, 10))
	}
	total := p.endPS - p.startPS
	accounted := observationAccountedPS(p)
	residual := uint64(0)
	if total > accounted {
		residual = total - accounted
	}
	row := []string{
		"observation-path-v1",
		strconv.FormatUint(p.sequence, 10), p.pathID, p.leaderPathID,
		p.l1Role, p.l2Role, p.cache, strconv.FormatUint(p.pid, 10),
		strconv.FormatUint(p.address, 10), strconv.FormatUint(p.byteSize, 10), p.op,
		strconv.Itoa(len(p.parents)), strings.Join(parentIDs, ";"),
		strconv.FormatUint(p.startPS, 10), strconv.FormatUint(p.endPS, 10),
		strconv.FormatUint(total, 10), strconv.FormatUint(accounted, 10),
		strconv.FormatUint(residual, 10), p.route, strconv.FormatBool(p.remote),
		p.source, p.l1Result, p.l2Result, p.status,
		strings.Join(p.componentPath, ";"), strings.Join(messageIDs, ";"),
		strings.Join(events, ";"), strconv.FormatUint(p.duplicateEvents, 10),
		strconv.FormatUint(p.timeRegressions, 10),
	}
	for _, stage := range observationStageNames {
		row = append(row, strconv.FormatUint(p.stagePS[stage], 10))
	}
	return row
}

// DumpObservationTrace writes aggregate and invariant files and closes the
// compressed raw stream. Live paths are emitted as explicit incomplete rows.
func DumpObservationTrace() error {
	observationTraceActive.Store(false)
	s := globalObservationStats
	s.Lock()
	defer s.Unlock()
	if !s.enabled {
		return nil
	}

	live := make([]*observationPath, 0, len(s.paths))
	for _, p := range s.paths {
		live = append(live, p)
	}
	sort.Slice(live, func(i, j int) bool { return live[i].sequence < live[j].sequence })
	for _, p := range live {
		s.completeLocked(p, "incomplete_at_dump", s.maxSeenPS)
	}
	if err := s.closeDataLocked(); err != nil {
		return err
	}
	if err := s.dumpSummaryLocked(); err != nil {
		return err
	}
	if err := s.dumpValidationLocked(); err != nil {
		return err
	}
	s.enabled = false
	return nil
}

func (s *observationStats) closeDataLocked() error {
	var firstErr error
	if s.csv != nil {
		s.csv.Flush()
		if err := s.csv.Error(); err != nil {
			firstErr = err
		}
		s.csv = nil
	}
	if s.gzip != nil {
		if err := s.gzip.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.gzip = nil
	}
	if s.file != nil {
		if err := s.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		s.file = nil
	}
	return firstErr
}

func (s *observationStats) dumpSummaryLocked() error {
	f, err := os.Create(s.prefix + "_summary.csv")
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write([]string{"kind", "name", "accesses", "total_ps", "average_ps"}); err != nil {
		return err
	}
	for _, stage := range observationStageNames {
		count := s.stageCount[stage]
		total := s.stageSumPS[stage]
		avg := float64(0)
		if count > 0 {
			avg = float64(total) / float64(count)
		}
		_ = w.Write([]string{"stage", stage, strconv.FormatUint(count, 10),
			strconv.FormatUint(total, 10), strconv.FormatFloat(avg, 'f', 3, 64)})
	}
	writeCounts := func(kind string, counts map[string]uint64) {
		keys := make([]string, 0, len(counts))
		for key := range counts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			_ = w.Write([]string{kind, key, strconv.FormatUint(counts[key], 10), "", ""})
		}
	}
	writeCounts("route", s.routeCount)
	writeCounts("source", s.sourceCount)
	w.Flush()
	return w.Error()
}

func (s *observationStats) dumpValidationLocked() error {
	f, err := os.Create(s.prefix + "_validation.csv")
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"invariant", "value", "pass"})
	v := s.validation
	rows := []struct {
		name  string
		value uint64
		zero  bool
	}{
		{"start_calls", v.startCalls, false},
		{"observed_demand_reads", s.observed, false},
		{"measured_starts", v.measuredStarts, false},
		{"completed", v.completed, false},
		{"incomplete_at_dump", v.abortedAtDump, true},
		{"duplicate_start_calls", v.duplicateStartCalls, true},
		{"duplicate_events", v.duplicateEvents, true},
		{"time_regressions", v.timeRegressions, true},
		{"duplicate_message_links", v.duplicateMessageLinks, true},
		{"parent_responses", v.parentResponses, false},
		{"duplicate_parent_responses", v.duplicateParentRsp, true},
		{"unknown_parent_responses", v.unknownParentRsp, true},
		{"accounting_mismatches", v.accountingMismatches, true},
		{"completion_without_all_parents", v.completionWithoutParts, true},
		{"missing_read_source", v.missingReadSource, true},
		{"l1_mshr_follower_owned_lower_path", v.l1FollowerOwnedLower, true},
		{"l2_mshr_follower_owned_dram", v.l2FollowerOwnedDRAM, true},
		{"remote_path_missing_network", v.remoteMissingNetwork, true},
		{"local_path_owned_network", v.localOwnedNetwork, true},
		{"l2_hit_owned_dram", v.l2HitOwnedDRAM, true},
	}
	for _, row := range rows {
		pass := true
		if row.zero {
			pass = row.value == 0
		}
		_ = w.Write([]string{row.name, strconv.FormatUint(row.value, 10), strconv.FormatBool(pass)})
	}
	terminal := v.completed + v.abortedAtDump
	_ = w.Write([]string{"started_equals_terminal", strconv.FormatUint(terminal, 10),
		strconv.FormatBool(terminal == v.measuredStarts)})
	_ = w.Write([]string{"live_paths_after_dump", strconv.Itoa(len(s.paths)),
		strconv.FormatBool(len(s.paths) == 0)})
	_ = w.Write([]string{"live_message_links_after_dump", strconv.Itoa(len(s.messageToPaths)),
		strconv.FormatBool(len(s.messageToPaths) == 0)})
	w.Flush()
	return w.Error()
}
