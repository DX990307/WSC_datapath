package writeback

import (
	"sort"
	"sync"

	cachepkg "github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/sim"
)

// A1 (cross-kernel retention of read-only remote lines).
//
// The M3 kernel-boundary invalidation drops every remote replica line of a
// requester L2 when a kernel is launched. A1 drops only the replicas whose
// page (tracking region) was written since the requester's previous kernel
// boundary; replicas of regions nobody wrote stay valid across the boundary.
//
// RemoteRetainTracker models the home-side write epochs. Every write that
// changes the content of a physical address reports itself with NoteWrite at
// the time it is performed (home L2 write completion, DMA / host copy, buffer
// free). The tracker keeps a global epoch number that advances at kernel
// boundaries and, per tracking region, the last epoch in which the region was
// written. A requester GPM remembers the epoch current at its previous
// boundary (since); at its next boundary a replica line is stale exactly when
// its region was written in an epoch >= since. See
// isca27/results_index/a1/DESIGN.md for the correctness argument.
//
// In hardware the per-region epochs are the home's write sets: each home GPM
// keeps the set of its regions written during the current epoch and
// broadcasts it (exact list or a SignatureBits-bit Bloom signature) at the
// kernel boundary. SignatureBits > 0 models that signature: a region counts as
// written when every one of its hash bits is set in the home's signature of
// some epoch >= since. False positives only cause extra invalidations.

// HomeWriteObserver receives every write performed at a home (owner) memory.
type HomeWriteObserver interface {
	NoteWrite(now sim.VTimeInSec, addr, size uint64)
}

// RemoteRetainConfig configures the tracker.
type RemoteRetainConfig struct {
	// Log2RegionSize is the tracking granularity (12 = 4 KiB page).
	Log2RegionSize uint
	// Log2OwnerSize gives the home GPM of a physical address
	// (addr >> Log2OwnerSize). Only used by the signature mode and stats.
	Log2OwnerSize uint
	// SignatureBits is the per-home, per-epoch Bloom signature size in bits.
	// 0 = exact write sets.
	SignatureBits uint64
	// SignatureHashes is the number of hash functions of the signature.
	SignatureHashes int
}

// RemoteRetainStats reports the tracker's work.
type RemoteRetainStats struct {
	Epochs                 uint64 // epochs opened by kernel boundaries
	Boundaries             uint64 // requester boundary checks
	WriteNotes             uint64 // NoteWrite calls (home L2 + host/driver)
	RegionEpochWrites      uint64 // distinct (epoch, region) write-set entries
	MaxRegionsOwnerEpoch   uint64 // largest write set of one home in one epoch
	MaxRegionsEpoch        uint64 // largest write set of one epoch, all homes
	Queries                uint64 // replica lines checked at boundaries
	ExactWritten           uint64 // ... whose region was really written
	SignatureFalsePositive uint64 // ... signature said written, exact did not
}

type retainOwnerEpoch struct {
	epoch uint64
	owner uint64
}

// RemoteRetainTracker is shared by all GPMs of a platform.
type RemoteRetainTracker struct {
	mu  sync.Mutex
	cfg RemoteRetainConfig

	epoch          uint64
	epochHasWrites bool

	lastWrite map[uint64]uint64 // region -> last epoch written (+1; 0 = never)

	requesterEpoch []uint64

	signatures      map[retainOwnerEpoch][]uint64
	ownerEpochCount map[uint64]uint64 // owner -> regions written this epoch
	epochCount      uint64

	stats RemoteRetainStats
}

// NewRemoteRetainTracker creates a tracker.
func NewRemoteRetainTracker(cfg RemoteRetainConfig) *RemoteRetainTracker {
	if cfg.Log2RegionSize == 0 {
		cfg.Log2RegionSize = 12
	}
	if cfg.Log2RegionSize < 6 {
		cfg.Log2RegionSize = 6
	}
	if cfg.Log2OwnerSize == 0 {
		cfg.Log2OwnerSize = 33
	}
	if cfg.SignatureHashes <= 0 {
		cfg.SignatureHashes = 2
	}
	return &RemoteRetainTracker{
		cfg:             cfg,
		lastWrite:       make(map[uint64]uint64),
		signatures:      make(map[retainOwnerEpoch][]uint64),
		ownerEpochCount: make(map[uint64]uint64),
	}
}

// Config returns the tracker configuration.
func (t *RemoteRetainTracker) Config() RemoteRetainConfig { return t.cfg }

// Stats returns a snapshot of the counters.
func (t *RemoteRetainTracker) Stats() RemoteRetainStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}

