package writeback

import (
	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

type cacheState int

const (
	cacheStateInvalid cacheState = iota
	cacheStateRunning
	cacheStatePreFlushing
	cacheStateFlushing
	cacheStatePaused
)

// A Cache in the writeback package is a cache that performs the write-back policy.
type Cache struct {
	*sim.TickingComponent

	topPort     sim.Port
	bottomPort  sim.Port
	controlPort sim.Port

	dirStageBuffer           sim.Buffer
	dirToBankBuffers         []sim.Buffer
	writeBufferToBankBuffers []sim.Buffer
	mshrStageBuffer          sim.Buffer
	writeBufferBuffer        sim.Buffer

	topSender         sim.BufferedSender
	bottomSender      sim.BufferedSender
	controlPortSender sim.BufferedSender

	topParser   *topParser
	writeBuffer *writeBufferStage
	dirStage    *directoryStage
	bankStages  []*bankStage
	mshrStage   *mshrStage
	flusher     *flusher

	storage         *mem.Storage
	lowModuleFinder mem.LowModuleFinder
	directory       cache.Directory
	mshr            cache.MSHR
	mshrCapacity    int
	log2BlockSize   uint64
	numReqPerCycle  int

	requestFilter                *TypedCuckooFilter
	remoteReplicaFilter          lineMembershipFilter
	remoteReplicaBlocks          map[*cache.Block]*remoteReplicaRecord
	residentFilter               lineMembershipFilter
	residentFilterBlocks         map[*cache.Block]residentFilterKey
	residentFilterEnabled        bool
	residentFilterReliable       bool
	residentFilterStats          ResidentFilterStats
	filterPrefetchEnabled        bool
	filterPrefetchPredictorOnly  bool
	filterPrefetchUngated        bool
	filterPrefetcher             *DemandStridePredictor
	localPrefetchPatternFilters  map[TypedFilterKey]*TypedCuckooFilter
	localPrefetchPeers           []*Cache
	localPrefetchInterleave      uint64
	localPrefetchLeader          bool
	localPrefetchCandidate       *localPrefetchCandidate
	localPrefetchByLine          map[localPrefetchLineKey]*localPrefetchRecord
	localPrefetchByBlock         map[*cache.Block]*localPrefetchRecord
	localPrefetchStats           LocalFilterPrefetchStats
	localPrefetchOutstanding     int
	granularityAdaptationEnabled bool
	granularityWithoutFilter     bool
	granularityAlwaysExpand      bool
	granularityPredictorOnly     bool
	granularityPredictor         *DemandStridePredictor
	granularityPatternFilters    map[TypedFilterKey]*TypedCuckooFilter
	granularityLeader            bool
	granularityByLine            map[granularityLineKey]*granularityRecord
	granularityByBlock           map[*cache.Block]*granularityRecord
	granularityStats             GranularityAdaptationStats
	adaptivePairEnabled          bool
	adaptivePairAdapter          *adaptivePairAdapter
	adaptivePairStats            AdaptivePairStats
	interleaving                 bool
	interleavingBlocks           int
	interleavingUnits            int
	interleavingIndex            int

	observationL2Accesses   uint64
	remoteReplicaStats      RemoteReplicaStats
	remoteReplicaGeneration uint64

	localMemoryPathStats LocalMemoryPathStats
	fillForwarding       bool
	fillForwardingStats  FillForwardingStats

	state                cacheState
	inFlightTransactions []*transaction
	evictingList         map[uint64]bool
}

// GetAdaptivePairStats returns the counters for the restored historical M1
// adapter with one aligned 128-B controller-level read.
func (c *Cache) GetAdaptivePairStats() AdaptivePairStats {
	return c.adaptivePairStats
}

// RequestFilter exposes the one physical metadata filter owned by this L2
// slice to the requester-local RDMA metadata interface.
func (c *Cache) RequestFilter() *TypedCuckooFilter {
	return c.requestFilter
}

// GetTypedFilterStats returns shared-array and per-logical-type counters.
func (c *Cache) GetTypedFilterStats() TypedFilterStats {
	if c.requestFilter == nil {
		return TypedFilterStats{}
	}
	return c.requestFilter.Stats()
}

// SetLowModuleFinder sets the LowModuleFinder used by the cache.
func (c *Cache) SetLowModuleFinder(lmf mem.LowModuleFinder) {
	c.lowModuleFinder = lmf
}

// Tick updates the internal states of the Cache.
func (c *Cache) Tick(now sim.VTimeInSec) bool {
	madeProgress := false

	madeProgress = c.controlPortSender.Tick(now) || madeProgress
	if c.state != cacheStateRunning {
		madeProgress = c.topParser.processRemoteFillWhileStopped(now) || madeProgress
	}

	if c.state != cacheStatePaused {
		madeProgress = c.runPipeline(now) || madeProgress
	} else {
		madeProgress = c.runStage(now, c.topSender) || madeProgress
	}

	madeProgress = c.flusher.Tick(now) || madeProgress

	return madeProgress
}

func (c *Cache) runPipeline(now sim.VTimeInSec) bool {
	madeProgress := false

	madeProgress = c.runStage(now, c.topSender) || madeProgress
	madeProgress = c.runStage(now, c.bottomSender) || madeProgress
	madeProgress = c.runStage(now, c.mshrStage) || madeProgress

	for _, bs := range c.bankStages {
		madeProgress = bs.Tick(now) || madeProgress
	}

	madeProgress = c.runStage(now, c.writeBuffer) || madeProgress
	// directoryStage already accepts and retires up to numReqPerCycle requests
	// per Tick and its pipeline has the same width. Calling it through
	// runStage advanced every pipeline stage numReqPerCycle times at the same
	// simulation timestamp, collapsing a configured 10-cycle lookup to zero.
	madeProgress = c.dirStage.Tick(now) || madeProgress
	madeProgress = c.runStage(now, c.topParser) || madeProgress
	// Speculation is deliberately last. A real request consumes every cache
	// pipeline opportunity first; the prefetcher only uses an otherwise idle
	// front-end slot and drops its candidate on any conflict.
	madeProgress = c.runLocalFilterPrefetch(now) || madeProgress

	return madeProgress
}

func (c *Cache) runStage(now sim.VTimeInSec, stage sim.Ticker) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = stage.Tick(now) || madeProgress
	}
	return madeProgress
}

func (c *Cache) discardInflightTransactions(now sim.VTimeInSec) {
	sets := c.directory.GetSets()
	for _, set := range sets {
		for _, block := range set.Blocks {
			block.ReadCount = 0
			block.IsLocked = false
		}
	}

	c.dirStage.Reset(now)
	for _, bs := range c.bankStages {
		bs.Reset(now)
	}
	c.mshrStage.Reset(now)
	c.writeBuffer.Reset(now)

	c.topSender.Clear()
	c.discardTopPort(now)

	// for _, t := range c.inFlightTransactions {
	// 	fmt.Printf("%.10f, %s, transaction %s discarded due to flushing\n",
	// 		now, c.Name(), t.id)
	// }

	c.inFlightTransactions = nil
}
