package writeback

import (
	"reflect"
	"testing"

	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

type typedFilterTestSignature struct {
	fingerprint uint16
	first       int
	second      int
	kind        TypedFilterKeyType
}

func typedFilterKeysWithUniqueSignatures(
	t *testing.T,
	filter *TypedCuckooFilter,
	pid vm.PID,
	kind TypedFilterKeyType,
	count int,
) []TypedFilterKey {
	t.Helper()
	seen := make(map[typedFilterTestSignature]bool)
	keys := make([]TypedFilterKey, 0, count)
	for i := uint64(0); i < 1<<20 && len(keys) < count; i++ {
		key := TypedFilterKey{PID: pid, Address: i * 64, Type: kind}
		fingerprint, first, second, _ := filter.signature(key)
		if second < first {
			first, second = second, first
		}
		signature := typedFilterTestSignature{
			fingerprint: fingerprint, first: first, second: second, kind: kind,
		}
		if seen[signature] {
			continue
		}
		seen[signature] = true
		keys = append(keys, key)
	}
	if len(keys) != count {
		t.Fatalf("found %d unique typed signatures, want %d", len(keys), count)
	}
	return keys
}

func testTypedFilter(capacity int) *TypedCuckooFilter {
	return NewTypedCuckooFilter(TypedFilterConfig{
		Capacity:            capacity,
		Mode:                TypedFilterCuckoo,
		LookupLatencyCycles: 2,
		LookupWidth:         1,
		UpdateLatencyCycles: 1,
		UpdateWidth:         1,
		Freq:                1 * sim.GHz,
	})
}

func TestTypedFilterSeparatesLogicalKeyTypes(t *testing.T) {
	f := testTypedFilter(32)
	base := TypedFilterKey{PID: vm.PID(7), Owner: 9, Address: 0x4000}
	resident := base
	resident.Type = FilterResident
	pending := base
	pending.Type = FilterPending
	seen := base
	seen.Type = FilterSeen

	if !f.Insert(resident) || !f.Insert(pending) {
		t.Fatal("typed inserts failed")
	}
	if !f.Contains(resident) || !f.Contains(pending) {
		t.Fatal("inserted type is absent")
	}
	if f.Contains(seen) {
		t.Fatal("one logical type hit another type")
	}
	f.Delete(resident)
	if f.Contains(resident) || !f.Contains(pending) {
		t.Fatal("deleting RESIDENT altered PENDING")
	}
}

func TestTypedFilterSpeculativeUpdateDoesNotJumpAheadOfSerializedPort(t *testing.T) {
	f := testTypedFilter(32)
	first := TypedFilterKey{PID: 1, Address: 64, Type: FilterResident}
	second := TypedFilterKey{PID: 1, Address: 128, Type: FilterResident}
	speculative := TypedFilterKey{PID: 1, Address: 7, Type: FilterPattern}
	if !f.ScheduleUpdate(1, first, false) || !f.ScheduleUpdate(1, second, false) {
		t.Fatal("lifecycle updates failed")
	}
	if f.TryScheduleUpdate(1, speculative, false) {
		t.Fatal("speculative update jumped ahead of serialized lifecycle update")
	}
	if f.ExactContains(speculative) {
		t.Fatal("rejected speculative state became visible")
	}
}

func TestTypedFilterFingerprintCollisionIsOnlyAFalsePositive(t *testing.T) {
	f := NewTypedCuckooFilter(TypedFilterConfig{
		Capacity: 8, FingerprintBits: 4, Mode: TypedFilterCuckoo,
		LookupWidth: 1, UpdateWidth: 1, Freq: 1 * sim.GHz,
	})
	type signature struct {
		fingerprint uint16
		first       int
		second      int
	}
	seen := make(map[signature]TypedFilterKey)
	var first, collision TypedFilterKey
	for i := uint64(0); i < 1<<16; i++ {
		key := TypedFilterKey{PID: 1, Address: i * 64, Type: FilterPending}
		fingerprint, a, b, _ := f.signature(key)
		if b < a {
			a, b = b, a
		}
		sig := signature{fingerprint, a, b}
		if previous, ok := seen[sig]; ok && previous != key {
			first, collision = previous, key
			break
		}
		seen[sig] = key
	}
	if first == (TypedFilterKey{}) {
		t.Fatal("could not construct a deterministic fingerprint collision")
	}
	if !f.Insert(first) {
		t.Fatal("collision source insertion failed")
	}
	possible, reliable := f.Query(collision)
	if !possible || !reliable || f.ExactContains(collision) {
		t.Fatal("collision was not exposed as an approximate-only positive")
	}
	if f.Stats().ByType[FilterPending].FalsePositives != 1 {
		t.Fatal("collision false positive was not counted")
	}
	// Deleting the absent colliding full key must not damage the live counted
	// signature. Only a deletion of the exact inserted key may remove it.
	f.Delete(collision)
	if !f.Contains(first) {
		t.Fatal("absent colliding-key deletion removed a live entry")
	}
	f.Delete(first)
	if f.Contains(first) {
		t.Fatal("exact collision source deletion did not retire the entry")
	}
}

func TestTypedFilterCountsReliableActiveFalseNegativeByType(t *testing.T) {
	f := testTypedFilter(32)
	resident := TypedFilterKey{
		PID: 1, Address: 0x4000, Type: FilterResident,
	}
	if !f.Insert(resident) {
		t.Fatal("resident insertion failed")
	}

	fingerprint, first, second, _ := f.signature(resident)
	bucket, slot, found := f.find(
		first, second, fingerprint, resident.Type)
	if !found {
		t.Fatal("inserted resident signature was not found")
	}
	// Corrupt only the physical array to exercise the diagnostic invariant.
	// The exact analysis shadow intentionally remains active.
	f.bucket[bucket].slots[slot] = typedFilterSlot{}

	possible, reliable := f.Query(resident)
	if possible || !reliable {
		t.Fatalf("corrupted lookup = (%t, %t), want reliable negative",
			possible, reliable)
	}
	missing := TypedFilterKey{
		PID: 1, Address: 0x8000, Type: FilterPending,
	}
	possible, reliable = f.Query(missing)
	if possible || !reliable {
		t.Fatalf("ordinary miss = (%t, %t), want reliable negative",
			possible, reliable)
	}

	stats := f.Stats()
	if stats.ByType[FilterResident].ActiveFalseNegatives != 1 {
		t.Fatalf("resident active false negatives = %d, want 1",
			stats.ByType[FilterResident].ActiveFalseNegatives)
	}
	if stats.ByType[FilterPending].ActiveFalseNegatives != 0 {
		t.Fatalf("pending active false negatives = %d, want 0",
			stats.ByType[FilterPending].ActiveFalseNegatives)
	}
}

func TestTypedFilterCountedDuplicateDelete(t *testing.T) {
	f := testTypedFilter(16)
	key := TypedFilterKey{PID: 1, Address: 0x8000, Type: FilterPending}
	if !f.Insert(key) || !f.Insert(key) {
		t.Fatal("duplicate insert failed")
	}
	f.Delete(key)
	if !f.Contains(key) {
		t.Fatal("first delete removed a duplicate signature")
	}
	f.Delete(key)
	if f.Contains(key) {
		t.Fatal("matching deletes did not remove signature")
	}
}

func TestTypedFilterFailureIsIsolatedByType(t *testing.T) {
	f := testTypedFilter(1)
	failed := false
	for i := uint64(0); i < 10000; i++ {
		key := TypedFilterKey{
			PID: 1, Owner: i, Address: i * 64, Type: FilterSeen,
		}
		if !f.Insert(key) {
			failed = true
			break
		}
	}
	if !failed {
		t.Fatal("small filter did not reach a bounded insertion failure")
	}
	stats := f.Stats()
	if stats.ByType[FilterSeen].Reliable {
		t.Fatal("failed SEEN class remained reliable")
	}
	if !stats.ByType[FilterResident].Reliable ||
		!stats.ByType[FilterPending].Reliable {
		t.Fatal("SEEN pressure damaged a critical key class")
	}
	resident := TypedFilterKey{PID: 2, Address: 0x1234000, Type: FilterResident}
	_ = f.Insert(resident)
	_, reliable := f.Query(resident)
	if !reliable && f.Stats().ByType[FilterResident].InsertFailures == 0 {
		t.Fatal("RESIDENT reliability changed without its own insertion failure")
	}
}

func TestTypedFilterCriticalReserveSurvivesLowPriorityPressure(t *testing.T) {
	f := NewTypedCuckooFilter(TypedFilterConfig{
		Capacity: 16, CriticalReserve: 8,
		SlotsPerBucket: 4, FingerprintBits: 16,
		Mode: TypedFilterCuckoo, LookupWidth: 1, UpdateWidth: 1,
		Freq: 1 * sim.GHz,
	})
	for i := uint64(0); ; i++ {
		key := TypedFilterKey{
			PID: 1, Owner: i, Address: i * 64, Type: FilterSeen,
		}
		if !f.Insert(key) {
			break
		}
		if i > 32 {
			t.Fatal("low-priority occupancy ignored its derived budget")
		}
	}
	if f.Stats().ByType[FilterSeen].Reliable {
		t.Fatal("low-priority capacity failure did not fail open its own type")
	}
	for i := uint64(0); i < 8; i++ {
		key := TypedFilterKey{
			PID: 2, Address: 0x10000 + i*64, Type: FilterResident,
		}
		if !f.Insert(key) {
			t.Fatalf("critical insertion %d failed after low-priority pressure", i)
		}
	}
	if !f.Stats().ByType[FilterResident].Reliable {
		t.Fatal("low-priority pressure damaged RESIDENT reliability")
	}
}

func TestTypedFilterFailedInsertionRollsBack(t *testing.T) {
	f := testTypedFilter(1)
	keys := typedFilterKeysWithUniqueSignatures(
		t, f, vm.PID(3), FilterResident, 9,
	)
	for _, key := range keys[:8] {
		if !f.Insert(key) {
			t.Fatal("failed to fill typed filter")
		}
	}
	before := append([]typedFilterBucket(nil), f.bucket...)
	if f.Insert(keys[8]) {
		t.Fatal("insert unexpectedly succeeded in a full filter")
	}
	if !reflect.DeepEqual(before, f.bucket) {
		t.Fatal("failed insertion did not roll back physical buckets")
	}
	if f.ExactContains(keys[8]) {
		t.Fatal("failed insertion did not roll back the exact analysis shadow")
	}
	for _, key := range keys[:8] {
		if !f.Contains(key) {
			t.Fatal("failed insertion lost a previously inserted signature")
		}
	}
}

func TestTypedFilterRelocatesWhenCandidateBucketsAreFull(t *testing.T) {
	f := NewTypedCuckooFilter(TypedFilterConfig{
		Capacity: 8, SlotsPerBucket: 1, Mode: TypedFilterCuckoo,
	})
	target := TypedFilterKey{PID: 9, Address: 0x4000, Type: FilterResident}
	targetFingerprint, first, second, hash := f.signature(target)
	start := first
	if hash>>63 != 0 {
		start = second
	}

	var victim typedFilterSlot
	var victimAlternate int
	for fingerprint := uint16(1); fingerprint != 0; fingerprint++ {
		alternate := f.alternateIndex(
			start, fingerprint, FilterPending)
		if alternate == first || alternate == second ||
			fingerprint == targetFingerprint {
			continue
		}
		victim = typedFilterSlot{
			fingerprint: fingerprint, references: 1,
			kind: FilterPending, occupied: true,
		}
		victimAlternate = alternate
		break
	}
	if !victim.occupied {
		t.Fatal("could not construct a relocatable victim")
	}
	f.bucket[start].slots[0] = victim
	other := second
	if other == start {
		other = first
	}
	f.bucket[other].slots[0] = typedFilterSlot{
		fingerprint: targetFingerprint ^ 0x1, references: 1,
		kind: FilterSeen, occupied: true,
	}

	if !f.Insert(target) {
		t.Fatal("insertion did not relocate a victim from full candidate buckets")
	}
	if !f.Contains(target) {
		t.Fatal("relocated insertion lost the new key")
	}
	if entry := f.bucket[victimAlternate].slots[0]; entry != victim {
		t.Fatalf("victim was not relocated to bucket %d", victimAlternate)
	}
	stats := f.Stats()
	if stats.KickAttempts == 0 || stats.KickedInsertions != 1 ||
		stats.KickRollbacks != 0 {
		t.Fatalf("unexpected relocation counters: %+v", stats)
	}
}

func TestTypedFilterLookupLatencyWidthAndReset(t *testing.T) {
	f := testTypedFilter(32)
	key := TypedFilterKey{PID: 4, Address: 0x2000, Type: FilterPending}
	if !f.Insert(key) {
		t.Fatal("insert failed")
	}
	lookup, ok := f.StartLookup(0, key)
	if !ok {
		t.Fatal("first lookup port reservation failed")
	}
	if _, ok := f.StartLookup(0, key); ok {
		t.Fatal("lookup width was not enforced")
	}
	if _, _, ready := f.CompleteLookup(1e-9, lookup); ready {
		t.Fatal("lookup completed before configured latency")
	}
	possible, reliable, ready := f.CompleteLookup(2e-9, lookup)
	if !ready || !reliable || !possible {
		t.Fatalf("completed lookup = (%v, %v, %v)", possible, reliable, ready)
	}
	f.Reset()
	if f.Contains(key) {
		t.Fatal("reset retained a signature")
	}
	stats := f.Stats()
	if stats.LookupPortStalls != 1 || stats.Occupancy != 0 {
		t.Fatalf("unexpected post-reset stats: %+v", stats)
	}
}

func TestTypedFilterUpdateLatencyAndWidth(t *testing.T) {
	f := testTypedFilter(32)
	key := TypedFilterKey{PID: 5, Address: 0x3000, Type: FilterPending}
	update, ok := f.StartUpdate(0, key, false)
	if !ok {
		t.Fatal("first update port reservation failed")
	}
	if _, ok := f.StartUpdate(0, key, false); ok {
		t.Fatal("update width was not enforced")
	}
	if _, ready := f.CompleteUpdate(0, update); ready {
		t.Fatal("update completed before configured latency")
	}
	if success, ready := f.CompleteUpdate(1e-9, update); !ready || !success {
		t.Fatalf("completed update = (%v, %v), want success and ready", success, ready)
	}
	if !f.Contains(key) || f.Stats().UpdatePortStalls != 1 {
		t.Fatalf("update did not commit with modeled contention: %+v", f.Stats())
	}
}

func TestTypedFilterScheduledLifecycleUpdatesFailOpenUntilVisible(t *testing.T) {
	f := NewTypedCuckooFilter(TypedFilterConfig{
		Capacity: 32, Mode: TypedFilterCuckoo,
		LookupLatencyCycles: 0, LookupWidth: 2,
		UpdateLatencyCycles: 1, UpdateWidth: 1,
		Freq: 1 * sim.GHz,
	})
	a := TypedFilterKey{PID: 1, Address: 0x1000, Type: FilterResident}
	b := TypedFilterKey{PID: 1, Address: 0x1040, Type: FilterResident}
	if !f.ScheduleUpdate(0, a, false) || !f.ScheduleUpdate(0, b, false) {
		t.Fatal("scheduled lifecycle insertion failed")
	}
	if f.Stats().UpdatePortStalls != 1 {
		t.Fatal("serialized scheduled update did not count port contention")
	}
	lookupA, _ := f.StartLookup(0, a)
	if _, reliable, ready := f.CompleteLookup(0, lookupA); !ready || reliable {
		t.Fatal("not-yet-visible scheduled update did not fail open")
	}
	lookupA, _ = f.StartLookup(1e-9, a)
	if possible, reliable, ready := f.CompleteLookup(1e-9, lookupA); !ready || !reliable || !possible {
		t.Fatal("first scheduled update was not visible after one cycle")
	}
	lookupB, _ := f.StartLookup(1e-9, b)
	if _, reliable, ready := f.CompleteLookup(1e-9, lookupB); !ready || reliable {
		t.Fatal("width-serialized second update became visible too early")
	}
	lookupB, _ = f.StartLookup(2e-9, b)
	if possible, reliable, ready := f.CompleteLookup(2e-9, lookupB); !ready || !reliable || !possible {
		t.Fatal("second scheduled update was not visible after serialization")
	}
}

func TestTypedFilterConfigurableFingerprintAndBucketSlots(t *testing.T) {
	f := NewTypedCuckooFilter(TypedFilterConfig{
		Capacity:        31,
		SlotsPerBucket:  2,
		FingerprintBits: 13,
		Mode:            TypedFilterCuckoo,
		LookupWidth:     1,
		UpdateWidth:     1,
		Freq:            1 * sim.GHz,
	})
	stats := f.Stats()
	if stats.SlotsPerBucket != 2 || stats.FingerprintBits != 13 ||
		stats.Slots < 31 {
		t.Fatalf("unexpected physical sensitivity point: %+v", stats)
	}
	for i := uint64(0); i < 1000; i++ {
		key := TypedFilterKey{PID: 1, Address: i * 64, Type: FilterResident}
		fingerprint, _, _, _ := f.signature(key)
		if fingerprint == 0 || fingerprint >= 1<<13 {
			t.Fatalf("13-bit fingerprint out of range: %d", fingerprint)
		}
	}
	wantBits := stats.Slots * uint64(13+3+1+typedFilterReferenceBits)
	if stats.EstimatedStorageBits != wantBits {
		t.Fatalf("storage bits = %d, want %d", stats.EstimatedStorageBits, wantBits)
	}
}

func TestTypedFilterReferenceCounterSaturationFailsOpen(t *testing.T) {
	f := testTypedFilter(32)
	key := TypedFilterKey{PID: 9, Address: 0x9000, Type: FilterPending}
	for i := 0; i < typedFilterMaxReferences; i++ {
		if !f.Insert(key) {
			t.Fatalf("reference %d failed before the four-bit counter saturated", i)
		}
	}
	if f.Insert(key) {
		t.Fatal("overflowing counted signature unexpectedly succeeded")
	}
	if f.exact[key] != typedFilterMaxReferences {
		t.Fatalf("failed counter overflow left %d exact references, want %d",
			f.exact[key], typedFilterMaxReferences)
	}
	possible, reliable := f.Query(key)
	if possible || reliable {
		t.Fatal("counter saturation did not force a type-local fail-open")
	}
	stats := f.Stats()
	if stats.ReferenceBits != typedFilterReferenceBits ||
		stats.ByType[FilterPending].InsertFailures != 1 ||
		stats.ByType[FilterPending].ReferenceCountSaturations != 1 ||
		stats.ByType[FilterResident].Reliable != true {
		t.Fatalf("unexpected saturation accounting: %+v", stats)
	}
}

func TestTypedFilterRejectsUnsupportedSensitivityPoints(t *testing.T) {
	for _, config := range []TypedFilterConfig{
		{Capacity: 1, SlotsPerBucket: 9, FingerprintBits: 16},
		{Capacity: 1, SlotsPerBucket: 4, FingerprintBits: 3},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("unsupported config did not panic: %+v", config)
				}
			}()
			NewTypedCuckooFilter(config)
		}()
	}
}