// RegisterRequester adds one requester GPM and returns its id.
func (t *RemoteRetainTracker) RegisterRequester() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.requesterEpoch = append(t.requesterEpoch, 0)
	return len(t.requesterEpoch) - 1
}

// NoteWrite records that [addr, addr+size) was written at time now.
func (t *RemoteRetainTracker) NoteWrite(now sim.VTimeInSec, addr, size uint64) {
	if size == 0 {
		size = 1
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stats.WriteNotes++
	first := addr >> t.cfg.Log2RegionSize
	last := (addr + size - 1) >> t.cfg.Log2RegionSize
	for region := first; region <= last; region++ {
		t.noteRegion(region)
	}
}

func (t *RemoteRetainTracker) noteRegion(region uint64) {
	t.epochHasWrites = true
	if t.lastWrite[region] == t.epoch+1 {
		return
	}
	t.lastWrite[region] = t.epoch + 1
	t.stats.RegionEpochWrites++
	owner := t.ownerOfRegion(region)
	t.ownerEpochCount[owner]++
	if t.ownerEpochCount[owner] > t.stats.MaxRegionsOwnerEpoch {
		t.stats.MaxRegionsOwnerEpoch = t.ownerEpochCount[owner]
	}
	t.epochCount++
	if t.epochCount > t.stats.MaxRegionsEpoch {
		t.stats.MaxRegionsEpoch = t.epochCount
	}
	if t.cfg.SignatureBits > 0 {
		key := retainOwnerEpoch{epoch: t.epoch, owner: owner}
		sig := t.signatures[key]
		if sig == nil {
			sig = make([]uint64, (t.cfg.SignatureBits+63)/64)
			t.signatures[key] = sig
		}
		for i := 0; i < t.cfg.SignatureHashes; i++ {
			bit := t.signatureBit(region, i)
			sig[bit/64] |= 1 << (bit % 64)
		}
	}
}

func (t *RemoteRetainTracker) ownerOfRegion(region uint64) uint64 {
	if t.cfg.Log2OwnerSize <= t.cfg.Log2RegionSize {
		return region
	}
	return region >> (t.cfg.Log2OwnerSize - t.cfg.Log2RegionSize)
}

func retainMix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func (t *RemoteRetainTracker) signatureBit(region uint64, i int) uint64 {
	return retainMix(region*0x100000001b3+uint64(i)*0x632be59bd9b4e019) %
		t.cfg.SignatureBits
}

// BeginBoundary is called when requester id accepts a kernel launch. It
// returns the epoch the requester's previous boundary opened (the "since"
// argument of WrittenSince) and makes the requester's next window start now.
// A new epoch is opened only when a write happened in the current one, so the
// GPMs that launch the same kernel at slightly different times share one
// epoch unless a write falls in between.
func (t *RemoteRetainTracker) BeginBoundary(id int, now sim.VTimeInSec) uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stats.Boundaries++
	// Collect before this requester moves on: its own query below still
	// uses the old epoch.
	t.collectSignatures()
	since := t.requesterEpoch[id]
	if t.epochHasWrites {
		t.epoch++
		t.epochHasWrites = false
		t.stats.Epochs++
		clear(t.ownerEpochCount)
		t.epochCount = 0
	}
	t.requesterEpoch[id] = t.epoch
	return since
}

func (t *RemoteRetainTracker) collectSignatures() {
	if t.cfg.SignatureBits == 0 || len(t.requesterEpoch) == 0 {
		return
	}
	oldest := t.requesterEpoch[0]
	for _, e := range t.requesterEpoch {
		if e < oldest {
			oldest = e
		}
	}
	for key := range t.signatures {
		if key.epoch < oldest {
			delete(t.signatures, key)
		}
	}
}

// WrittenSince reports whether the region of addr was (possibly, in signature
// mode) written in an epoch >= since.
func (t *RemoteRetainTracker) WrittenSince(addr uint64, since uint64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stats.Queries++
	region := addr >> t.cfg.Log2RegionSize
	last := t.lastWrite[region]
	exact := last != 0 && last-1 >= since
	if exact {
		t.stats.ExactWritten++
	}
	if t.cfg.SignatureBits == 0 {
		return exact
	}
	owner := t.ownerOfRegion(region)
	for e := since; e <= t.epoch; e++ {
		sig := t.signatures[retainOwnerEpoch{epoch: e, owner: owner}]
		if sig == nil {
			continue
		}
		hit := true
		for i := 0; i < t.cfg.SignatureHashes; i++ {
			bit := t.signatureBit(region, i)
			if sig[bit/64]&(1<<(bit%64)) == 0 {
				hit = false
				break
			}
		}
		if hit {
			if !exact {
				t.stats.SignatureFalsePositive++
			}
			return true
		}
	}
	if exact {
		panic("A1 signature missed a written region")
	}
	return false
}

