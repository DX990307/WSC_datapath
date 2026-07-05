package runner

import "flag"

var timingFlag = flag.Bool("timing", false, "Run detailed timing simulation.")
var maxInstCount = flag.Uint64("max-inst", 0,
	"Terminate the simulation after the given number of instructions is retired.")
var maxWGCount = flag.Uint64("max-wg", 0,
	"Terminate the simulation after the given number of workgroups is retired.")
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
var memoryPathTraceRemoteOnly = flag.Bool("trace-memory-path-remote-only", false,
	"Only count and write remote L1V memory paths in memory-path trace outputs.")
var memoryPathTraceStream = flag.Bool("trace-memory-path-stream", false,
	"Stream memory-path raw/L1V path rows to disk and release completed request records.")
var memoryPathTraceTailWindow = flag.Bool("trace-memory-path-tail-window", false,
	"Keep the last max-records memory-path rows after warmup and dump them at the end.")
var memoryPathTraceExitOnComplete = flag.Bool("trace-memory-path-exit-on-complete", false,
	"Exit the benchmark process after the memory-path raw trace window reaches max records.")
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
var networkFlitSizeFlag = flag.Int("network-flit-size", 16,
	"NoC flit payload size in bytes. Larger values reduce response flit count.")
var l1vRemoteMaxInflightFlag = flag.Int("l1v-remote-max-inflight", 0,
	"Limit in-flight remote L1V bottom transactions per L1V cache; 0 disables remote-only throttling.")
var l1vMSHREntriesFlag = flag.Int("l1v-mshr-entries", 160,
	"Number of L1V cache MSHR entries per L1V cache.")
var l1vTLBMSHREntriesFlag = flag.Int("l1v-tlb-mshr-entries", 160,
	"Number of L1V TLB MSHR entries per L1V TLB.")
var l1vReqPerCycleFlag = flag.Int("l1v-req-per-cycle", 32,
	"L1V request-path width for ROB, address translator, TLB, and cache.")
var l1vMaxConcurrentTransFlag = flag.Int("l1v-max-concurrent-trans", 160,
	"Maximum concurrent L1V cache transactions per L1V cache.")
var forceLocalDataAccessFlag = flag.Bool("force-local-data-access", false,
	"Force L1V data-cache misses to use the requester's local L2/DRAM path. "+
		"Address translation and non-L1V memory traffic remain unchanged.")
var m1DirectDramBypassEnableFlag = flag.Bool("m1-direct-dram-bypass-enable", false,
	"Enable M1 direct local DRAM bypass with 128B access-unit coalescing.")
var m2RDMABatchEnableFlag = flag.Bool("m2-rdma-batch-enable", false,
	"Enable requester-side RDMA bitmap batching for remote 64B reads.")
var m2AUPrefetchEnableFlag = flag.Bool("m2-au-prefetch-enable", false,
	"Enable M2 same-128B-access-unit mate-line prefetch for remote 64B reads.")
var m2MaxBatchLinesFlag = flag.Int("m2-max-batch-lines", 8,
	"Maximum unique cache lines per M2 bitmap RDMA batch.")
var m2MaxWaitNSFlag = flag.Uint64("m2-max-wait-ns", 50,
	"Maximum M2 batch collection wait in ns before timeout flush.")
var m2BatchTableEntriesFlag = flag.Int("m2-batch-table-entries", 64,
	"Maximum active M2 requester-side batch table entries per RDMA engine.")
var m3OwnerFairEnableFlag = flag.Bool("m3-owner-fair-enable", false,
	"Enable owner-side per-requester fair RDMA service queue.")
var m3L1RemoteCacheEnableFlag = flag.Bool("m3-l1-remote-cache-enable", false,
	"Enable M3 L1V remote-only data area for remote demand and prefetch fills.")
var m3L1RemoteCacheEntriesFlag = flag.Int("m3-l1-remote-cache-entries", 128,
	"Number of 64B lines in each L1V remote-only data area.")
var m3FairQuantumLinesFlag = flag.Int("m3-fair-quantum-lines", 8,
	"M3 DRR quantum in cache lines.")
var m3MaxConsecutiveFlag = flag.Int("m3-max-consecutive-batches", 2,
	"M3 maximum consecutive packets served from one requester when others wait.")
var m3HardAgeLimitNSFlag = flag.Uint64("m3-hard-age-limit-ns", 500,
	"M3 hard age escape threshold in ns; 0 disables hard-age escape.")
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
var mmutlbLookupLatency = flag.Int("mmutlb-lookup-latency", 10,
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
