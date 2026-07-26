package writeback

import (
	"fmt"

	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/pipelining"
	"github.com/sarchlab/akita/v3/sim"
)

// A Builder can build writeback caches
type Builder struct {
	engine           sim.Engine
	freq             sim.Freq
	lowModuleFinder  mem.LowModuleFinder
	wayAssociativity int
	log2BlockSize    uint64

	interleaving          bool
	numInterleavingBlock  int
	interleavingUnitCount int
	interleavingUnitIndex int

	byteSize            uint64
	numMSHREntry        int
	numReqPerCycle      int
	writeBufferCapacity int
	maxInflightFetch    int
	maxInflightEviction int

	dirLatency  int
	bankLatency int

	remoteReplicaFilter     bool
	residentFilter          bool
	fillForwarding          bool
	filterPrefetch          bool
	prefetchPredictorOnly   bool
	prefetchUngated         bool
	prefetchStreams         int
	granularityAdaptation   bool
	granularityNoFilter     bool
	granularityAlways       bool
	granularityPredictor    bool
	adaptivePair            bool
	adaptivePairRegionLines int
	typedFilter             bool
	typedFilterConfig       TypedFilterConfig
}

// MakeBuilder creates a new builder with default configurations.
func MakeBuilder() Builder {
	return Builder{
		freq:                    1 * sim.GHz,
		wayAssociativity:        4,
		log2BlockSize:           6,
		byteSize:                512 * mem.KB,
		numMSHREntry:            16,
		numReqPerCycle:          1,
		writeBufferCapacity:     1024,
		adaptivePairRegionLines: 2,
		maxInflightFetch:        128,
		maxInflightEviction:     128,
		bankLatency:             10,
		prefetchStreams:         64,
		typedFilterConfig: TypedFilterConfig{
			Mode:                TypedFilterCuckoo,
			LookupLatencyCycles: 1,
			UpdateLatencyCycles: 1,
		},
	}
}

// WithEngine sets the engine to be used by the caches.
func (b Builder) WithEngine(engine sim.Engine) Builder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency to be used by the caches.
func (b Builder) WithFreq(freq sim.Freq) Builder {
	b.freq = freq
	return b
}

// WithWayAssociativity sets the way associativity.
func (b Builder) WithWayAssociativity(n int) Builder {
	b.wayAssociativity = n
	return b
}

// WithLog2BlockSize sets the cache line size as the power of 2.
func (b Builder) WithLog2BlockSize(n uint64) Builder {
	b.log2BlockSize = n
	return b
}

// WithNumMSHREntry sets the number of MSHR entries.
func (b Builder) WithNumMSHREntry(n int) Builder {
	b.numMSHREntry = n
	return b
}

// WithLowModuleFinder sets the LowModuleFinder to be used.
func (b Builder) WithLowModuleFinder(f mem.LowModuleFinder) Builder {
	b.lowModuleFinder = f
	return b
}

// WithNumReqPerCycle sets the number of requests that can be processed by the
// cache in each cycle.
func (b Builder) WithNumReqPerCycle(n int) Builder {
	b.numReqPerCycle = n
	return b
}

// WithByteSize set the size of the cache.
func (b Builder) WithByteSize(byteSize uint64) Builder {
	b.byteSize = byteSize
	return b
}

// WithInterleaving sets the size that the cache is interleaved.
func (b Builder) WithInterleaving(
	numBlock, unitCount, unitIndex int,
) Builder {
	b.interleaving = true
	b.numInterleavingBlock = numBlock
	b.interleavingUnitCount = unitCount
	b.interleavingUnitIndex = unitIndex
	return b
}

// WithWriteBufferSize sets the number of cach lines that can reside in the
// writebuffer.
func (b Builder) WithWriteBufferSize(n int) Builder {
	b.writeBufferCapacity = n
	return b
}

// WithMaxInflightFetch sets the number of concurrent fetch that the write-back
// cache can issue at the same time.
func (b Builder) WithMaxInflightFetch(n int) Builder {
	b.maxInflightFetch = n
	return b
}

