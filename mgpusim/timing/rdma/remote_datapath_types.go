package rdma

import (
	"math/bits"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

const (
	remoteLineBytes     = uint64(64)
	remotePageBytes     = uint64(4096)
	bitmapReqTraffic    = 20
	bitmapRspOverhead   = 4
	flushReasonFull     = "full"
	flushReasonIssue    = "work-conserving"
	flushReasonCapacity = "capacity"
	flushReasonConflict = "conflict"
	flushReasonDrain    = "drain"
)

// RemoteDataPathConfig controls requester-side exact deduplication, remote
// read batching, and requester-L2 remote replicas. Negative subfeature flags
// allow clean standalone ablations while preserving the full mechanism for
// existing zero-value callers with Enabled set. The whole mechanism is
// disabled by default.
type RemoteDataPathConfig struct {
	Enabled                  bool
	DisableDedup             bool
	DisableBatching          bool
	DisableRequesterL2       bool
	EnableFilterPrefetch     bool
	EnableAuthoritativeAudit bool
	PrefetchEntries          int
	MaxBatchLines            int
	MaxBatches               int
}

func normalizeRemoteDataPathConfig(c RemoteDataPathConfig) RemoteDataPathConfig {
	if c.PrefetchEntries <= 0 {
		c.PrefetchEntries = 64
	}
	if c.MaxBatchLines <= 0 || c.MaxBatchLines > 64 {
		c.MaxBatchLines = 8
	}
	if c.MaxBatches <= 0 {
		c.MaxBatches = 64
	}
	return c
}

// RemoteDataPathStats contains the small set of counters needed to separate
// batching, exact deduplication, L2 reuse, and fill admission effects.
type RemoteDataPathStats struct {
	Enabled                                bool
	DedupEnabled                           bool
	BatchingEnabled                        bool
	RequesterL2Enabled                     bool
	FilterPrefetchEnabled                  bool
	AuthoritativeAuditEnabled              bool
	MaxBatchLines                          uint64
	MaxBatches                             uint64
	LineEntryCapacity                      uint64
	PeakLineEntries                        uint64
	LineEntryFullStalls                    uint64
	WaiterEntryCapacity                    uint64
	PeakWaiterEntries                      uint64
	WaiterEntryFullStalls                  uint64
	OwnerChildLineCapacity                 uint64
	OwnerPeakChildLines                    uint64
	OwnerChildLineFullStalls               uint64
	ObservedRemoteReads                    uint64
	ObservedRemoteWrites                   uint64
	LogicalRemoteReads                     uint64
	WireLines                              uint64
	DemandWireLines                        uint64
	DuplicateReads                         uint64
	InflightFilterQueries                  uint64
	InflightFilterPositives                uint64
	InflightFilterNegatives                uint64
	InflightFilterFalsePositives           uint64
	InflightFilterInsertFailures           uint64
	PendingAuthoritativeChecks             uint64
	PendingVerifiedSafeBypasses            uint64
	PendingAuthoritativeFalseNegatives     uint64
	ExactTableLookups                      uint64
	ExactTableLookupsAvoided               uint64
	CollectingMerges                       uint64
	InflightMerges                         uint64
	ReadyMerges                            uint64
	L2ProbeHits                            uint64
	L2ProbeMisses                          uint64
	RequesterL2FilterNegativeDecisions     uint64
	L2OneTouchProbeBypasses                uint64
	RequesterL2AuthoritativeChecks         uint64
	RequesterL2VerifiedSafeBypasses        uint64
	RequesterL2AuthoritativeFalseNegatives uint64
	RequesterL2AuthoritativeUnavailable    uint64
	ReuseWriteUncacheableSkips             uint64
	L2LogicalResponses                     uint64
	SingleReadPackets                      uint64
	BitmapPackets                          uint64
	BitmapLines                            uint64
	BitmapResponsePackets                  uint64
	BitmapResponseLines                    uint64
	EarlyBitmapResponses                   uint64
	BatchSizeHistogram                     [65]uint64
	TwoTouchCandidates                     uint64
	ResidentQueries                        uint64
	ResidentPositives                      uint64
	ResidentNegatives                      uint64
	SeenQueries                            uint64
	SeenHits                               uint64
	SeenNegatives                          uint64
	SeenFalsePositives                     uint64
	SeenInsertFailures                     uint64
	FirstTouchRemoteLines                  uint64
	SecondTouchAdmissions                  uint64
	MultipleDemandAdmissions               uint64
	TwoTouchFillAttempts                   uint64
	TwoTouchInstalledFills                 uint64
	FanoutResponses                        uint64
	NetworkRequestBytes                    uint64
	NetworkResponseBytes                   uint64
	BatchQueueWaitSamples                  uint64
	BatchQueueWaitTotalNS                  float64
	BatchQueueWaitMaxNS                    float64
	PreNetworkWaitSamples                  uint64
	PreNetworkWaitTotalNS                  float64
	PreNetworkWaitMaxNS                    float64
	ProbeLatencySamples                    uint64
	ProbeLatencyTotalNS                    float64
	ProbeLatencyMaxNS                      float64
	LogicalReadLatencyTotalNS              float64
	LogicalReadLatencyMaxNS                float64
	FullFlushes                            uint64
	WorkConservingFlushes                  uint64
	CapacityFlushes                        uint64
	ConflictFlushes                        uint64
	DrainFlushes                           uint64
	RequesterIssueWidthStalls              uint64
	ResponseFanoutWidthStalls              uint64
	OwnerIssueWidthStalls                  uint64
	OwnerResponseWidthStalls               uint64
	PrefetchRealDemands                    uint64
	PrefetchCandidates                     uint64
	PrefetchPatternInstalls                uint64
	PrefetchPatternInstallDrops            uint64
	PrefetchFilterDrops                    uint64
	PrefetchSameGroupDrops                 uint64
	PrefetchCapacityDrops                  uint64
	PrefetchNoExistingBatchDrops           uint64
	PrefetchBatchFullDrops                 uint64
	PrefetchPiggybackLines                 uint64
	PrefetchWireLines                      uint64
	PrefetchUseful                         uint64
	PrefetchUnused                         uint64
	PrefetchStandalonePrevented            uint64
	PrefetchPredictor                      writeback.DemandStridePredictorStats
}

// BitmapReadReq names multiple 64B cache lines in one remote 4KiB page.
type BitmapReadReq struct {
	sim.MsgMeta
	PID        vm.PID
	PagePAddr  uint64
	LineBitmap uint64
	Info       interface{}
}

// Meta returns message metadata.
func (r *BitmapReadReq) Meta() *sim.MsgMeta { return &r.MsgMeta }

// BitmapReadRsp returns one data block for each requested bitmap bit.
type BitmapReadRsp struct {
	sim.MsgMeta
	RespondTo string
	LineData  map[uint64][]byte
}

// Meta returns message metadata.
func (r *BitmapReadRsp) Meta() *sim.MsgMeta { return &r.MsgMeta }

// GetRspTo identifies the BitmapReadReq being completed.
func (r *BitmapReadRsp) GetRspTo() string { return r.RespondTo }

type remoteLineIdentity struct {
	ownerName string
	pid       vm.PID
	lineAddr  uint64
}

type remoteLineKey struct {
	remoteLineIdentity
	epoch uint64
}

type remoteLineState uint8

const (
	remoteLineProbing remoteLineState = iota
	remoteLinePendingBatch
	remoteLineCollecting
	remoteLineInflight
	remoteLineReady
)

type remoteWaiter struct {
	req       *mem.ReadReq
	arrival   sim.VTimeInSec
	firstSeen sim.VTimeInSec
}

type remoteLineEntry struct {
	key                     remoteLineKey
	owner                   sim.Port
	state                   remoteLineState
	waiters                 []remoteWaiter
	data                    []byte
	batch                   *remoteBatch
	admit                   bool
	fromRemote              bool
	queuedReady             bool
	fillInflight            bool
	fillComplete            bool
	multipleDemandAdmission bool
	seenAdmission           bool
	fillInstalled           bool
	replicaGeneration       uint64
	info                    interface{}
	speculative             bool
	speculativeUseful       bool
	patternToken            writeback.PatternToken
	patternKey              writeback.TypedFilterKey
	prefetchCandidate       *remotePrefetchCandidate
}

type remotePrefetchCandidate struct {
	identity   remoteLineIdentity
	owner      sim.Port
	token      writeback.PatternToken
	patternKey writeback.TypedFilterKey
	lookups    [4]writeback.TypedFilterLookup
}

type remoteBatchKey struct {
	ownerName string
	pid       vm.PID
	pageAddr  uint64
}

type remoteBatch struct {
	key        remoteBatchKey
	dst        sim.Port
	lineBitmap uint64
	lineOrder  []uint64
	// A page has exactly 64 cache lines. A fixed index avoids allocating and
	// hashing a small map for every requester batch.
	lines     [64]*remoteLineEntry
	oldest    sim.VTimeInSec
	createdAt sim.VTimeInSec
	info      interface{}
	// responseBitmap tracks partial owner responses. A slow cache line must
	// not hold back other independent 64-B lines already returned by owner L2.
	responseBitmap uint64
}

func (b *remoteBatch) lineCount() int {
	return bits.OnesCount64(b.lineBitmap)
}

type remoteProbe struct {
	entry *remoteLineEntry
	req   *mem.ReadReq
	sent  sim.VTimeInSec
}

// authoritativeL2LookupOnly is a simulator-only, timing-neutral audit
// interface. The normal requester path never uses this result unless it finds
// a dangerous Filter false negative, in which case it conservatively restores
// the exact requester-L2 probe.
type authoritativeL2LookupOnly interface {
	AuthoritativeLookupOnlyHit(pid vm.PID, address uint64) bool
}

var _ authoritativeL2LookupOnly = (*writeback.Cache)(nil)

type remoteOwnerBatch struct {
	req         *BitmapReadReq
	remaining   int
	readyData   map[uint64][]byte
	readyQueued bool
}

type remoteOwnerSubReq struct {
	batch *remoteOwnerBatch
	line  uint64
	read  *mem.ReadReq
}

func remotePageAddress(addr uint64) uint64 {
	return addr & ^(remotePageBytes - 1)
}

func remoteLineAddress(addr uint64) uint64 {
	return addr & ^(remoteLineBytes - 1)
}

func remoteLineOffset(addr uint64) uint64 {
	return (remoteLineAddress(addr) - remotePageAddress(addr)) / remoteLineBytes
}

func remoteReadBatchable(req *mem.ReadReq) bool {
	return req != nil && req.Address%remoteLineBytes == 0 &&
		req.AccessByteSize == remoteLineBytes &&
		remotePageAddress(req.Address) ==
			remotePageAddress(req.Address+req.AccessByteSize-1)
}

func bitmapLines(bitmap uint64) []uint64 {
	lines := make([]uint64, 0, bits.OnesCount64(bitmap))
	for line := uint64(0); line < 64; line++ {
		if bitmap&(uint64(1)<<line) != 0 {
			lines = append(lines, line)
		}
	}
	return lines
}
