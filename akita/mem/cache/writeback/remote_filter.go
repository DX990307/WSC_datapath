package writeback

import (
	"math"

	"github.com/sarchlab/akita/v3/mem/vm"
)

const (
	remoteReplicaFilterSlotsPerBucket = 4
	remoteReplicaFilterMaxKicks       = 500
)

// remoteReplicaFilter is a deterministic counting cuckoo filter. The bucket
// count is always a power of two, making alternateIndex an involution.
//
// A slot has a reference count because distinct keys can have the same
// fingerprint and candidate buckets. Keeping one counted signature prevents
// deleting one key from creating a false negative for the other keys.
type remoteReplicaFilter struct {
	buckets []remoteReplicaFilterBucket
	mask    uint64
}

type remoteReplicaFilterBucket struct {
	slots [remoteReplicaFilterSlotsPerBucket]remoteReplicaFilterSlot
}

type remoteReplicaFilterSlot struct {
	fingerprint uint16
	references  uint64
	occupied    bool
}

type remoteReplicaFilterMutation struct {
	bucket   int
	slot     int
	previous remoteReplicaFilterSlot
}

// newRemoteReplicaFilter creates a filter with at least capacity slots. The
// actual number of slots is rounded up so that the bucket count is a power of
// two. A minimum of two buckets keeps the two candidate buckets distinct.
func newRemoteReplicaFilter(capacity int) *remoteReplicaFilter {
	bucketCount := remoteReplicaFilterBucketCount(capacity)
	return &remoteReplicaFilter{
		buckets: make([]remoteReplicaFilterBucket, bucketCount),
		mask:    uint64(bucketCount - 1),
	}
}

func remoteReplicaFilterBucketCount(capacity int) int {
	if capacity < 1 {
		capacity = 1
	}

	required := (capacity-1)/remoteReplicaFilterSlotsPerBucket + 1
	bucketCount := 2
	for bucketCount < required {
		if bucketCount > int(^uint(0)>>1)/2 {
			panic("remote replica filter capacity is too large")
		}
		bucketCount <<= 1
	}
	return bucketCount
}

// Contains reports possible membership. As with any cuckoo filter, true can
// be a false positive, while a successfully inserted signature remains
// discoverable until its matching number of Delete calls have completed.
func (f *remoteReplicaFilter) Contains(pid vm.PID, lineAddr uint64) bool {
	if f == nil || len(f.buckets) == 0 {
		return false
	}

	fingerprint, first, second, _ := f.signature(pid, lineAddr)
	_, _, found := f.findFingerprint(first, second, fingerprint)
	return found
}

// Insert adds one reference to a key signature. It returns false only when a
// new signature cannot be placed after the bounded kick sequence. A failed
// insertion rolls every slot mutation back in reverse order.
func (f *remoteReplicaFilter) Insert(pid vm.PID, lineAddr uint64) bool {
	if f == nil || len(f.buckets) == 0 {
		return false
	}

	fingerprint, first, second, hash := f.signature(pid, lineAddr)
	if bucket, slot, found := f.findFingerprint(
		first, second, fingerprint,
	); found {
		if f.buckets[bucket].slots[slot].references == math.MaxUint64 {
			return false
		}
		f.buckets[bucket].slots[slot].references++
		return true
	}

	incoming := remoteReplicaFilterSlot{
		fingerprint: fingerprint,
		references:  1,
		occupied:    true,
	}

	start, alternate := first, second
	if hash>>63 != 0 {
		start, alternate = second, first
	}
	if f.insertIntoEmptySlot(start, incoming, nil) ||
		f.insertIntoEmptySlot(alternate, incoming, nil) {
		return true
	}

	mutations := make([]remoteReplicaFilterMutation, 0,
		remoteReplicaFilterMaxKicks)
	bucket := start
	for kick := 0; kick < remoteReplicaFilterMaxKicks; kick++ {
		slotIndex := f.victimSlot(hash, incoming.fingerprint, bucket, kick)
		victim := f.buckets[bucket].slots[slotIndex]
		f.replaceSlot(bucket, slotIndex, incoming, &mutations)

		incoming = victim
		bucket = f.alternateIndex(bucket, incoming.fingerprint)

		if matchingBucket, matchingSlot, found := f.findFingerprintInBucket(
			bucket, incoming.fingerprint,
		); found {
			updated := f.buckets[matchingBucket].slots[matchingSlot]
			if math.MaxUint64-updated.references < incoming.references {
				f.rollback(mutations)
				return false
			}
			updated.references += incoming.references
			f.replaceSlot(matchingBucket, matchingSlot, updated, &mutations)
			return true
		}

		if f.insertIntoEmptySlot(bucket, incoming, &mutations) {
			return true
		}
	}

	f.rollback(mutations)
	return false
}