// WithMaxInflightEviction sets the number of concurrent eviction that the
// write buffer can write to a low-level module.
func (b Builder) WithMaxInflightEviction(n int) Builder {
	b.maxInflightEviction = n
	return b
}

// WithDirectoryLatency sets the number of cycles required to access the
// directory.
func (b Builder) WithDirectoryLatency(n int) Builder {
	b.dirLatency = n
	return b
}

// WithBankLatency sets the number of cycles required to process each can
// read/write operation.
func (b Builder) WithBankLatency(n int) Builder {
	b.bankLatency = n
	return b
}

// WithRemoteReplicaFilter enables the per-slice Cuckoo Filter and the
// requester-side clean remote-replica path. It is disabled by default.
func (b Builder) WithRemoteReplicaFilter(enable bool) Builder {
	b.remoteReplicaFilter = enable
	return b
}

// WithResidentFilter enables a per-slice Cuckoo Filter that fast-rejects L2
// read and write misses without bypassing the L2 cache itself.
func (b Builder) WithResidentFilter(enable bool) Builder {
	b.residentFilter = enable
	return b
}

// WithFillForwarding enables best-effort early responses for read-only local
// DRAM fills. The data is still written into the ordinary L2 bank.
func (b Builder) WithFillForwarding(enable bool) Builder {
	b.fillForwarding = enable
	return b
}

// WithFilterCoupledPrefetch enables the demand-trained, one-line speculative
// path. It reuses the slice's Typed Cuckoo Filter and the ordinary L2/MSHR/fill
// path; it does not allocate a prefetch data buffer.
func (b Builder) WithFilterCoupledPrefetch(enable bool) Builder {
	b.filterPrefetch = enable
	return b
}

// WithPrefetchPredictorOnly records candidate coverage without issuing data
// requests. It is a diagnostic configuration, not a paper mechanism.
func (b Builder) WithPrefetchPredictorOnly(enable bool) Builder {
	b.prefetchPredictorOnly = enable
	if enable {
		b.filterPrefetch = true
	}
	return b
}

// WithUngatedPrefetch uses the same predictor and work-conserving structural
// admission but bypasses PATTERN/RESIDENT/PENDING membership gating. It is a
// diagnostic upper bound.
func (b Builder) WithUngatedPrefetch(enable bool) Builder {
	b.prefetchUngated = enable
	if enable {
		b.filterPrefetch = true
	}
	return b
}

// WithFilterCoupledPrefetchStreams sets the bounded direct-mapped predictor
// capacity. Values below one use the default.
func (b Builder) WithFilterCoupledPrefetchStreams(n int) Builder {
	b.prefetchStreams = n
	return b
}

// WithGranularityAdaptation enables demand-attached paired-read scheduling.
// The predicted sibling is a separate 64-B lower-memory request with its own
// request ID and response. It shares only a logical PairID with the real miss
// and fills an ordinary L2 block/MSHR; it is never a widened transaction.
func (b Builder) WithGranularityAdaptation(enable bool) Builder {
	b.granularityAdaptation = enable
	return b
}

// WithGranularityAdaptationWithoutFilter is a diagnostic mode that retains
// prediction and exact tag/MSHR checks but bypasses Cuckoo-Filter gating.
func (b Builder) WithGranularityAdaptationWithoutFilter(enable bool) Builder {
	b.granularityNoFilter = enable
	if enable {
		b.granularityAdaptation = true
	}
	return b
}

// WithAlwaysExpandGranularity is a diagnostic upper bound. Correctness,
// mapping, and resource checks still apply, but no learned pattern is needed.
func (b Builder) WithAlwaysExpandGranularity(enable bool) Builder {
	b.granularityAlways = enable
	if enable {
		b.granularityAdaptation = true
	}
	return b
}

// WithGranularityPredictorOnly trains the formal M1 predictor and reports its
// eligible direct-sibling candidates without allocating an MSHR, cache block,
// or lower-memory request.
func (b Builder) WithGranularityPredictorOnly(enable bool) Builder {
	b.granularityPredictor = enable
	if enable {
		b.granularityAdaptation = true
	}
	return b
}