// SetHomeWriteObserver makes this L2 slice report every write it performs to
// its home memory. nil (the default) reports nothing.
func (c *Cache) SetHomeWriteObserver(o HomeWriteObserver) {
	c.homeWriteObserver = o
}

// AddHomeWriteObserver adds an observer next to the existing one.
func (c *Cache) AddHomeWriteObserver(o HomeWriteObserver) {
	if c.homeWriteObserver == nil {
		c.homeWriteObserver = o
		return
	}
	c.homeWriteObserver = homeWriteObservers{c.homeWriteObserver, o}
}

type homeWriteObservers []HomeWriteObserver

func (h homeWriteObservers) NoteWrite(now sim.VTimeInSec, addr, size uint64) {
	for _, o := range h {
		o.NoteWrite(now, addr, size)
	}
}

func (c *Cache) noteHomeWrite(now sim.VTimeInSec, addr uint64, size uint64) {
	if c.homeWriteObserver == nil {
		return
	}
	c.homeWriteObserver.NoteWrite(now, addr, size)
}

// RemoteRetainBoundaryStats counts the A1 selective boundary work.
type RemoteRetainBoundaryStats struct {
	Boundaries          uint64 // selective boundaries at this slice
	LinesChecked        uint64 // replica lines checked
	LinesInvalidated    uint64 // ... dropped (region written since last boundary)
	LinesRetained       uint64 // ... kept across the boundary
	RetainedHits        uint64 // replica hits on lines kept across >= 1 boundary
	RetainedLinesUsed   uint64 // kept lines hit at least once after being kept
	RetainedLinesUnused uint64 // kept lines evicted/invalidated without such a hit
}

// GetRemoteRetainBoundaryStats returns the A1 counters.
func (c *Cache) GetRemoteRetainBoundaryStats() RemoteRetainBoundaryStats {
	return c.retainStats
}

// InvalidateWrittenRemoteReplicas is the A1 version of
// InvalidateRemoteReplicas: only replica lines for which written(line)
// reports true are invalidated; the others stay valid (and stay in the
// RESIDENT filter). As in the M3 invalidation, a remote fill sent at or before
// the boundary is dropped on arrival (its data may predate the boundary), and
// the work is a flash operation that takes no simulated time. It returns the
// number of lines invalidated and retained.
func (c *Cache) InvalidateWrittenRemoteReplicas(
	now sim.VTimeInSec,
	written func(line uint64) bool,
) (invalidated, retained int) {
	if c.remoteReplicaFilter == nil {
		return 0, 0
	}
	c.replicaBoundarySet = true
	c.replicaBoundaryTime = now
	c.replicaBoundaryStats.Invalidations++
	c.retainStats.Boundaries++
	blocks := make([]*cachepkg.Block, 0, len(c.remoteReplicaBlocks))
	for block := range c.remoteReplicaBlocks {
		blocks = append(blocks, block)
	}
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].CacheAddress < blocks[j].CacheAddress
	})
	for _, block := range blocks {
		record := c.remoteReplicaBlocks[block]
		c.retainStats.LinesChecked++
		if !c.replicaStale(record, written) {
			if !record.retained {
				record.retained = true
				record.usedAfterRetain = false
			}
			retained++
			continue
		}
		c.obsL2InvalidateBlock(block)
		c.rcCoherence = true
		c.untrackRemoteReplica(block)
		c.rcCoherence = false
		c.untrackResidentBlock(block)
		block.IsValid = false
		block.IsDirty = false
		invalidated++
	}
	c.replicaBoundaryStats.LinesInvalidated += uint64(invalidated)
	c.retainStats.LinesInvalidated += uint64(invalidated)
	c.retainStats.LinesRetained += uint64(retained)
	return invalidated, retained
}

func (c *Cache) recordRetainedHit(record *remoteReplicaRecord) {
	if !record.retained {
		return
	}
	c.retainStats.RetainedHits++
	if !record.usedAfterRetain {
		record.usedAfterRetain = true
		c.retainStats.RetainedLinesUsed++
	}
}

func (c *Cache) recordRetainedRetirement(record *remoteReplicaRecord) {
	if record == nil || !record.retained || record.usedAfterRetain {
		return
	}
	c.retainStats.RetainedLinesUnused++
}