// Delete removes one reference to a key signature. Calling Delete for a key
// that was not inserted is outside the filter contract; an absent signature is
// nevertheless treated as a no-op.
func (f *remoteReplicaFilter) Delete(pid vm.PID, lineAddr uint64) {
	if f == nil || len(f.buckets) == 0 {
		return
	}

	fingerprint, first, second, _ := f.signature(pid, lineAddr)
	bucket, slot, found := f.findFingerprint(first, second, fingerprint)
	if !found {
		return
	}

	entry := &f.buckets[bucket].slots[slot]
	if entry.references > 1 {
		entry.references--
		return
	}
	*entry = remoteReplicaFilterSlot{}
}

// Reset removes all signatures without changing the configured capacity.
func (f *remoteReplicaFilter) Reset() {
	if f == nil {
		return
	}
	for i := range f.buckets {
		f.buckets[i] = remoteReplicaFilterBucket{}
	}
}

func (f *remoteReplicaFilter) signature(
	pid vm.PID,
	lineAddr uint64,
) (fingerprint uint16, first, second int, hash uint64) {
	hash = remoteReplicaFilterMix(
		lineAddr ^ remoteReplicaFilterMix(uint64(pid)+0x9e3779b97f4a7c15),
	)
	fingerprint = uint16(hash ^ (hash >> 16) ^ (hash >> 32) ^ (hash >> 48))
	first = int(hash & f.mask)
	second = f.alternateIndex(first, fingerprint)
	return fingerprint, first, second, hash
}

func (f *remoteReplicaFilter) alternateIndex(
	bucket int,
	fingerprint uint16,
) int {
	delta := remoteReplicaFilterMix(uint64(fingerprint)+
		0x517cc1b727220a95) & f.mask
	if delta == 0 {
		delta = 1
	}
	return bucket ^ int(delta)
}

func (f *remoteReplicaFilter) findFingerprint(
	first int,
	second int,
	fingerprint uint16,
) (bucket int, slot int, found bool) {
	if bucket, slot, found = f.findFingerprintInBucket(
		first, fingerprint,
	); found {
		return bucket, slot, true
	}
	if second != first {
		return f.findFingerprintInBucket(second, fingerprint)
	}
	return 0, 0, false
}

func (f *remoteReplicaFilter) findFingerprintInBucket(
	bucket int,
	fingerprint uint16,
) (foundBucket int, foundSlot int, found bool) {
	for i := range f.buckets[bucket].slots {
		entry := f.buckets[bucket].slots[i]
		if entry.occupied && entry.fingerprint == fingerprint {
			return bucket, i, true
		}
	}
	return 0, 0, false
}

func (f *remoteReplicaFilter) insertIntoEmptySlot(
	bucket int,
	incoming remoteReplicaFilterSlot,
	mutations *[]remoteReplicaFilterMutation,
) bool {
	for i := range f.buckets[bucket].slots {
		if f.buckets[bucket].slots[i].occupied {
			continue
		}
		if mutations == nil {
			f.buckets[bucket].slots[i] = incoming
		} else {
			f.replaceSlot(bucket, i, incoming, mutations)
		}
		return true
	}
	return false
}

func (f *remoteReplicaFilter) replaceSlot(
	bucket int,
	slot int,
	replacement remoteReplicaFilterSlot,
	mutations *[]remoteReplicaFilterMutation,
) {
	*mutations = append(*mutations, remoteReplicaFilterMutation{
		bucket:   bucket,
		slot:     slot,
		previous: f.buckets[bucket].slots[slot],
	})
	f.buckets[bucket].slots[slot] = replacement
}

func (f *remoteReplicaFilter) rollback(
	mutations []remoteReplicaFilterMutation,
) {
	for i := len(mutations) - 1; i >= 0; i-- {
		mutation := mutations[i]
		f.buckets[mutation.bucket].slots[mutation.slot] = mutation.previous
	}
}

func (f *remoteReplicaFilter) victimSlot(
	keyHash uint64,
	fingerprint uint16,
	bucket int,
	kick int,
) int {
	selection := remoteReplicaFilterMix(
		keyHash ^
			(uint64(fingerprint) << 32) ^
			(uint64(bucket) * 0x94d049bb133111eb) ^
			(uint64(kick+1) * 0xbf58476d1ce4e5b9),
	)
	return int(selection % remoteReplicaFilterSlotsPerBucket)
}

// remoteReplicaFilterMix is SplitMix64's stateless finalizer.
func remoteReplicaFilterMix(value uint64) uint64 {
	value += 0x9e3779b97f4a7c15
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}