// WithAdaptivePair enables the historical confidence/inflight/buffer M1
// policy and its single aligned 128-B controller-level read. The per-slice
// typed Cuckoo Filter suppresses expansions whose sibling is already resident
// or pending without changing the historical fallback policy.
func (b Builder) WithAdaptivePair(enable bool) Builder {
	b.adaptivePair = enable
	return b
}

// WithAdaptivePairRegionLines configures the aligned row-local fetch region.
func (b Builder) WithAdaptivePairRegionLines(lines int) Builder {
	if lines != 2 && lines != 4 && lines != 8 && lines != 16 {
		panic("adaptive-pair region lines must be one of 2, 4, 8, or 16")
	}
	b.adaptivePairRegionLines = lines
	return b
}

// WithTypedFilter allocates the one per-slice physical metadata filter even
// when only remote PENDING/SEEN users are enabled.
func (b Builder) WithTypedFilter(enable bool) Builder {
	b.typedFilter = enable
	return b
}

// WithTypedFilterConfig selects the diagnostic metadata implementation and
// modeled per-slice ports. Capacity zero keeps the cache-derived sizing.
func (b Builder) WithTypedFilterConfig(config TypedFilterConfig) Builder {
	b.typedFilterConfig = config
	return b
}

// Build creates a usable writeback cache.
func (b Builder) Build(name string) *Cache {
	cache := new(Cache)
	cache.TickingComponent = sim.NewTickingComponent(
		name, b.engine, b.freq, cache)

	b.configureCache(cache)
	b.createPorts(cache)
	b.createPortSenders(cache)
	b.createInternalStages(cache)
	b.createInternalBuffers(cache)

	return cache
}

