package writeback

import (
	"reflect"
	"testing"

	"github.com/sarchlab/akita/v3/mem/vm"
)

func TestRemoteReplicaFilterInsertContainsDelete(t *testing.T) {
	filter := newRemoteReplicaFilter(32)
	pid := vm.PID(7)
	line := uint64(0x12340)

	if filter.Contains(pid, line) {
		t.Fatal("an empty filter reported a hit")
	}
	if !filter.Insert(pid, line) {
		t.Fatal("insert into an empty filter failed")
	}
	if !filter.Contains(pid, line) {
		t.Fatal("inserted key is missing")
	}

	filter.Delete(pid, line)
	if filter.Contains(pid, line) {
		t.Fatal("deleted key is still present")
	}
}

func TestRemoteReplicaFilterCountsDuplicateInsertions(t *testing.T) {
	filter := newRemoteReplicaFilter(8)
	pid := vm.PID(3)
	line := uint64(0x8000)

	if !filter.Insert(pid, line) || !filter.Insert(pid, line) {
		t.Fatal("duplicate insertion failed")
	}

	fingerprint, first, second, _ := filter.signature(pid, line)
	bucket, slot, found := filter.findFingerprint(first, second, fingerprint)
	if !found {
		t.Fatal("duplicate signature is missing")
	}
	if got := filter.buckets[bucket].slots[slot].references; got != 2 {
		t.Fatalf("reference count is %d, want 2", got)
	}

	filter.Delete(pid, line)
	if !filter.Contains(pid, line) {
		t.Fatal("first delete removed a multiply inserted signature")
	}
	filter.Delete(pid, line)
	if filter.Contains(pid, line) {
		t.Fatal("signature remains after matching deletes")
	}
}

func TestRemoteReplicaFilterCountsDistinctKeysWithSameSignature(t *testing.T) {
	filter := newRemoteReplicaFilter(8)
	pid := vm.PID(11)
	firstLine, secondLine := findRemoteReplicaFilterSignatureCollision(
		t, filter, pid,
	)

	if !filter.Insert(pid, firstLine) || !filter.Insert(pid, secondLine) {
		t.Fatal("inserting colliding signatures failed")
	}

	fingerprint, first, second, _ := filter.signature(pid, firstLine)
	bucket, slot, found := filter.findFingerprint(first, second, fingerprint)
	if !found {
		t.Fatal("colliding signature is missing")
	}
	if got := filter.buckets[bucket].slots[slot].references; got != 2 {
		t.Fatalf("colliding signature count is %d, want 2", got)
	}

	filter.Delete(pid, firstLine)
	if !filter.Contains(pid, secondLine) {
		t.Fatal("deleting one collision created a false negative for the other")
	}
	filter.Delete(pid, secondLine)
	if filter.Contains(pid, secondLine) {
		t.Fatal("colliding signature remains after both deletes")
	}
}

func TestRemoteReplicaFilterFailedInsertRollsBack(t *testing.T) {
	filter := newRemoteReplicaFilter(8)
	pid := vm.PID(1)
	lines := remoteReplicaFilterLinesWithUniqueFingerprints(
		t, filter, pid, 9,
	)

	for _, line := range lines[:8] {
		if !filter.Insert(pid, line) {
			t.Fatalf("failed to populate full filter with line %#x", line)
		}
	}

	before := append([]remoteReplicaFilterBucket(nil), filter.buckets...)
	if filter.Insert(pid, lines[8]) {
		t.Fatal("insert unexpectedly succeeded in a full two-bucket filter")
	}
	if !reflect.DeepEqual(before, filter.buckets) {
		t.Fatal("failed insertion did not roll back all bucket mutations")
	}

	for _, line := range lines[:8] {
		if !filter.Contains(pid, line) {
			t.Fatalf("failed insertion lost existing line %#x", line)
		}
	}
	if filter.Contains(pid, lines[8]) {
		t.Fatal("failed insertion left the rejected signature in the filter")
	}
}

func TestRemoteReplicaFilterIsDeterministic(t *testing.T) {
	first := newRemoteReplicaFilter(16)
	second := newRemoteReplicaFilter(16)
	pid := vm.PID(9)

	for i := uint64(0); i < 64; i++ {
		line := i * 64
		firstResult := first.Insert(pid, line)
		secondResult := second.Insert(pid, line)
		if firstResult != secondResult {
			t.Fatalf("insert result diverged at line %#x", line)
		}
		if !reflect.DeepEqual(first.buckets, second.buckets) {
			t.Fatalf("filter state diverged at line %#x", line)
		}
	}
}

func TestRemoteReplicaFilterReset(t *testing.T) {
	filter := newRemoteReplicaFilter(16)
	pid := vm.PID(5)
	lines := []uint64{0x1000, 0x2000, 0x3000}

	for _, line := range lines {
		if !filter.Insert(pid, line) {
			t.Fatalf("failed to insert line %#x", line)
		}
	}
	filter.Reset()

	for _, line := range lines {
		if filter.Contains(pid, line) {
			t.Fatalf("reset retained line %#x", line)
		}
	}
	if got := len(filter.buckets); got != 4 {
		t.Fatalf("reset changed bucket count to %d, want 4", got)
	}
}

type remoteReplicaFilterTestSignature struct {
	fingerprint uint16
	first       int
	second      int
}

func findRemoteReplicaFilterSignatureCollision(
	t *testing.T,
	filter *remoteReplicaFilter,
	pid vm.PID,
) (uint64, uint64) {
	t.Helper()
	seen := make(map[remoteReplicaFilterTestSignature]uint64)
	for i := uint64(0); i < 1<<20; i++ {
		line := i * 64
		fingerprint, first, second, _ := filter.signature(pid, line)
		if second < first {
			first, second = second, first
		}
		signature := remoteReplicaFilterTestSignature{
			fingerprint: fingerprint,
			first:       first,
			second:      second,
		}
		if prior, ok := seen[signature]; ok && prior != line {
			return prior, line
		}
		seen[signature] = line
	}
	t.Fatal("could not find two keys with the same filter signature")
	return 0, 0
}

func remoteReplicaFilterLinesWithUniqueFingerprints(
	t *testing.T,
	filter *remoteReplicaFilter,
	pid vm.PID,
	count int,
) []uint64 {
	t.Helper()
	used := make(map[uint16]bool)
	lines := make([]uint64, 0, count)
	for i := uint64(0); i < 1<<20 && len(lines) < count; i++ {
		line := i * 64
		fingerprint, _, _, _ := filter.signature(pid, line)
		if used[fingerprint] {
			continue
		}
		used[fingerprint] = true
		lines = append(lines, line)
	}
	if len(lines) != count {
		t.Fatalf("found %d unique fingerprints, want %d", len(lines), count)
	}
	return lines
}
