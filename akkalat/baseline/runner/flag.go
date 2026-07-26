package runner

import "flag"

var timingFlag = flag.Bool("timing", false, "Run detailed timing simulation.")
var maxInstCount = flag.Uint64("max-inst", 0,
	"Terminate the simulation after the given number of instructions is retired.")
var maxWGCount = flag.Uint64("max-wg", 0,
	"Terminate after this many workgroups complete across all GPUs.")
var parallelFlag = flag.Bool("parallel", false,
	"Run the simulation in parallel.")
var isaDebug = flag.Bool("debug-isa", false, "Generate the ISA debugging file.")
var visTracing = flag.Bool("trace-vis", false,
	"Generate trace for visualization purposes.")
var visTraceStartTime = flag.Float64("trace-vis-start", -1,
	"The starting time to collect visualization traces. A negative number "+
		"represents starting from the beginning.")
var visTraceEndTime = flag.Float64("trace-vis-end", -1,
	"The end time of collecting visualization traces. A negative number"+
		"means that the trace will be collected to the end of the simulation.")
var verifyFlag = flag.Bool("verify", false, "Verify the emulation result.")
var memTracing = flag.Bool("trace-mem", false, "Generate memory trace")
var sharingTracing = flag.Bool("trace-sharing", false,
	"Generate compact gzip-compressed page-sharing trace from L1 vector memory address translators.")
var sharingTraceFile = flag.String("trace-sharing-file", "sharing_trace.csv.gz",
	"Path of the gzip-compressed page-sharing trace.")
var sharingTraceSampleEvery = flag.Uint64("trace-sharing-sample", 1,
	"Record one translated data access every N accesses in the page-sharing trace.")
var sharingTraceMaxRecords = flag.Uint64("trace-sharing-max-records", 1000000,
	"Maximum page-sharing trace records to write; 0 means unlimited.")
var memoryPathTracing = flag.Bool("trace-memory-path", false,
	"Generate request-level memory path trace with TLB/cache joint-miss statistics.")
var memoryPathTraceFile = flag.String("trace-memory-path-file", "",
	"Output prefix for memory-path CSV files. Defaults to <metric-file-name>_memory_path.")
var memoryPathTraceWarmupAccesses = flag.Uint64("trace-memory-path-warmup-accesses", 100000,
	"Observed L1V memory accesses to skip before writing raw memory-path rows.")
var memoryPathTraceMaxRecords = flag.Uint64("trace-memory-path-max-records", 100000,
	"Maximum stable-window memory-path raw records to write; 0 means unlimited after warmup.")
var memoryPathTraceExitOnComplete = flag.Bool("trace-memory-path-exit-on-complete", false,
	"Exit the benchmark process after the memory-path raw trace window reaches max records.")
var observationTracing = flag.Bool("trace-observation", false,
	"Generate the new exclusive-stage observation trace (independent of the legacy memory-path tracer).")
var observationTraceFile = flag.String("trace-observation-file", "",
	"Output prefix for observation files. Defaults to <metric-file-name>_observation.")
var observationTraceWarmupAccesses = flag.Uint64("trace-observation-warmup-accesses", 100000,
	"Post-coalescing L1 demand-read transactions to skip before collecting the observation window.")
var observationTraceMaxRecords = flag.Uint64("trace-observation-max-records", 100000,
	"Maximum post-warmup demand-read observation paths; 0 means unlimited.")
var observationTraceExitOnComplete = flag.Bool("trace-observation-exit-on-complete", false,
	"Flush metrics and exit after the bounded observation window completes.")
var observationDRAMWarmupAccesses = flag.Uint64("trace-observation-dram-warmup-accesses", 100000,
	"Physical DRAM read subtransactions to skip before the O3 locality window.")
var observationDRAMMaxRecords = flag.Uint64("trace-observation-dram-max-records", 100000,
	"Maximum physical DRAM read subtransactions in the O3 window; 0 means unlimited.")
var observationRemoteWarmupRequests = flag.Uint64("trace-observation-remote-warmup-requests", 0,
	"Remote requests to skip before the O4/O5/O6 window.")
var observationRemoteMaxRecords = flag.Uint64("trace-observation-remote-max-records", 100000,
	"Maximum logical remote requests in the O4/O5/O6 window; 0 uses a safe finite default.")
var observationL2SampleMax = flag.Uint64("trace-observation-l2-sample-max", 100000,
	"Maximum periodic L2 utilization samples for O6; 0 uses a safe finite default.")
