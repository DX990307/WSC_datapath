package rdma

import (
	"container/list"
	"math/bits"

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
	Enabled            bool
	AUPrefetch         bool
	DisableDedup       bool
	DisableBatching    bool
	DisableRequesterL2 bool
	MaxBatchLines      int
	MaxWaitNS          uint64
	MaxBatches         int
	ReuseTableEntries  int
}

func normalizeRemoteDataPathConfig(c RemoteDataPathConfig) RemoteDataPathConfig {
	if c.MaxBatchLines <= 0 || c.MaxBatchLines > 64 {
		c.MaxBatchLines = 8
	}
	if c.MaxBatches <= 0 {
		c.MaxBatches = 64
	}
	if c.ReuseTableEntries <= 0 {
		c.ReuseTableEntries = 4096
	}
	// Remote batching is work-conserving. Keep MaxWaitNS in the public
	// configuration for command-line compatibility, but never delay a ready
	// batch in the hope that a future request will join it.
	c.MaxWaitNS = 0
	if c.DisableBatching || c.DisableRequesterL2 {
		c.AUPrefetch = false
	}
	return c
}

// RemoteDataPathStats contains the small set of counters needed to separate
// batching, exact deduplication, L2 reuse, and fill admission effects.
type RemoteDataPathStats struct {
	Enabled                   bool
	AUPrefetchEnabled         bool
	DedupEnabled              bool
	BatchingEnabled           bool
	RequesterL2Enabled        bool
	MaxBatchLines             uint64
	MaxWaitNS                 uint64
	MaxBatches                uint64
	ReuseTableEntries         uint64
	LogicalRemoteReads        uint64
	WireLines                 uint64
	DemandWireLines           uint64
	PrefetchWireLines         uint64
	DuplicateReads            uint64
	CollectingMerges          uint64
	InflightMerges            uint64
	ReadyMerges               uint64
	L2ProbeHits               uint64
	L2ProbeMisses             uint64
	L2LogicalResponses        uint64
	SingleReadPackets         uint64
	BitmapPackets             uint64
	BitmapLines               uint64
	BatchSizeHistogram        [65]uint64
	AUPrefetchCandidates      uint64
	AUPrefetchConvertedDemand uint64
	AUPrefetchDemandMerges    uint64
	TwoTouchCandidates        uint64
	TwoTouchFillAttempts      uint64
	PrefetchFillAttempts      uint64
	TwoTouchInstalledFills    uint64
	PrefetchInstalledFills    uint64
	FanoutResponses           uint64
	NetworkRequestBytes       uint64
	NetworkResponseBytes      uint64
	BatchQueueWaitSamples     uint64
	BatchQueueWaitTotalNS     float64
	BatchQueueWaitMaxNS       float64
	PreNetworkWaitSamples     uint64
	PreNetworkWaitTotalNS     float64
	PreNetworkWaitMaxNS       float64
	ProbeLatencySamples       uint64
	ProbeLatencyTotalNS       float64
	ProbeLatencyMaxNS         float64
	LogicalReadLatencyTotalNS float64
	LogicalReadLatencyMaxNS   float64
	FullFlushes               uint64
	WorkConservingFlushes     uint64
	TimeoutFlushes            uint64
	CapacityFlushes           uint64
	ConflictFlushes           uint64
	DrainFlushes              uint64
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
	key               remoteLineKey
	owner             sim.Port
	state             remoteLineState
	waiters           []remoteWaiter
	data              []byte
	batch             *remoteBatch
	wasPrefetch       bool
	admit             bool
	fromRemote        bool
	queuedReady       bool
	fillInflight      bool
	fillComplete      bool
	replicaGeneration uint64
	info              interface{}
}

type remoteBatchKey struct {
	ownerName string
	pid       vm.PID
	pageAddr  uint64
}

type remoteBatch struct {
	key            remoteBatchKey
	dst            sim.Port
	lineBitmap     uint64
	prefetchBitmap uint64
	lineOrder      []uint64
	lines          map[uint64]*remoteLineEntry
	oldest         sim.VTimeInSec
	createdAt      sim.VTimeInSec
	info           interface{}
}

func (b *remoteBatch) lineCount() int {
	return bits.OnesCount64(b.lineBitmap)
}

type remoteProbe struct {
	entry *remoteLineEntry
	req   *mem.ReadReq
	sent  sim.VTimeInSec
}

type remoteOwnerBatch struct {
	req       *BitmapReadReq
	remaining int
	lineData  map[uint64][]byte
}

type remoteOwnerSubReq struct {
	batch *remoteOwnerBatch
	line  uint64
	read  *mem.ReadReq
}

type remoteReuseRecord struct {
	count uint8
	elem  *list.Element
}

type remoteReuseTable struct {
	capacity int
	records  map[remoteLineIdentity]*remoteReuseRecord
	lru      *list.List
}

func newRemoteReuseTable(capacity int) *remoteReuseTable {
	return &remoteReuseTable{
		capacity: capacity,
		records:  make(map[remoteLineIdentity]*remoteReuseRecord),
		lru:      list.New(),
	}
}

func (t *remoteReuseTable) Touch(key remoteLineIdentity) uint8 {
	if record := t.records[key]; record != nil {
		if record.count < 2 {
			record.count++
		}
		t.lru.MoveToBack(record.elem)
		return record.count
	}

	if len(t.records) >= t.capacity {
		front := t.lru.Front()
		if front != nil {
			old := front.Value.(remoteLineIdentity)
			delete(t.records, old)
			t.lru.Remove(front)
		}
	}
	elem := t.lru.PushBack(key)
	t.records[key] = &remoteReuseRecord{count: 1, elem: elem}
	return 1
}

func (t *remoteReuseTable) Delete(key remoteLineIdentity) {
	record := t.records[key]
	if record == nil {
		return
	}
	delete(t.records, key)
	t.lru.Remove(record.elem)
}

func (t *remoteReuseTable) Reset() {
	clear(t.records)
	t.lru.Init()
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
