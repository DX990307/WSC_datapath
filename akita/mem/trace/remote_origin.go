package trace

import (
	"encoding/csv"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
)

const defaultRemoteOriginRawRecords = uint64(100_000)
const remoteOriginShardCount = 64

// WGOriginInfo is diagnostic provenance attached to a vector-memory request.
// It is populated only while the remote-origin audit is enabled.
type WGOriginInfo struct {
	PID           uint64
	RequesterGPU  int
	FlattenedWGID uint64
	WGX           int
	WGY           int
	WGZ           int
}

type remoteOriginObject struct {
	pid   uint64
	begin uint64
	end   uint64
	label string
}

// RemoteOriginRecord describes a translated demand before it enters L1/L2.
type RemoteOriginRecord struct {
	RequestID    string
	Operation    string
	ByteSize     uint64
	RequesterGPU int
	OwnerGPU     int
	Requester    string
	Owner        string
	PID          uint64
	FlattenedWG  uint64
	WGX          int
	WGY          int
	WGZ          int
	VAddr        uint64
	PAddr        uint64
}

type remoteOriginAggregateKey struct {
	requester int
	owner     int
	remote    bool
	object    string
	op        string
}

type remoteOriginAggregate struct {
	requests uint64
	bytes    uint64
}

type remoteOriginRow struct {
	sequence uint64
	record   RemoteOriginRecord
	object   string
	remote   bool
	page     uint64
}

type remoteOriginShard struct {
	sync.Mutex
	raw        []remoteOriginRow
	aggregates map[remoteOriginAggregateKey]*remoteOriginAggregate
}

type remoteOriginState struct {
	sync.RWMutex
	prefix   string
	pageSize uint64
	maxRaw   uint64
	total    uint64
	objects  []remoteOriginObject
	shards   [remoteOriginShardCount]remoteOriginShard
}

var remoteOriginEnabled uint32
var globalRemoteOrigin = newRemoteOriginState()

func newRemoteOriginState() *remoteOriginState {
	state := &remoteOriginState{
		pageSize: 4096,
		maxRaw:   defaultRemoteOriginRawRecords,
	}
	state.resetShards()
	return state
}

func (s *remoteOriginState) resetShards() {
	for i := range s.shards {
		s.shards[i].raw = nil
		s.shards[i].aggregates =
			make(map[remoteOriginAggregateKey]*remoteOriginAggregate)
	}
}

// EnableRemoteOriginTrace enables passive WG/object provenance collection.
func EnableRemoteOriginTrace(prefix string, maxRaw, pageSize uint64) {
	globalRemoteOrigin.Lock()
	defer globalRemoteOrigin.Unlock()
	if maxRaw == 0 {
		maxRaw = defaultRemoteOriginRawRecords
	}
	if pageSize == 0 {
		pageSize = 4096
	}
	globalRemoteOrigin.prefix = prefix
	globalRemoteOrigin.pageSize = pageSize
	globalRemoteOrigin.maxRaw = maxRaw
	atomic.StoreUint64(&globalRemoteOrigin.total, 0)
	globalRemoteOrigin.objects = nil
	globalRemoteOrigin.resetShards()
	atomic.StoreUint32(&remoteOriginEnabled, 1)
}

// DisableRemoteOriginTrace drops diagnostic state.
func DisableRemoteOriginTrace() {
	atomic.StoreUint32(&remoteOriginEnabled, 0)
	globalRemoteOrigin.Lock()
	defer globalRemoteOrigin.Unlock()
	globalRemoteOrigin.prefix = ""
	globalRemoteOrigin.objects = nil
	globalRemoteOrigin.resetShards()
}

// RemoteOriginTraceEnabled is a cheap off-path gate used by vector memory.
func RemoteOriginTraceEnabled() bool {
	return atomic.LoadUint32(&remoteOriginEnabled) != 0
}

// WithWGOriginInfo adds workgroup provenance without discarding other Info.
func WithWGOriginInfo(info interface{}, origin WGOriginInfo) interface{} {
	accessInfo := cloneL2AccessInfo(info)
	accessInfo.HasWGOrigin = true
	accessInfo.WGOrigin = origin
	return accessInfo
}

// RegisterRemoteOriginObject names a virtual allocation for audit reports.
func RegisterRemoteOriginObject(
	pid, begin, size uint64,
	label string,
) {
	if !RemoteOriginTraceEnabled() || size == 0 || label == "" {
		return
	}
	globalRemoteOrigin.Lock()
	defer globalRemoteOrigin.Unlock()
	globalRemoteOrigin.objects = append(globalRemoteOrigin.objects,
		remoteOriginObject{pid: pid, begin: begin, end: begin + size, label: label})
}