var remoteOriginTracing = flag.Bool("trace-remote-origin", false,
	"Generate a diagnostic WG/object local-vs-remote data-request trace.")
var remoteOriginTraceFile = flag.String("trace-remote-origin-file", "",
	"Output prefix for the remote-origin audit. Defaults to <metric-file-name>_remote_origin.")
var remoteOriginTraceMaxRecords = flag.Uint64("trace-remote-origin-max-records", 100000,
	"Maximum raw remote-origin rows; complete aggregates are always retained.")
var allocationProfile = flag.Bool("allocation-profile", false,
	"Write allocation metrics and exit immediately before the first kernel launch.")
var instCountReportFlag = flag.Bool("report-inst-count", false,
	"Report the number of instructions executed in each compute unit.")
var cacheLatencyReportFlag = flag.Bool("report-cache-latency", false,
	"Report the average cache latency.")
var cacheHitRateReportFlag = flag.Bool("report-cache-hit-rate", false,
	"Report the cache hit rate of each cache.")
var tlbHitRateReportFlag = flag.Bool("report-tlb-hit-rate", false,
	"Report the TLB hit rate of each TLB.")
var rdmaTransactionCountReportFlag = flag.Bool("report-rdma-transaction-count",
	false, "Report the number of transactions going through the RDMA engines.")
var dramTransactionCountReportFlag = flag.Bool("report-dram-transaction-count",
	false, "Report the number of transactions accessing the DRAMs.")
var reportCPIStackFlag = flag.Bool("report-cpi-stack", false,
	"Report the compute-unit CPI stack.")
var useUnifiedMemoryFlag = flag.Bool("use-unified-memory", false,
	"Run benchmark with Unified Memory or not")
var reportAll = flag.Bool("report-all", false, "Report all metrics to .csv file.")
var filenameFlag = flag.String("metric-file-name", "metrics",
	"Modify the name of the output csv file.")
var magicMemoryCopy = flag.Bool("magic-memory-copy", false,
	"Copy data from CPU directly to global memory")
var switchLatencyFlag = flag.Int("switch-latency", 20,
	"The latency of the switch")
var bandwidthFlag = flag.Int("bandwidth", 1,
	"The bandwidth of the network as a multiple of 16GB/s.")
var endpointChannelsFlag = flag.Int("endpoint-channels", 0,
	"Override local device endpoint input/output flit channels per cycle; 0 uses network bandwidth.")
var endpointBufferSizeFlag = flag.Int("endpoint-buffer-size", 0,
	"Override local device endpoint buffer capacity; 0 uses endpoint channel count.")
var rdmaPipelineWidthFlag = flag.Int("rdma-pipeline-width", 8,
	"Maximum requests or responses processed by each RDMA input path per cycle.")
var rdmaPipelineLatencyFlag = flag.Int("rdma-pipeline-latency", 10,
	"Fixed RDMA processing latency in cycles paid on each endpoint traversal.")
var rdmaMaxOutstandingFlag = flag.Int("rdma-max-outstanding", 64,
	"Maximum distinct operations tracked independently by each RDMA requester and owner direction.")
var networkFlitSizeFlag = flag.Int("network-flit-size", 16,
	"NoC flit payload size in bytes. Larger values reduce response flit count.")
var l1vRemoteMaxInflightFlag = flag.Int("l1v-remote-max-inflight", 0,
	"Limit in-flight remote L1V bottom transactions per L1V cache; 0 disables remote-only throttling.")
var l1vMSHREntriesFlag = flag.Int("l1v-mshr-entries", 16,
	"Number of L1V cache MSHR entries per L1V cache.")
var l1vMaxConcurrentTransFlag = flag.Int("l1v-max-concurrent-trans", 16,
	"Maximum concurrent L1V cache transactions per L1V cache.")
var l2CacheSizeMBFlag = flag.Int("l2-cache-size-mb", 4,
	"Total L2 cache capacity per GPM in MiB.")
var forceLocalDataAccessFlag = flag.Bool("force-local-data-access", false,
	"Force L1V data-cache misses to use the requester's local L2/DRAM path. "+
		"Address translation and non-L1V memory traffic remain unchanged.")
var l2ResidentFilterEnableFlag = flag.Bool("l2-resident-filter-enable", false,
	"Enable the per-L2-slice resident Cuckoo Filter fast-miss path.")
var l2FilterPrefetchEnableFlag = flag.Bool("l2-filter-prefetch-enable", false,
	"Enable real-demand-trained, Filter-coupled 64B prefetching with at most one candidate per demand.")