func (b *Builder) configureCache(cacheModule *Cache) {
	blockSize := 1 << b.log2BlockSize
	vimctimFinder := cache.NewLRUVictimFinder()
	numSet := int(b.byteSize / uint64(b.wayAssociativity*blockSize))
	directory := cache.NewDirectory(
		numSet, b.wayAssociativity, blockSize, vimctimFinder)

	if b.interleaving {
		directory.AddrConverter = &mem.InterleavingConverter{
			InterleavingSize:    uint64(b.numInterleavingBlock) * (1 << b.log2BlockSize),
			TotalNumOfElements:  b.interleavingUnitCount,
			CurrentElementIndex: b.interleavingUnitIndex,
		}
	}

	mshr := cache.NewMSHR(b.numMSHREntry)
	storage := mem.NewStorage(b.byteSize)

	cacheModule.log2BlockSize = b.log2BlockSize
	cacheModule.numReqPerCycle = b.numReqPerCycle
	cacheModule.directory = directory
	cacheModule.mshr = mshr
	cacheModule.mshrCapacity = b.numMSHREntry
	cacheModule.storage = storage
	cacheModule.lowModuleFinder = b.lowModuleFinder
	cacheModule.state = cacheStateRunning
	cacheModule.evictingList = make(map[uint64]bool)
	cacheModule.fillForwarding = b.fillForwarding
	cacheModule.authoritativeAuditEnabled =
		b.typedFilterConfig.EnableAuthoritativeAudit
	cacheModule.interleaving = b.interleaving
	cacheModule.interleavingBlocks = b.numInterleavingBlock
	cacheModule.interleavingUnits = b.interleavingUnitCount
	cacheModule.interleavingIndex = b.interleavingUnitIndex
	if b.typedFilter || b.remoteReplicaFilter || b.residentFilter ||
		b.filterPrefetch || b.granularityAdaptation || b.adaptivePair {
		numBlocks := numSet * b.wayAssociativity
		// Capacity is derived from the slice: one slot per resident block plus
		// equal headroom for transient and reuse metadata. The critical reserve
		// covers all resident blocks and a worst-case RDMA outstanding skew;
		// SEEN uses only the remainder.
		config := b.typedFilterConfig
		if config.Capacity <= 0 {
			config.Capacity = numBlocks * 2
		}
		if config.CriticalReserve <= 0 {
			config.CriticalReserve = numBlocks + 64
		}
		if config.LookupWidth <= 0 {
			config.LookupWidth = b.numReqPerCycle
		}
		if config.UpdateWidth <= 0 {
			config.UpdateWidth = b.numReqPerCycle
		}
		config.Freq = b.freq
		cacheModule.requestFilter = NewTypedCuckooFilter(config)
		cacheModule.residentFilter = newTypedFilterLineView(
			cacheModule.requestFilter, FilterResident)
		cacheModule.residentFilterBlocks =
			make(map[*cache.Block]residentFilterKey, numBlocks)
		cacheModule.residentFilterReliable = true
		cacheModule.residentFilterEnabled = b.residentFilter
	}
	cacheModule.adaptivePairEnabled = b.adaptivePair
	cacheModule.adaptivePairStats.Enabled = b.adaptivePair
	if b.adaptivePair {
		cacheModule.adaptivePairAdapter = newAdaptivePairAdapter(
			16, b.adaptivePairRegionLines)
	}
	if b.filterPrefetch {
		cacheModule.filterPrefetchEnabled = true
		cacheModule.filterPrefetchPredictorOnly = b.prefetchPredictorOnly
		cacheModule.filterPrefetchUngated = b.prefetchUngated
		cacheModule.filterPrefetcher = NewDemandStridePredictor(
			b.prefetchStreams, uint64(1)<<b.log2BlockSize)
		cacheModule.filterPrefetcher.EnableCandidateOnPatternEstablishment()
		cacheModule.filterPrefetcher.EnableExponentialLateLookahead()
		cacheModule.localPrefetchPatternFilters =
			make(map[TypedFilterKey]*TypedCuckooFilter)
		cacheModule.localPrefetchByLine = make(map[localPrefetchLineKey]*localPrefetchRecord)
		cacheModule.localPrefetchByBlock = make(map[*cache.Block]*localPrefetchRecord)
		cacheModule.localPrefetchLeader = true
	}
	if b.granularityAdaptation {
		cacheModule.granularityAdaptationEnabled = true
		cacheModule.granularityWithoutFilter = b.granularityNoFilter
		cacheModule.granularityAlwaysExpand = b.granularityAlways
		cacheModule.granularityPredictorOnly = b.granularityPredictor
		cacheModule.granularityPredictor = NewPageLocalDemandStridePredictor(
			b.prefetchStreams, uint64(1)<<b.log2BlockSize, 4096)
		cacheModule.granularityPredictor.EnableCandidateOnPatternEstablishment()
		cacheModule.granularityPredictor.EnableFeedbackGatedIssue()
		cacheModule.granularityPatternFilters =
			make(map[TypedFilterKey]*TypedCuckooFilter)
		cacheModule.granularityByLine =
			make(map[granularityLineKey]*granularityRecord)
		cacheModule.granularityByBlock =
			make(map[*cache.Block]*granularityRecord)
		cacheModule.granularityLeader = true
	}
	if b.remoteReplicaFilter {
		cacheModule.remoteReplicaFilter = newTypedFilterLineView(
			cacheModule.requestFilter, FilterResident)
		cacheModule.remoteReplicaBlocks =
			make(map[*cache.Block]*remoteReplicaRecord)
	}
	if b.residentFilter {
		cacheModule.residentFilterEnabled = true
	}
}

func (b *Builder) createPorts(cache *Cache) {
	cache.topPort = sim.NewLimitNumMsgPort(cache,
		cache.numReqPerCycle*2, cache.Name()+".ToTop")
	cache.AddPort("Top", cache.topPort)

	cache.bottomPort = sim.NewLimitNumMsgPort(cache,
		cache.numReqPerCycle*2, cache.Name()+".BottomPort")
	cache.AddPort("Bottom", cache.bottomPort)

	cache.controlPort = sim.NewLimitNumMsgPort(cache,
		cache.numReqPerCycle*2, cache.Name()+".ControlPort")
	cache.AddPort("Control", cache.controlPort)

}

