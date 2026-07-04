package rdma

import (
	"math/bits"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

const (
	m2DefaultLineBytes       = uint64(64)
	m2DefaultPageBytes       = uint64(4096)
	m2BitmapRequestOverhead  = 20
	m2BitmapResponseOverhead = 4
	m2FlushReasonFull        = "full"
	m2FlushReasonTimeout     = "timeout"
	m2FlushReasonCapacity    = "capacity"
	m2FlushReasonConflict    = "conflict"
	m2FlushReasonDrain       = "drain"
	m3SelectFIFO             = "fifo"
	m3SelectDRR              = "drr"
	m3SelectHardAge          = "hard_age"
	m3SelectFallbackOldest   = "fallback_oldest"
)

// BitmapReadReq is an RDMA-private request that represents multiple 64B
// remote cache-line reads within one page.
type BitmapReadReq struct {
	sim.MsgMeta

	BatchID              uint64
	RequesterName        string
	OwnerName            string
	PID                  vm.PID
	PagePAddr            uint64
	LineBitmap           uint64
	LineCount            int
	LineSize             uint64
	OriginalRequestCount int
	Info                 interface{}
}

// Meta returns the message metadata.
func (r *BitmapReadReq) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// BitmapReadRsp returns all cache lines requested by a BitmapReadReq.
type BitmapReadRsp struct {
	sim.MsgMeta

	BatchID       uint64
	RequesterName string
	OwnerName     string
	PID           vm.PID
	PagePAddr     uint64
	LineBitmap    uint64
	LineCount     int
	LineSize      uint64
	LineData      map[uint64][]byte
	RespondTo     string
}

// Meta returns the message metadata.
func (r *BitmapReadRsp) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// GetRspTo returns the bitmap request ID.
func (r *BitmapReadRsp) GetRspTo() string {
	return r.RespondTo
}

// M2Stats summarizes requester-side bitmap batching and owner-side bitmap
// service. The mechanism is disabled by default, so all counters stay zero for
// baseline runs.
type M2Stats struct {
	Enabled bool

	OriginalRemoteRequests uint64
	BatchableRemoteReads   uint64
	BypassRequests         uint64

	BatchesCreated           uint64
	BatchesFlushed           uint64
	BatchedPackets           uint64
	LinesInBatchedPackets    uint64
	RequestsInBatchedPackets uint64
	DuplicateLineReads       uint64
	AUPrefetchEnabled        bool
	AUPrefetchLinesRequested uint64
	AUPrefetchLinesReturned  uint64
	AUPrefetchDemandHits     uint64

	FullFlushes     uint64
	TimeoutFlushes  uint64
	CapacityFlushes uint64
	ConflictFlushes uint64
	DrainFlushes    uint64

	MaxBatchLines       uint64
	MaxRequestsPerBatch uint64
	TotalWaitNS         float64
	WaitSamples         uint64

	OwnerBatchRequests     uint64
	OwnerLocalReadReqs     uint64
	OwnerBatchResponses    uint64
	RequesterBatchRsps     uint64
	RequesterUnbatchRsps   uint64
	RequesterPendingRspMax uint64
}

// M3Stats summarizes owner-side fair service queue behavior.
type M3Stats struct {
	Enabled bool

	OwnerReceivedPackets uint64
	OwnerServedPackets   uint64
	OwnerServedLines     uint64

	ConsecutiveCapHits uint64
	HardAgeEscapes     uint64
	FallbackOldest     uint64
	MaxConsecutive     uint64

	TotalOwnerWaitNS float64
	OwnerWaitSamples uint64
}

type m2BatchKey struct {
	requesterName string
	ownerName     string
	pid           vm.PID
	pagePAddr     uint64
}

type m2OriginalReq struct {
	req       mem.AccessReq
	arrival   sim.VTimeInSec
	firstSeen sim.VTimeInSec
}

type m2RequesterBatch struct {
	id          uint64
	key         m2BatchKey
	dst         sim.Port
	lineBitmap  uint64
	lineOrder   []uint64
	requests    map[uint64][]m2OriginalReq
	prefetch    map[uint64]bool
	prefetchDst map[uint64]sim.Port
	requestList []m2OriginalReq
	oldest      sim.VTimeInSec
	newest      sim.VTimeInSec
	info        interface{}
}

func (b *m2RequesterBatch) uniqueLineCount() int {
	return bits.OnesCount64(b.lineBitmap)
}

func (b *m2RequesterBatch) requestCount() int {
	return len(b.requestList)
}

func (b *m2RequesterBatch) hasLine(line uint64) bool {
	return b.lineBitmap&(uint64(1)<<line) != 0
}

func (b *m2RequesterBatch) add(req mem.AccessReq, arrival, firstSeen sim.VTimeInSec) {
	line := m2LineOffset(req.GetAddress())
	if !b.hasLine(line) {
		b.lineBitmap |= uint64(1) << line
		b.lineOrder = append(b.lineOrder, line)
	}
	if b.prefetch != nil {
		delete(b.prefetch, line)
	}
	original := m2OriginalReq{
		req:       req,
		arrival:   arrival,
		firstSeen: firstSeen,
	}
	b.requests[line] = append(b.requests[line], original)
	b.requestList = append(b.requestList, original)
	b.newest = arrival
}

func (b *m2RequesterBatch) addPrefetchLine(line uint64, dst sim.Port) bool {
	if b.hasLine(line) {
		return false
	}
	if b.prefetch == nil {
		b.prefetch = make(map[uint64]bool)
	}
	if b.prefetchDst == nil {
		b.prefetchDst = make(map[uint64]sim.Port)
	}
	b.lineBitmap |= uint64(1) << line
	b.lineOrder = append(b.lineOrder, line)
	b.prefetch[line] = true
	b.prefetchDst[line] = dst
	return true
}

func (b *m2RequesterBatch) isPrefetchLine(line uint64) bool {
	return b.prefetch != nil && b.prefetch[line]
}

type m2PendingRequesterRsp struct {
	original         m2OriginalReq
	batchReq         *BitmapReadReq
	data             []byte
	batchRspID       string
	batchRspSendTime sim.VTimeInSec
	firstSeen        sim.VTimeInSec
	providerName     string
}

type m2PendingRemoteFill struct {
	pid       vm.PID
	dst       sim.Port
	address   uint64
	data      []byte
	firstSeen sim.VTimeInSec
}

type m2OwnerBatch struct {
	req       *BitmapReadReq
	remaining int
	lineData  map[uint64][]byte
}

type m2OwnerSubReq struct {
	batch *m2OwnerBatch
	line  uint64
	read  *mem.ReadReq
}

type m3OwnerServiceRequest struct {
	requesterName string
	lineCount     int
	arrival       sim.VTimeInSec
	msg           sim.Msg
}

func m2ReadBatchable(req *mem.ReadReq) bool {
	if req.AccessByteSize != m2DefaultLineBytes {
		return false
	}
	return m2PageAddress(req.Address) ==
		m2PageAddress(req.Address+req.AccessByteSize-1)
}

func m2PageAddress(addr uint64) uint64 {
	return addr & ^(m2DefaultPageBytes - 1)
}

func m2LineOffset(addr uint64) uint64 {
	return (addr - m2PageAddress(addr)) / m2DefaultLineBytes
}

func m2VTimeToNS(t sim.VTimeInSec) float64 {
	return float64(t) * 1e9
}

func m2PortName(port sim.Port) string {
	if port == nil {
		return ""
	}
	return port.Name()
}

func bitmapLineCount(bitmap uint64) int {
	count := bits.OnesCount64(bitmap)
	if count < 1 {
		return 1
	}
	return count
}