var l2PrefetchPredictorOnlyFlag = flag.Bool("l2-prefetch-predictor-only", false,
	"Diagnostic: train the predictor and count candidates without issuing them.")
var l2PrefetchUngatedFlag = flag.Bool("l2-prefetch-ungated", false,
	"Diagnostic: issue predicted candidates without membership gating.")
var l2GranularityAdaptationEnableFlag = flag.Bool(
	"l2-granularity-adaptation-enable", false,
	"Enable demand-attached, filter-guided paired-read aggregation.")
var l2AdaptivePairEnableFlag = flag.Bool(
	"l2-adaptive-pair-enable", false,
	"Enable the historical adjacent-line M1 policy using one aligned 128B DRAM read.")
var l2AdaptivePairRegionLinesFlag = flag.Int(
	"l2-adaptive-pair-region-lines", 2,
	"Aligned row-local region fetched by adaptive pairing, in 64B cachelines; valid values are 2, 4, 8, and 16.")
var l2GranularityWithoutFilterFlag = flag.Bool(
	"l2-granularity-without-filter", false,
	"Diagnostic: adapt controller granularity using prediction and exact checks without Cuckoo gating.")
var l2GranularityAlwaysExpandFlag = flag.Bool(
	"l2-granularity-always-expand", false,
	"Diagnostic: expand every resource-safe demand region without requiring a learned pattern.")
var l2GranularityPredictorOnlyFlag = flag.Bool(
	"l2-granularity-predictor-only", false,
	"Diagnostic: train the paired-read predictor and count eligible sibling candidates without issuing them.")
var prefetchPredictorEntriesFlag = flag.Int("prefetch-predictor-entries", 256,
	"Bounded demand-stride predictor entries per GPU/requester.")
var l2FillForwardingEnableFlag = flag.Bool("l2-fill-forwarding-enable", false,
	"Enable best-effort read-only L2 fill-and-forward on local DRAM returns.")
var typedFilterModeFlag = flag.String("typed-filter-mode", "cuckoo",
	"Per-L2-slice metadata mode: disabled, cuckoo, or exact.")
var typedFilterCapacityFlag = flag.Int("typed-filter-capacity", 0,
	"Typed-filter slots per L2 slice; 0 derives capacity from the slice.")
var typedFilterSlotsPerBucketFlag = flag.Int("typed-filter-slots-per-bucket", 4,
	"Typed-filter entries per bucket; sensitivity range 1 through 8.")
var typedFilterFingerprintBitsFlag = flag.Int("typed-filter-fingerprint-bits", 13,
	"Typed-filter fingerprint width in bits; sensitivity range 4 through 16.")
var typedFilterLookupLatencyFlag = flag.Int("typed-filter-lookup-latency", 1,
	"Typed-filter lookup latency in L2 cycles.")
var typedFilterLookupWidthFlag = flag.Int("typed-filter-lookup-width", 16,
	"Typed-filter lookups accepted by each L2 slice per cycle.")
var typedFilterUpdateLatencyFlag = flag.Int("typed-filter-update-latency", 1,
	"Typed-filter update latency in L2 cycles.")
var typedFilterUpdateWidthFlag = flag.Int("typed-filter-update-width", 16,
	"Typed-filter updates accepted by each L2 slice per cycle.")
var dramRowContinuationEnableFlag = flag.Bool("dram-row-continuation-enable", false,
	"Enable work-conserving same-bank, same-row DRAM continuation.")
var remoteDataPathEnableFlag = flag.Bool("remote-data-path-enable", false,
	"Enable requester RDMA exact dedup/batching and requester-L2 remote replicas.")
var remoteDataPathDedupEnableFlag = flag.Bool("remote-data-path-dedup-enable", true,
	"Enable requester RDMA exact same-line deduplication when the remote data path is enabled.")
var remoteDataPathBatchingEnableFlag = flag.Bool("remote-data-path-batching-enable", true,
	"Enable requester RDMA FIFO/bitmap batching when the remote data path is enabled.")
var remoteFilterPrefetchEnableFlag = flag.Bool("remote-filter-prefetch-enable", false,
	"Allow Filter-approved candidates to piggyback existing remote batches.")
var remoteDataPathL2EnableFlag = flag.Bool("remote-data-path-l2-enable", true,
	"Enable requester-L2 Cuckoo probes and remote clean replicas when the remote data path is enabled.")