func (b *Builder) createPortSenders(cache *Cache) {
	cache.topSender = sim.NewBufferedSender(
		cache.topPort,
		sim.NewBuffer(cache.Name()+".TopSenderBuffer",
			cache.numReqPerCycle*4,
		),
	)
	cache.bottomSender = sim.NewBufferedSender(
		cache.bottomPort,
		sim.NewBuffer(
			cache.Name()+".BottomSenderBuffer",
			cache.numReqPerCycle*4,
		),
	)
	cache.controlPortSender = sim.NewBufferedSender(
		cache.controlPort, sim.NewBuffer(
			cache.Name()+".ControlSenderBuffer",
			cache.numReqPerCycle*4,
		),
	)
}

func (b *Builder) createInternalStages(cache *Cache) {
	cache.topParser = &topParser{cache: cache}
	b.buildDirectoryStage(cache)
	b.buildBankStages(cache)
	cache.mshrStage = &mshrStage{cache: cache}
	cache.flusher = &flusher{cache: cache}
	cache.writeBuffer = &writeBufferStage{
		cache:               cache,
		writeBufferCapacity: b.writeBufferCapacity,
		maxInflightFetch:    b.maxInflightFetch,
		maxInflightEviction: b.maxInflightEviction,
	}
}

func (b *Builder) buildDirectoryStage(cache *Cache) {
	buf := sim.NewBuffer(
		cache.Name()+".DirectoryStageBuffer",
		b.numReqPerCycle,
	)
	pipeline := pipelining.
		MakeBuilder().
		WithCyclePerStage(1).
		WithNumStage(b.dirLatency).
		WithPipelineWidth(b.numReqPerCycle).
		WithPostPipelineBuffer(buf).
		Build(cache.Name() + ".BankPipeline")
	cache.dirStage = &directoryStage{
		cache:    cache,
		pipeline: pipeline,
		buf:      buf,
	}
}

func (b *Builder) buildBankStages(cache *Cache) {
	cache.bankStages = make([]*bankStage, 1)

	laneWidth := b.numReqPerCycle
	if laneWidth == 1 {
		laneWidth = 2
	}

	buf := &bufferImpl{
		name:     fmt.Sprintf("%s.Bank.PostPipelineBuffer", cache.Name()),
		capacity: laneWidth,
	}
	pipeline := pipelining.
		MakeBuilder().
		WithCyclePerStage(1).
		WithNumStage(b.bankLatency).
		WithPipelineWidth(laneWidth).
		WithPostPipelineBuffer(buf).
		Build(fmt.Sprintf("%s.Bank.Pipeline", cache.Name()))
	cache.bankStages[0] = &bankStage{
		cache:           cache,
		bankID:          0,
		pipeline:        pipeline,
		postPipelineBuf: buf,
		pipelineWidth:   laneWidth,
	}
}

func (b *Builder) createInternalBuffers(cache *Cache) {
	cache.dirStageBuffer = sim.NewBuffer(
		cache.Name()+".DirStageBuffer",
		cache.numReqPerCycle,
	)
	cache.dirToBankBuffers = make([]sim.Buffer, 1)
	cache.dirToBankBuffers[0] = sim.NewBuffer(
		cache.Name()+".DirToBankBuffer",
		cache.numReqPerCycle,
	)
	cache.writeBufferToBankBuffers = make([]sim.Buffer, 1)
	cache.writeBufferToBankBuffers[0] = sim.NewBuffer(
		cache.Name()+".WriteBufferToBankBuffer",
		cache.numReqPerCycle,
	)
	cache.mshrStageBuffer = sim.NewBuffer(
		cache.Name()+".MSHRStageBuffer",
		cache.numReqPerCycle,
	)
	cache.writeBufferBuffer = sim.NewBuffer(
		cache.Name()+".WriteBufferBuffer",
		cache.numReqPerCycle,
	)
}