// RecordRemoteOrigin records one translated data request.
func RecordRemoteOrigin(record RemoteOriginRecord) {
	if !RemoteOriginTraceEnabled() {
		return
	}
	sequence := atomic.AddUint64(&globalRemoteOrigin.total, 1)
	globalRemoteOrigin.RLock()
	object := globalRemoteOrigin.objectFor(record.PID, record.VAddr)
	pageSize := globalRemoteOrigin.pageSize
	maxRaw := globalRemoteOrigin.maxRaw
	globalRemoteOrigin.RUnlock()

	remote := record.RequesterGPU != record.OwnerGPU
	key := remoteOriginAggregateKey{
		requester: record.RequesterGPU,
		owner:     record.OwnerGPU,
		remote:    remote,
		object:    object,
		op:        record.Operation,
	}
	shardIndex := record.RequesterGPU
	if shardIndex < 0 {
		shardIndex = 0
	}
	shard := &globalRemoteOrigin.shards[shardIndex%remoteOriginShardCount]
	shard.Lock()
	defer shard.Unlock()
	aggregate := shard.aggregates[key]
	firstForKey := aggregate == nil
	if aggregate == nil {
		aggregate = &remoteOriginAggregate{}
		shard.aggregates[key] = aggregate
	}
	aggregate.requests++
	aggregate.bytes += record.ByteSize
	// Keep the bounded leading window plus the first remote example for each
	// aggregate key. A boundary remote access may occur long after the leading
	// window under the original scheduler; retaining one such row is bounded
	// while preserving actionable provenance.
	if sequence <= maxRaw || (remote && firstForKey) {
		page := record.PAddr / pageSize * pageSize
		shard.raw = append(shard.raw, remoteOriginRow{
			sequence: sequence,
			record:   record,
			object:   object,
			remote:   remote,
			page:     page,
		})
	}
}

func (s *remoteOriginState) objectFor(pid, address uint64) string {
	for _, object := range s.objects {
		if object.pid == pid && address >= object.begin && address < object.end {
			return object.label
		}
	}
	return "unclassified"
}

// DumpRemoteOriginTrace writes raw evidence and complete aggregates.
func DumpRemoteOriginTrace() error {
	if !RemoteOriginTraceEnabled() {
		return nil
	}
	globalRemoteOrigin.RLock()
	prefix := globalRemoteOrigin.prefix
	globalRemoteOrigin.RUnlock()
	if prefix == "" {
		return fmt.Errorf("remote-origin trace prefix is empty")
	}
	raw, aggregates := globalRemoteOrigin.snapshotRecords()
	if err := dumpRemoteOriginRaw(prefix, raw); err != nil {
		return err
	}
	return dumpRemoteOriginSummary(prefix, aggregates)
}

func (s *remoteOriginState) snapshotRecords() (
	[]remoteOriginRow,
	map[remoteOriginAggregateKey]remoteOriginAggregate,
) {
	raw := make([]remoteOriginRow, 0)
	aggregates := make(map[remoteOriginAggregateKey]remoteOriginAggregate)
	for i := range s.shards {
		shard := &s.shards[i]
		shard.Lock()
		raw = append(raw, shard.raw...)
		for key, value := range shard.aggregates {
			aggregate := aggregates[key]
			aggregate.requests += value.requests
			aggregate.bytes += value.bytes
			aggregates[key] = aggregate
		}
		shard.Unlock()
	}
	sort.Slice(raw, func(i, j int) bool {
		return raw[i].sequence < raw[j].sequence
	})
	return raw, aggregates
}

func dumpRemoteOriginRaw(prefix string, rows []remoteOriginRow) error {
	file, err := os.Create(prefix + "_raw.csv")
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	_ = writer.Write([]string{
		"sequence", "request_id", "operation", "bytes", "requester_gpu",
		"owner_gpu", "page_owner_gpu", "is_remote", "flattened_wg_id",
		"wg_x", "wg_y", "wg_z", "pid", "vaddr", "paddr", "page_paddr",
		"object", "requester_component", "owner_component",
	})
	for _, row := range rows {
		r := row.record
		_ = writer.Write([]string{
			strconv.FormatUint(row.sequence, 10), r.RequestID, r.Operation,
			strconv.FormatUint(r.ByteSize, 10), strconv.Itoa(r.RequesterGPU),
			strconv.Itoa(r.OwnerGPU), strconv.Itoa(r.OwnerGPU),
			strconv.FormatBool(row.remote), strconv.FormatUint(r.FlattenedWG, 10),
			strconv.Itoa(r.WGX), strconv.Itoa(r.WGY), strconv.Itoa(r.WGZ),
			strconv.FormatUint(r.PID, 10), strconv.FormatUint(r.VAddr, 10),
			strconv.FormatUint(r.PAddr, 10), strconv.FormatUint(row.page, 10),
			row.object, r.Requester, r.Owner,
		})
	}
	return writer.Error()
}

func dumpRemoteOriginSummary(
	prefix string,
	aggregates map[remoteOriginAggregateKey]remoteOriginAggregate,
) error {
	file, err := os.Create(prefix + "_summary.csv")
	if err != nil {
		return err
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	_ = writer.Write([]string{
		"requester_gpu", "owner_gpu", "page_owner_gpu", "is_remote",
		"object", "operation", "requests", "bytes",
	})
	keys := make([]remoteOriginAggregateKey, 0, len(aggregates))
	for key := range aggregates {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].requester != keys[j].requester {
			return keys[i].requester < keys[j].requester
		}
		if keys[i].owner != keys[j].owner {
			return keys[i].owner < keys[j].owner
		}
		if keys[i].object != keys[j].object {
			return keys[i].object < keys[j].object
		}
		return keys[i].op < keys[j].op
	})
	for _, key := range keys {
		value := aggregates[key]
		_ = writer.Write([]string{
			strconv.Itoa(key.requester), strconv.Itoa(key.owner),
			strconv.Itoa(key.owner), strconv.FormatBool(key.remote), key.object,
			key.op, strconv.FormatUint(value.requests, 10),
			strconv.FormatUint(value.bytes, 10),
		})
	}
	return writer.Error()
}
