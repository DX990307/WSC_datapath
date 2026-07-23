package writeback

import (
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// TypedFilterOwnerID deterministically compresses a local module/remote owner
// name into the owner field of PENDING and SEEN keys.
func TypedFilterOwnerID(owner string) uint64 {
	hash := uint64(1469598103934665603)
	for i := 0; i < len(owner); i++ {
		hash ^= uint64(owner[i])
		hash *= 1099511628211
	}
	return hash
}

const (
	typedFilterDefaultSlotsPerBucket  = 4
	typedFilterMaxSlotsPerBucket      = 8
	typedFilterDefaultFingerprintBits = 13
	typedFilterReferenceBits          = 4
	typedFilterMaxReferences          = (1 << typedFilterReferenceBits) - 1
	typedFilterMaxKicks               = 500
	typedFilterTypeCount              = 5
)

// TypedFilterKeyType identifies a logical metadata set sharing one physical
// per-L2-slice Cuckoo Filter.
type TypedFilterKeyType uint8

const (
	// FilterResident tracks lines currently installed in this L2 slice.
	FilterResident TypedFilterKeyType = iota
	// FilterPending tracks exact remote transactions still in flight.
	FilterPending
	// FilterSeen tracks completed one-touch remote lines not installed in L2.
	FilterSeen
	// FilterPattern tracks low-priority, real-demand-trained stride evidence.
	// It is a performance hint: absence suppresses speculation and never
	// changes the correctness path of a demand request.
	FilterPattern
	// FilterGranularityPending tracks sibling lines already attached to an
	// in-flight controller-granularity expansion.  It is distinct from the
	// requester-RDMA PENDING namespace so either mechanism can reset its own
	// lifecycle state without disturbing the other while sharing one physical
	// per-slice Cuckoo Filter.
	FilterGranularityPending
)

// TypedFilterMode selects the metadata implementation used by diagnostics.
type TypedFilterMode uint8

const (
	// TypedFilterDisabled leaves transformations to their conservative fallback.
	TypedFilterDisabled TypedFilterMode = iota
	// TypedFilterCuckoo uses the modeled approximate physical structure.
	TypedFilterCuckoo
	// TypedFilterExact uses the analysis shadow as ideal exact metadata.
	TypedFilterExact
)

// TypedFilterKey is metadata only. Address is cache-line aligned for line
// lifecycle types. FilterPattern uses Address to encode a signed stride.
// Owner is zero for owner-independent local state.
type TypedFilterKey struct {
	PID     vm.PID
	Owner   uint64
	Address uint64
	Type    TypedFilterKeyType
}

// TypedFilterConfig derives capacity from the structures the filter
// summarizes. CriticalReserve is protected from SEEN pressure.
type TypedFilterConfig struct {
	Capacity            int
	CriticalReserve     int
	SlotsPerBucket      int
	FingerprintBits     int
	Mode                TypedFilterMode
	LookupLatencyCycles int
	LookupWidth         int
	UpdateLatencyCycles int
	UpdateWidth         int
	Freq                sim.Freq
}

// TypedFilterTypeStats reports one logical key class.
type TypedFilterTypeStats struct {
	Reliable        bool
	Queries         uint64
	Positives       uint64
	Negatives       uint64
	FalsePositives  uint64
	Insertions      uint64
	Deletes         uint64
	InsertFailures  uint64
	FailOpen        uint64
	LookupBusyDrops uint64
	UpdateBusyDrops uint64
	Occupancy       uint64
	PeakOccupancy   uint64
}

// TypedFilterStats reports the shared physical array and modeled ports.
type TypedFilterStats struct {
	Mode                 TypedFilterMode
	Buckets              uint64
	Slots                uint64
	SlotsPerBucket       uint64
	FingerprintBits      uint64
	ReferenceBits        uint64
	LookupLatencyCycles  uint64
	UpdateLatencyCycles  uint64
	LookupWidth          uint64
	UpdateWidth          uint64
	LookupPortStalls     uint64
	UpdatePortStalls     uint64
	Occupancy            uint64
	PeakOccupancy        uint64
	LowPriorityLimit     uint64
	EstimatedStorageBits uint64
	ByType               [typedFilterTypeCount]TypedFilterTypeStats
}

type typedFilterSlot struct {
	fingerprint uint16
	references  uint8
	kind        TypedFilterKeyType
	occupied    bool
}

type typedFilterBucket struct {
	slots [typedFilterMaxSlotsPerBucket]typedFilterSlot
}

type typedFilterMutation struct {
	bucket   int
	slot     int
	previous typedFilterSlot
}

// TypedFilterLookup is a latency-bearing lookup ticket. Call CompleteLookup
// no earlier than ReadyAt.
type TypedFilterLookup struct {
	Key     TypedFilterKey
	ReadyAt sim.VTimeInSec
}

// TypedFilterUpdate is a latency-bearing insert/delete ticket.
type TypedFilterUpdate struct {
	Key     TypedFilterKey
	ReadyAt sim.VTimeInSec
	Delete  bool
}

// TypedCuckooFilter is the single physical metadata filter owned by one L2
// slice. The exact map is an analysis/correctness shadow, not modeled storage.
type TypedCuckooFilter struct {
	config TypedFilterConfig
	bucket []typedFilterBucket
	mask   uint64
	exact  map[TypedFilterKey]uint64
	stats  TypedFilterStats

	lookupCycle sim.VTimeInSec
	lookupUsed  int
	updateCycle sim.VTimeInSec
	updateUsed  int
	// scheduledVisibility records fire-and-forget cache metadata writes that
	// have updated the simulator's exact lifecycle state but have not yet
	// become visible through the modeled filter array. A lookup to such a key
	// fails open until the shared update port and latency have elapsed.
	scheduledVisibility map[TypedFilterKey]sim.VTimeInSec
}

// lineMembershipFilter preserves the small cache-local API while RESIDENT
// and requester-L2 state become logical views of one physical typed filter.
type lineMembershipFilter interface {
	Contains(pid vm.PID, lineAddr uint64) bool
	Insert(pid vm.PID, lineAddr uint64) bool
	Delete(pid vm.PID, lineAddr uint64)
	Reset()
}

type typedFilterLineView struct {
	filter *TypedCuckooFilter
	kind   TypedFilterKeyType
}

func newTypedFilterLineView(
	filter *TypedCuckooFilter,
	kind TypedFilterKeyType,
) lineMembershipFilter {
	return &typedFilterLineView{filter: filter, kind: kind}
}

func (v *typedFilterLineView) key(pid vm.PID, line uint64) TypedFilterKey {
	return TypedFilterKey{PID: pid, Address: line, Type: v.kind}
}

func (v *typedFilterLineView) Contains(pid vm.PID, line uint64) bool {
	possible, reliable := v.filter.Query(v.key(pid, line))
	// Both current users treat positive as a hint and confirm in the exact L2
	// directory. An unreliable class therefore safely fails open to lookup.
	return possible || !reliable
}

func (v *typedFilterLineView) Insert(pid vm.PID, line uint64) bool {
	return v.filter.Insert(v.key(pid, line))
}

func (v *typedFilterLineView) Delete(pid vm.PID, line uint64) {
	v.filter.Delete(v.key(pid, line))
}

func (v *typedFilterLineView) Reset() {
	v.filter.ClearType(v.kind)
}

// NewTypedCuckooFilter builds a power-of-two bucket array with four slots per
// bucket. The default 13-bit fingerprint targets a sub-percent false-positive
// rate at the intended load factor.
func NewTypedCuckooFilter(config TypedFilterConfig) *TypedCuckooFilter {
	if config.Capacity < 1 {
		config.Capacity = 1
	}
	if config.SlotsPerBucket == 0 {
		config.SlotsPerBucket = typedFilterDefaultSlotsPerBucket
	}
	if config.SlotsPerBucket < 1 ||
		config.SlotsPerBucket > typedFilterMaxSlotsPerBucket {
		panic("typed Cuckoo Filter slots per bucket must be in [1, 8]")
	}
	if config.FingerprintBits == 0 {
		config.FingerprintBits = typedFilterDefaultFingerprintBits
	}
	if config.FingerprintBits < 4 || config.FingerprintBits > 16 {
		panic("typed Cuckoo Filter fingerprint bits must be in [4, 16]")
	}
	if config.LookupLatencyCycles < 0 {
		config.LookupLatencyCycles = 0
	}
	if config.UpdateLatencyCycles < 0 {
		config.UpdateLatencyCycles = 0
	}
	if config.LookupWidth < 1 {
		config.LookupWidth = 1
	}
	if config.UpdateWidth < 1 {
		config.UpdateWidth = 1
	}
	if config.Freq == 0 {
		config.Freq = 1 * sim.GHz
	}

	bucketCount := typedFilterBucketCount(
		config.Capacity, config.SlotsPerBucket)
	actualSlots := bucketCount * config.SlotsPerBucket
	if config.CriticalReserve < 0 {
		config.CriticalReserve = 0
	}
	if config.CriticalReserve > actualSlots {
		config.CriticalReserve = actualSlots
	}

	f := &TypedCuckooFilter{
		config:              config,
		bucket:              make([]typedFilterBucket, bucketCount),
		mask:                uint64(bucketCount - 1),
		exact:               make(map[TypedFilterKey]uint64),
		scheduledVisibility: make(map[TypedFilterKey]sim.VTimeInSec),
	}
	f.stats.Mode = config.Mode
	f.stats.Buckets = uint64(bucketCount)
	f.stats.Slots = uint64(actualSlots)
	f.stats.SlotsPerBucket = uint64(config.SlotsPerBucket)
	f.stats.FingerprintBits = uint64(config.FingerprintBits)
	f.stats.ReferenceBits = typedFilterReferenceBits
	f.stats.LookupLatencyCycles = uint64(config.LookupLatencyCycles)
	f.stats.UpdateLatencyCycles = uint64(config.UpdateLatencyCycles)
	f.stats.LookupWidth = uint64(config.LookupWidth)
	f.stats.UpdateWidth = uint64(config.UpdateWidth)
	// Keep the physical Cuckoo array below a conservative 90% target load.
	// LowPriorityLimit is a combined SEEN/PATTERN occupancy budget, not a limit on
	// total occupancy: resident cache lines legitimately consume the critical
	// reserve and must not accidentally disable every low-priority insertion.
	targetOccupancy := actualSlots * 9 / 10
	if targetOccupancy > config.CriticalReserve {
		f.stats.LowPriorityLimit = uint64(
			targetOccupancy - config.CriticalReserve)
	}
	// Per slot: configurable fingerprint, 3-bit type, valid bit, and a
	// four-bit saturating reference count. Analysis counters and
	// the simulator-only exact shadow are not charged as hardware storage.
	f.stats.EstimatedStorageBits = uint64(
		actualSlots * (config.FingerprintBits + 3 + 1 + typedFilterReferenceBits))
	for i := range f.stats.ByType {
		f.stats.ByType[i].Reliable = true
	}
	return f
}

func typedFilterBucketCount(capacity, slotsPerBucket int) int {
	required := (capacity-1)/slotsPerBucket + 1
	bucketCount := 2
	for bucketCount < required {
		if bucketCount > int(^uint(0)>>1)/2 {
			panic("typed Cuckoo Filter capacity is too large")
		}
		bucketCount <<= 1
	}
	return bucketCount
}

func validTypedFilterType(kind TypedFilterKeyType) bool {
	return int(kind) >= 0 && int(kind) < typedFilterTypeCount
}

func lowPriorityTypedFilterType(kind TypedFilterKeyType) bool {
	return kind == FilterSeen || kind == FilterPattern
}

func (f *TypedCuckooFilter) lowPriorityOccupancy() uint64 {
	return f.stats.ByType[FilterSeen].Occupancy +
		f.stats.ByType[FilterPattern].Occupancy
}

// StartLookup reserves one modeled lookup port and returns a completion
// ticket. A false return represents structural contention and must be retried.
func (f *TypedCuckooFilter) StartLookup(
	now sim.VTimeInSec,
	key TypedFilterKey,
) (TypedFilterLookup, bool) {
	if f == nil || !validTypedFilterType(key.Type) ||
		f.config.Mode == TypedFilterDisabled {
		return TypedFilterLookup{}, false
	}
	if f.lookupCycle != now {
		f.lookupCycle = now
		f.lookupUsed = 0
	}
	if f.lookupUsed >= f.config.LookupWidth {
		f.stats.LookupPortStalls++
		f.stats.ByType[key.Type].LookupBusyDrops++
		return TypedFilterLookup{}, false
	}
	f.lookupUsed++
	return TypedFilterLookup{
		Key:     key,
		ReadyAt: f.config.Freq.NCyclesLater(f.config.LookupLatencyCycles, now),
	}, true
}

// CompleteLookup returns possible membership after the modeled lookup delay.
// reliable=false means the caller must take its conservative fallback.
func (f *TypedCuckooFilter) CompleteLookup(
	now sim.VTimeInSec,
	lookup TypedFilterLookup,
) (possible bool, reliable bool, ready bool) {
	if now < lookup.ReadyAt {
		return false, true, false
	}
	if visibleAt, pending := f.scheduledVisibility[lookup.Key]; pending {
		if now < visibleAt {
			f.stats.ByType[lookup.Key.Type].FailOpen++
			return false, false, true
		}
		delete(f.scheduledVisibility, lookup.Key)
	}
	possible, reliable = f.Query(lookup.Key)
	return possible, reliable, true
}

// StartUpdate reserves one modeled update port. A rejected request must retry
// on a later cycle; it is not an insertion failure.
func (f *TypedCuckooFilter) StartUpdate(
	now sim.VTimeInSec,
	key TypedFilterKey,
	deleteKey bool,
) (TypedFilterUpdate, bool) {
	if f == nil || !validTypedFilterType(key.Type) ||
		f.config.Mode == TypedFilterDisabled {
		return TypedFilterUpdate{}, false
	}
	if f.updateCycle < now {
		f.updateCycle = now
		f.updateUsed = 0
	} else if f.updateCycle > now {
		f.stats.UpdatePortStalls++
		f.stats.ByType[key.Type].UpdateBusyDrops++
		return TypedFilterUpdate{}, false
	}
	if f.updateUsed >= f.config.UpdateWidth {
		f.stats.UpdatePortStalls++
		f.stats.ByType[key.Type].UpdateBusyDrops++
		return TypedFilterUpdate{}, false
	}
	f.updateUsed++
	return TypedFilterUpdate{
		Key: key, Delete: deleteKey,
		ReadyAt: f.config.Freq.NCyclesLater(f.config.UpdateLatencyCycles, now),
	}, true
}

// ScheduleUpdate models lifecycle writes that cannot hold a cache block or
// response pipeline while metadata becomes visible. The exact lifecycle state
// is committed immediately for simulator correctness, while same-key lookups
// fail open until the shared update port and configured latency have elapsed.
// Excess same-cycle writes serialize onto later update-port cycles and count
// structural contention.
func (f *TypedCuckooFilter) ScheduleUpdate(
	now sim.VTimeInSec,
	key TypedFilterKey,
	deleteKey bool,
) bool {
	if f == nil || !validTypedFilterType(key.Type) ||
		f.config.Mode == TypedFilterDisabled {
		return false
	}
	if f.updateCycle < now {
		f.updateCycle = now
		f.updateUsed = 0
	}
	if f.updateUsed >= f.config.UpdateWidth {
		f.updateCycle = f.config.Freq.NextTick(f.updateCycle)
		f.updateUsed = 0
		f.stats.UpdatePortStalls++
	}
	f.updateUsed++
	readyAt := f.config.Freq.NCyclesLater(
		f.config.UpdateLatencyCycles, f.updateCycle)
	if previous := f.scheduledVisibility[key]; readyAt > previous {
		f.scheduledVisibility[key] = readyAt
	}
	if deleteKey {
		f.Delete(key)
		return true
	}
	return f.Insert(key)
}

// TryScheduleUpdate is the non-blocking counterpart of ScheduleUpdate. It
// consumes only an update port available in the caller's current cycle and
// never serializes speculative metadata into a later cycle. The exact
// lifecycle shadow is committed immediately, while lookups fail open until
// the modeled update latency elapses. It is intended for PATTERN and
// speculative PENDING state whose safe fallback is to abandon the candidate.
func (f *TypedCuckooFilter) TryScheduleUpdate(
	now sim.VTimeInSec,
	key TypedFilterKey,
	deleteKey bool,
) bool {
	if f == nil || !validTypedFilterType(key.Type) ||
		f.config.Mode == TypedFilterDisabled {
		return false
	}
	if f.updateCycle > now {
		f.stats.UpdatePortStalls++
		f.stats.ByType[key.Type].UpdateBusyDrops++
		return false
	}
	if f.updateCycle < now {
		f.updateCycle = now
		f.updateUsed = 0
	}
	if f.updateUsed >= f.config.UpdateWidth {
		f.stats.UpdatePortStalls++
		f.stats.ByType[key.Type].UpdateBusyDrops++
		return false
	}
	f.updateUsed++
	readyAt := f.config.Freq.NCyclesLater(
		f.config.UpdateLatencyCycles, now)
	if previous := f.scheduledVisibility[key]; readyAt > previous {
		f.scheduledVisibility[key] = readyAt
	}
	if deleteKey {
		f.Delete(key)
		return true
	}
	return f.Insert(key)
}

// CompleteUpdate commits an accepted update after its modeled latency.
func (f *TypedCuckooFilter) CompleteUpdate(
	now sim.VTimeInSec,
	update TypedFilterUpdate,
) (success bool, ready bool) {
	if now < update.ReadyAt {
		return false, false
	}
	if update.Delete {
		f.Delete(update.Key)
		return true, true
	}
	return f.Insert(update.Key), true
}

// Query performs a completed metadata lookup and updates per-type analysis.
func (f *TypedCuckooFilter) Query(key TypedFilterKey) (bool, bool) {
	if f == nil || !validTypedFilterType(key.Type) {
		return false, false
	}
	typeStats := &f.stats.ByType[key.Type]
	typeStats.Queries++
	exact := f.exact[key] > 0
	if f.config.Mode == TypedFilterDisabled || !typeStats.Reliable {
		typeStats.FailOpen++
		return false, false
	}

	possible := exact
	if f.config.Mode == TypedFilterCuckoo {
		possible = f.containsSignature(key)
	}
	if possible {
		typeStats.Positives++
		if !exact {
			typeStats.FalsePositives++
		}
	} else {
		typeStats.Negatives++
	}
	return possible, true
}

// Contains is a raw membership helper for unit tests and exact confirmations;
// it does not update query counters.
func (f *TypedCuckooFilter) Contains(key TypedFilterKey) bool {
	if f == nil || !validTypedFilterType(key.Type) {
		return false
	}
	if f.config.Mode == TypedFilterExact {
		return f.exact[key] > 0
	}
	return f.containsSignature(key)
}

// ExactContains exposes the simulator-only analysis shadow for confirmation
// and idempotent lifecycle updates. It is never a modeled hardware lookup.
func (f *TypedCuckooFilter) ExactContains(key TypedFilterKey) bool {
	return f != nil && f.exact[key] > 0
}

func (f *TypedCuckooFilter) containsSignature(key TypedFilterKey) bool {
	if len(f.bucket) == 0 {
		return false
	}
	fingerprint, first, second, _ := f.signature(key)
	_, _, found := f.find(first, second, fingerprint, key.Type)
	return found
}

// Insert adds one counted reference. Low-priority types cannot consume slots
// reserved for correctness-critical RESIDENT/PENDING metadata.
func (f *TypedCuckooFilter) Insert(key TypedFilterKey) bool {
	if f == nil || !validTypedFilterType(key.Type) {
		return false
	}
	f.exact[key]++
	typeStats := &f.stats.ByType[key.Type]
	typeStats.Insertions++
	if f.config.Mode == TypedFilterDisabled || f.config.Mode == TypedFilterExact {
		f.noteLogicalInsert(key.Type)
		return true
	}

	fingerprint, first, second, hash := f.signature(key)
	if bucket, slot, found := f.find(first, second, fingerprint, key.Type); found {
		entry := &f.bucket[bucket].slots[slot]
		if entry.references >= typedFilterMaxReferences {
			f.failInsertion(key)
			return false
		}
		entry.references++
		return true
	}
	if lowPriorityTypedFilterType(key.Type) &&
		f.lowPriorityOccupancy() >= f.stats.LowPriorityLimit {
		f.failInsertion(key)
		return false
	}

	incoming := typedFilterSlot{
		fingerprint: fingerprint,
		references:  1,
		kind:        key.Type,
		occupied:    true,
	}
	start, alternate := first, second
	if hash>>63 != 0 {
		start, alternate = second, first
	}
	if f.insertEmpty(start, incoming, nil) ||
		f.insertEmpty(alternate, incoming, nil) {
		f.noteLogicalInsert(key.Type)
		return true
	}

	mutations := make([]typedFilterMutation, 0, typedFilterMaxKicks)
	bucket := start
	for kick := 0; kick < typedFilterMaxKicks; kick++ {
		slotIndex := f.victimSlot(hash, incoming, bucket, kick)
		victim := f.bucket[bucket].slots[slotIndex]
		f.replace(bucket, slotIndex, incoming, &mutations)
		incoming = victim
		bucket = f.alternateIndex(bucket, incoming.fingerprint, incoming.kind)

		if matchBucket, matchSlot, found := f.findInBucket(
			bucket, incoming.fingerprint, incoming.kind,
		); found {
			updated := f.bucket[matchBucket].slots[matchSlot]
			if typedFilterMaxReferences-updated.references < incoming.references {
				f.rollback(mutations)
				f.failInsertion(key)
				return false
			}
			updated.references += incoming.references
			f.replace(matchBucket, matchSlot, updated, &mutations)
			f.noteLogicalInsert(key.Type)
			return true
		}
		if f.insertEmpty(bucket, incoming, &mutations) {
			f.noteLogicalInsert(key.Type)
			return true
		}
	}

	f.rollback(mutations)
	f.failInsertion(key)
	return false
}

func (f *TypedCuckooFilter) failInsertion(key TypedFilterKey) {
	// Insert records the logical reference before it attempts a physical
	// placement.  A failed placement must retire that tentative reference as
	// well as roll back bucket mutations; otherwise the analysis shadow would
	// claim that a key exists after the corresponding hardware update failed.
	if references := f.exact[key]; references > 1 {
		f.exact[key] = references - 1
	} else {
		delete(f.exact, key)
	}
	typeStats := &f.stats.ByType[key.Type]
	typeStats.InsertFailures++
	typeStats.Reliable = false
}

func (f *TypedCuckooFilter) noteLogicalInsert(kind TypedFilterKeyType) {
	f.stats.Occupancy++
	if f.stats.Occupancy > f.stats.PeakOccupancy {
		f.stats.PeakOccupancy = f.stats.Occupancy
	}
	typeStats := &f.stats.ByType[kind]
	typeStats.Occupancy++
	if typeStats.Occupancy > typeStats.PeakOccupancy {
		typeStats.PeakOccupancy = typeStats.Occupancy
	}
}

// Delete removes one counted reference. Deleting an absent key is a no-op.
func (f *TypedCuckooFilter) Delete(key TypedFilterKey) {
	if f == nil || !validTypedFilterType(key.Type) || f.exact[key] == 0 {
		return
	}
	if f.exact[key] > 1 {
		f.exact[key]--
	} else {
		delete(f.exact, key)
	}
	f.stats.ByType[key.Type].Deletes++
	if f.config.Mode == TypedFilterCuckoo {
		fingerprint, first, second, _ := f.signature(key)
		bucket, slot, found := f.find(first, second, fingerprint, key.Type)
		if found {
			entry := &f.bucket[bucket].slots[slot]
			if entry.references > 1 {
				entry.references--
				return
			}
			*entry = typedFilterSlot{}
		}
	}
	if f.stats.Occupancy > 0 {
		f.stats.Occupancy--
	}
	if f.stats.ByType[key.Type].Occupancy > 0 {
		f.stats.ByType[key.Type].Occupancy--
	}
}

// Reset clears all logical types and restores their independent reliability.
func (f *TypedCuckooFilter) Reset() {
	if f == nil {
		return
	}
	for i := range f.bucket {
		f.bucket[i] = typedFilterBucket{}
	}
	clear(f.exact)
	clear(f.scheduledVisibility)
	f.stats.Occupancy = 0
	for i := range f.stats.ByType {
		f.stats.ByType[i].Occupancy = 0
		f.stats.ByType[i].Reliable = true
	}
}

// ClearType removes one logical set without disturbing the other sets.
func (f *TypedCuckooFilter) ClearType(kind TypedFilterKeyType) {
	if f == nil || !validTypedFilterType(kind) {
		return
	}
	keys := make([]TypedFilterKey, 0)
	for key, references := range f.exact {
		if key.Type != kind {
			continue
		}
		for i := uint64(0); i < references; i++ {
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		f.Delete(key)
	}
	f.stats.ByType[kind].Reliable = true
}

// Stats returns a copy of the filter counters.
func (f *TypedCuckooFilter) Stats() TypedFilterStats {
	if f == nil {
		return TypedFilterStats{}
	}
	return f.stats
}

// Mode reports the active diagnostic metadata implementation.
func (f *TypedCuckooFilter) Mode() TypedFilterMode {
	if f == nil {
		return TypedFilterDisabled
	}
	return f.config.Mode
}

// SetMode changes only the diagnostic implementation and resets contents so
// results from different metadata modes cannot be mixed accidentally.
func (f *TypedCuckooFilter) SetMode(mode TypedFilterMode) {
	if f == nil || f.config.Mode == mode {
		return
	}
	f.config.Mode = mode
	f.stats.Mode = mode
	f.Reset()
}

func (f *TypedCuckooFilter) signature(
	key TypedFilterKey,
) (fingerprint uint16, first, second int, hash uint64) {
	typeSalt := uint64(key.Type+1) * 0xd6e8feb86659fd93
	hash = typedFilterMix(key.Address ^
		typedFilterMix(uint64(key.PID)+0x9e3779b97f4a7c15) ^
		typedFilterMix(key.Owner+0x517cc1b727220a95) ^ typeSalt)
	fingerprint = uint16(hash ^ (hash >> 16) ^ (hash >> 32) ^ (hash >> 48))
	if f.config.FingerprintBits < 16 {
		fingerprint &= uint16((uint32(1) << f.config.FingerprintBits) - 1)
	}
	if fingerprint == 0 {
		fingerprint = 1
	}
	first = int(hash & f.mask)
	second = f.alternateIndex(first, fingerprint, key.Type)
	return fingerprint, first, second, hash
}

func (f *TypedCuckooFilter) alternateIndex(
	bucket int,
	fingerprint uint16,
	kind TypedFilterKeyType,
) int {
	delta := typedFilterMix(uint64(fingerprint)^
		(uint64(kind+1)*0x94d049bb133111eb)) & f.mask
	if delta == 0 {
		delta = 1
	}
	return bucket ^ int(delta)
}

func (f *TypedCuckooFilter) find(
	first, second int,
	fingerprint uint16,
	kind TypedFilterKeyType,
) (bucket, slot int, found bool) {
	if bucket, slot, found = f.findInBucket(first, fingerprint, kind); found {
		return bucket, slot, true
	}
	if second != first {
		return f.findInBucket(second, fingerprint, kind)
	}
	return 0, 0, false
}

func (f *TypedCuckooFilter) findInBucket(
	bucket int,
	fingerprint uint16,
	kind TypedFilterKeyType,
) (int, int, bool) {
	for i := 0; i < f.config.SlotsPerBucket; i++ {
		entry := f.bucket[bucket].slots[i]
		if entry.occupied && entry.fingerprint == fingerprint && entry.kind == kind {
			return bucket, i, true
		}
	}
	return 0, 0, false
}

func (f *TypedCuckooFilter) insertEmpty(
	bucket int,
	incoming typedFilterSlot,
	mutations *[]typedFilterMutation,
) bool {
	for i := 0; i < f.config.SlotsPerBucket; i++ {
		if f.bucket[bucket].slots[i].occupied {
			continue
		}
		if mutations == nil {
			f.bucket[bucket].slots[i] = incoming
		} else {
			f.replace(bucket, i, incoming, mutations)
		}
		return true
	}
	return false
}

func (f *TypedCuckooFilter) replace(
	bucket, slot int,
	replacement typedFilterSlot,
	mutations *[]typedFilterMutation,
) {
	*mutations = append(*mutations, typedFilterMutation{
		bucket: bucket, slot: slot, previous: f.bucket[bucket].slots[slot],
	})
	f.bucket[bucket].slots[slot] = replacement
}

func (f *TypedCuckooFilter) rollback(mutations []typedFilterMutation) {
	for i := len(mutations) - 1; i >= 0; i-- {
		mutation := mutations[i]
		f.bucket[mutation.bucket].slots[mutation.slot] = mutation.previous
	}
}

func (f *TypedCuckooFilter) victimSlot(
	hash uint64,
	incoming typedFilterSlot,
	bucket, kick int,
) int {
	selection := typedFilterMix(hash ^
		(uint64(incoming.fingerprint) << 32) ^
		(uint64(incoming.kind+1) * 0xd6e8feb86659fd93) ^
		(uint64(bucket) * 0x94d049bb133111eb) ^
		(uint64(kick+1) * 0xbf58476d1ce4e5b9))
	return int(selection % uint64(f.config.SlotsPerBucket))
}

func typedFilterMix(value uint64) uint64 {
	value += 0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}