var typedFilterAuthoritativeAuditFlag = flag.Bool(
	"typed-filter-authoritative-audit", false,
	"Enable timing-neutral exact-state checks for typed-Filter negative decisions.")
var remoteDataPathBatchLinesFlag = flag.Int("remote-data-path-batch-lines", 8,
	"Maximum unique 64B lines in one remote bitmap request.")
var remoteDataPathBatchesFlag = flag.Int("remote-data-path-batches", 64,
	"Maximum collecting page batches per requester RDMA.")
var maxNumHopsFlag = flag.Int("max-num-hops", -1,
	"The maximum number of hops in the network")
var numMemBankFlag = flag.Int("num-memory-banks", 16,
	"The maximum number of hops in the network")
var analyszerNameFlag = flag.String("analyzer-Name", "",
	"The name of the analyzer to use.")
var analyszerPeriodFlag = flag.Float64("analyzer-period", 0.0,
	"The period to dump the analyzer results.")
var visTracerDB = flag.String("trace-vis-db", "sqlite",
	"The database to store the visualization trace. Possible values are "+
		"sqlite, mysql, and csv.")
var visTracerDBFileName = flag.String("trace-vis-db-file", "",
	"The file name of the database to store the visualization trace. "+
		"Extension names are not required. "+
		"If not specified, a random file name will be used. "+
		"This flag does not work with Mysql db. When MySQL is used, "+
		"the database name is always randomly generated.")
var mmuWalkCoalescing = flag.Bool("mmu-walk-coalescing", false,
	"Enable MMU page-walk coalescing.")
var mmutlbLookupLatency = flag.Int("mmutlb-lookup-latency", 80,
	"Fixed MMUTLB/IOTLB lookup latency, in cycles, applied before each buffered translation request is looked up.")
var disableServersFlag = flag.Bool("disable-servers", false,
	"Disable profiling and monitoring servers. Useful for automated tests.")
var log2PageSizeFlag = flag.Uint64("log2-page-size", 12,
	"GPU page size as log2(bytes). For example 12=4KB, 14=16KB, 15=32KB, 21=2MB.")
var l2SourceReportFlag = flag.Bool("report-l2-source", false,
	"Report aggregate L2 source data: local DRAM fills and remote GPM requester/provider traffic.")
var l2SourceFileFlag = flag.String("l2-source-file", "",
	"Output prefix for L2 source CSV files. Defaults to <metric-file-name>_l2_source.")
var l2SourceTileWidthFlag = flag.Int("l2-source-tile-width", 7,
	"Tile-array width used to compute Manhattan hops for L2 source reports.")

func configuredLog2PageSize() uint64 {
	return *log2PageSizeFlag
}

// ParseFlag applies the runner flag to runner object
//
//nolint:gocyclo
func (r *Runner) ParseFlag() *Runner {
	if *parallelFlag {
		r.Parallel = true
	}

	if *verifyFlag {
		r.Verify = true
	}

	if *timingFlag {
		r.Timing = true
	}

	if *useUnifiedMemoryFlag {
		r.UseUnifiedMemory = true
	}

	if *instCountReportFlag {
		r.ReportInstCount = true
	}

	if *cacheLatencyReportFlag {
		r.ReportCacheLatency = true
	}

	if *cacheHitRateReportFlag {
		r.ReportCacheHitRate = true
	}

	if *tlbHitRateReportFlag {
		r.ReportTLBHitRate = true
	}

	if *dramTransactionCountReportFlag {
		r.ReportDRAMTransactionCount = true
	}

	if *rdmaTransactionCountReportFlag {
		r.ReportRDMATransactionCount = true
	}

	if *reportCPIStackFlag {
		r.ReportCPIStack = true
	}

	if *reportAll {
		r.ReportInstCount = true
		r.ReportCacheLatency = true
		r.ReportCacheHitRate = true
		r.ReportTLBHitRate = true
		r.ReportDRAMTransactionCount = true
		r.ReportRDMATransactionCount = true
		r.ReportRDMALatency = true
		r.ReportTLBLatency = true
		r.ReportGMMULatency = true
		r.ReportMMULatency = true
		r.ReportGMMUTransactionCount = true
		r.ReportMMUTransactionCount = true
		r.ReportSIMDBusyTime = true
		r.ReportCPIStack = true
		r.ReportL2TLBHitRate = true
		r.ReportL2TLBLatency = true
	}

	if *disableServersFlag {
		r.DisableServers = true
	}

	if *l2SourceReportFlag {
		r.ReportL2Source = true
	}

	return r
}
