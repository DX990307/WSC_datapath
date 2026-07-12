// Package runner defines how default benchmark samples are executed.
package runner

import (
	"fmt"
	"log"
	"net"
	"net/http"

	// Enable profiling
	_ "net/http/pprof"
	"strconv"
	"strings"
	"sync"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/monitoring"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
	"github.com/sarchlab/mgpusim/v3/benchmarks"
	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/emu"
	"github.com/sarchlab/mgpusim/v3/profiler"
	"github.com/sarchlab/mgpusim/v3/samples/sampledrunner"
	"github.com/sarchlab/mgpusim/v3/timing/cp"
	"github.com/sarchlab/mgpusim/v3/timing/rdma"
	"github.com/tebeka/atexit"
)

type verificationPreEnablingBenchmark interface {
	benchmarks.Benchmark

	EnableVerification()
}

// Runner is a class that helps running the benchmarks in the official samples.
type Runner struct {
	platform                *Platform
	maxInstStopper          *instTracer
	maxWGStopper            *wgTracer
	kernelTimeCounter       *tracing.BusyTimeTracer
	perGPUKernelTimeCounter []*tracing.BusyTimeTracer
	instCountTracers        []instCountTracer
	wgCountTracers          []wgCountTracer
	cacheLatencyTracers     []cacheLatencyTracer
	cacheHitRateTracers     []cacheHitRateTracer
	tlbLatencyTracers       []tlbLatencyTracer
	tlbHitRateTracers       []tlbHitRateTracer
	rdmaLatencyTracers      []rdmaLatencyTracer
	rdmaTransactionCounters []rdmaTransactionCountTracer
	l2TLBLatencyTracers     []l2TLBLatencyTracer
	l2TLBHitRateTracers     []l2TLBHitRateTracer
	mmuTransactionCounters  []mmuTransactionCountTracer
	mmuLatencyTracers       []mmuLatencyTracer
	gmmuTransactionCounters []gmmuTransactionCountTracer
	gmmuLatencyTracers      []gmmuLatencyTracer
	// gmmuCountTracers        []gmmuCountTracer
	// L2TLBTracers          []L2TLBTracer

	dramTracers         []dramTransactionCountTracer
	benchmarks          []benchmarks.Benchmark
	monitor             *monitoring.Monitor
	metricsCollector    *collector
	reportOnce          sync.Once
	simdBusyTimeTracers []simdBusyTimeTracer
	cuCPITraces         []cuCPIStackTracer
	sharingTraceWriter  *pageSharingTraceWriter
	observationDRAM     *windowedPhysicalDRAMObserver

	Timing                     bool
	Verify                     bool
	Parallel                   bool
	ReportInstCount            bool
	ReportCacheLatency         bool
	ReportCacheHitRate         bool
	ReportTLBHitRate           bool
	ReportRDMALatency          bool
	ReportRDMATransactionCount bool
	ReportGMMULatency          bool
	ReportMMULatency           bool
	ReportGMMUTransactionCount bool
	ReportMMUTransactionCount  bool

	ReportDRAMTransactionCount bool
	UseUnifiedMemory           bool
	ReportSIMDBusyTime         bool
	ReportCPIStack             bool
	ReportTLBLatency           bool
	ReportL2TLBLatency         bool
	ReportL2TLBHitRate         bool
	DisableServers             bool
	ReportL2Source             bool

	GPUIDs []int
}

func (r *Runner) startProfilingServer() {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		panic(err)
	}

	// fmt.Println("Profiling server running on:",
	// 	listener.Addr().(*net.TCPAddr).Port)

	if err := http.Serve(listener, nil); err != nil {
		panic(err)
	}
}

// Init initializes the platform simulate
func (r *Runner) Init() *Runner {
	r.ParseFlag()
	r.configureL2SourceStats()
	r.configureMemoryPathTrace()
	r.configureObservationTrace()

	if !r.DisableServers {
		go r.startProfilingServer()
	}

	log.SetFlags(log.Llongfile | log.Ldate | log.Ltime)

	sampledrunner.InitSampledEngine()
	sampledrunner.InitIPCSampledEngine()
	sampledrunner.InitKernelSampledEngine()
	profiler.InitInstCount()
	profiler.InitGlobalInstFeature()
	profiler.InitInstProfiler()
	profiler.InitBranchFeature()
	profiler.InitWfFeature()
	profiler.InitWallTime()
	profiler.LoadWGFeatureVec()

	if r.Timing {
		r.buildTimingPlatform()
	} else {
		r.buildEmuPlatform()
	}
	r.attachObservationDRAMObserver()

	sampledrunner.ClearGPUSampledEngines()
	for _, gpu := range r.platform.GPUs {
		cp, ok := gpu.CommandProcessor.(*cp.CommandProcessor)
		if !ok || cp == nil {
			continue
		}

		sampledrunner.InitGPUSampledEngines(
			gpu.GPUID,
			cp.Engine,
			cp.Freq,
			emu.StaticComputeUnitForGPU(gpu.GPUID),
		)
	}

	r.createUnifiedGPUs()

	r.defineMetrics()

	return r
}

func (r *Runner) configureL2SourceStats() {
	memtrace.DisableL2SourceStats()
	if !r.ReportL2Source {
		return
	}

	prefix := *l2SourceFileFlag
	if prefix == "" {
		prefix = *filenameFlag + "_l2_source"
	}
	memtrace.EnableL2SourceStats(prefix, *l2SourceTileWidthFlag)
}

func (r *Runner) configureMemoryPathTrace() {
	memtrace.DisableMemoryPathTrace()
	if !*memoryPathTracing {
		return
	}

	prefix := *memoryPathTraceFile
	if prefix == "" {
		prefix = *filenameFlag + "_memory_path"
	}
	var doneCallback func()
	if *memoryPathTraceExitOnComplete {
		doneCallback = func() {
			log.Printf(
				"memory-path trace reached max records; flushing metrics and exiting")
			// The callback runs outside the engine event handler. Pause waits
			// until the current event finishes, making component metrics safe to
			// iterate while the early-exit report is generated.
			r.platform.Engine.Pause()
			r.flushMetrics()
			atexit.Exit(0)
		}
	}
	if err := memtrace.EnableMemoryPathTrace(
		prefix,
		*memoryPathTraceWarmupAccesses,
		*memoryPathTraceMaxRecords,
		configuredLog2PageSize(),
		*l2SourceTileWidthFlag,
		*memoryPathTraceExitOnComplete,
		doneCallback,
	); err != nil {
		panic(err)
	}
}

func (r *Runner) configureObservationTrace() {
	memtrace.DisableObservationTrace()
	memtrace.DisableObservationRemoteTrace()
	if !*observationTracing {
		return
	}
	if !r.Timing {
		panic("-trace-observation requires -timing")
	}
	if *memoryPathTracing {
		panic("-trace-observation and legacy -trace-memory-path are mutually exclusive")
	}
	if *sampledrunner.SampledRunnerFlag ||
		*sampledrunner.IPCSampledRunnerFlag ||
		*sampledrunner.BranchSampledFlag ||
		*sampledrunner.KernelSampledFlag ||
		*sampledrunner.LoopSampledFlag {
		panic("-trace-observation requires full timing simulation; disable sampled execution")
	}
	if *observationTraceExitOnComplete && *observationTraceMaxRecords == 0 {
		panic("-trace-observation-exit-on-complete requires a nonzero path-record limit")
	}
	if *dramBatchEnableFlag || *dramRowReorderEnableFlag ||
		*remoteDataPathEnableFlag || *forceLocalDataAccessFlag ||
		*l1vBottomReorderPolicyFlag != "none" {
		panic("-trace-observation requires the unmodified baseline data path; disable local/remote mechanisms")
	}

	prefix := *observationTraceFile
	if prefix == "" {
		prefix = *filenameFlag + "_observation"
	}
	var doneCallback func()
	if *observationTraceExitOnComplete {
		doneCallback = func() {
			log.Printf(
				"observation trace reached max records; draining admitted remote requests")
			<-memtrace.FreezeObservationRemoteTrace()
			log.Printf(
				"observation remote tail drained; flushing metrics and exiting")
			r.platform.Engine.Pause()
			r.flushMetrics()
			atexit.Exit(0)
		}
	}
	if err := memtrace.EnableObservationTrace(
		prefix,
		*observationTraceWarmupAccesses,
		*observationTraceMaxRecords,
		*observationTraceExitOnComplete,
		doneCallback,
	); err != nil {
		panic(err)
	}
	if err := memtrace.EnableObservationRemoteTrace(
		memtrace.ObservationRemoteConfig{
			Prefix:         prefix,
			WarmupRequests: *observationRemoteWarmupRequests,
			MaxRequests:    *observationRemoteMaxRecords,
			L2SampleMax:    *observationL2SampleMax,
			TileWidth:      *l2SourceTileWidthFlag,
		},
	); err != nil {
		panic(err)
	}
}

func (r *Runner) buildEmuPlatform() {
	b := MakeEmuBuilder().
		WithNumGPU(r.GPUIDs[len(r.GPUIDs)-1]).
		WithLog2PageSize(configuredLog2PageSize())

	if r.Parallel {
		b = b.WithParallelEngine()
	}

	if *isaDebug {
		b = b.WithISADebugging()
	}

	if *visTracing {
		b = b.WithVisTracing()
	}

	if *memTracing {
		b = b.WithMemTracing()
	}

	if *magicMemoryCopy {
		b = b.WithMagicMemoryCopy()
	}

	r.platform = b.Build()
}

func (r *Runner) buildTimingPlatform() {
	reorderPolicy := *l1vBottomReorderPolicyFlag
	reorderWindow := *l1vBottomReorderWindowFlag
	reorderMaxAge := *l1vBottomReorderMaxAgeNSFlag
	if *remoteDataPathEnableFlag {
		// The remote datapath owns its one bounded FIFO batching point. Do not
		// stack the legacy per-L1 FIFO/HLQ in front of it.
		reorderPolicy = "none"
		reorderWindow = 0
		reorderMaxAge = 0
	}

	b := MakeR9NanoBuilder().
		WithLog2PageSize(configuredLog2PageSize()).
		WithBandwidth(*bandwidthFlag).
		WithSwitchLatency(*switchLatencyFlag).
		WithMaxNumHops(*maxNumHopsFlag).
		WithNetworkFlitSize(*networkFlitSizeFlag).
		WithEndpointChannels(*endpointChannelsFlag).
		WithEndpointBufferSize(*endpointBufferSizeFlag).
		WithRDMAPipeline(
			*rdmaPipelineWidthFlag,
			*rdmaPipelineLatencyFlag,
			*rdmaMaxOutstandingFlag,
		).
		WithL1VRemoteMaxInflight(*l1vRemoteMaxInflightFlag).
		WithL1VMSHREntries(*l1vMSHREntriesFlag).
		WithL1VMaxConcurrentTrans(*l1vMaxConcurrentTransFlag).
		WithL1VBottomReorder(
			reorderPolicy,
			reorderWindow,
			reorderMaxAge,
		).
		WithForceLocalDataAccess(*forceLocalDataAccessFlag).
		WithDRAMBatch(writeback.DRAMBatchConfig{
			Enabled:     *dramBatchEnableFlag,
			MaxEntries:  *dramBatchEntriesFlag,
			MaxLines:    *dramBatchLinesFlag,
			MaxWaitNS:   *dramBatchWaitNSFlag,
			WindowLines: 2,
		}).
		WithDRAMRowReorder(
			*dramRowReorderEnableFlag,
			*dramRowReorderMaxAgeFlag,
		).
		WithRemoteDataPath(rdma.RemoteDataPathConfig{
			Enabled:            *remoteDataPathEnableFlag,
			AUPrefetch:         *remoteDataPathPrefetchFlag,
			DisableDedup:       !*remoteDataPathDedupEnableFlag,
			DisableBatching:    !*remoteDataPathBatchingEnableFlag,
			DisableRequesterL2: !*remoteDataPathL2EnableFlag,
			MaxBatchLines:      *remoteDataPathBatchLinesFlag,
			MaxWaitNS:          *remoteDataPathWaitNSFlag,
			MaxBatches:         *remoteDataPathBatchesFlag,
			ReuseTableEntries:  *remoteDataPathReuseEntriesFlag,
		})

	if *sharingTracing {
		traceWriter, err := newPageSharingTraceWriter(
			*sharingTraceFile,
			*sharingTraceSampleEvery,
			*sharingTraceMaxRecords,
			b.log2PageSize,
			b.tileWidth,
			b.tileHeight,
		)
		if err != nil {
			panic(err)
		}
		r.sharingTraceWriter = traceWriter
		b = b.WithSharingTracer(traceWriter)
	}

	if r.Parallel {
		b = b.WithParallelEngine()
	}

	if *isaDebug {
		b = b.WithISADebugging()
	}

	if *visTracing {
		b = b.WithPartialVisTracing(
			sim.VTimeInSec(*visTraceStartTime),
			sim.VTimeInSec(*visTraceEndTime),
		)
	}

	if *memTracing {
		b = b.WithMemTracing()
	}

	r.monitor = monitoring.NewMonitor()
	b = b.WithMonitor(r.monitor)

	b = r.setAnalyszer(b)

	if *magicMemoryCopy {
		b = b.WithMagicMemoryCopy()
	}

	r.platform = b.Build(*numMemBankFlag)

	if !r.DisableServers {
		r.monitor.StartServer()
	}
}

func (*Runner) setAnalyszer(
	b R9NanoPlatformBuilder,
) R9NanoPlatformBuilder {
	if *analyszerPeriodFlag != 0 && *analyszerNameFlag == "" {
		panic("must specify -analyszer-name when using -analyszer-period")
	}

	if *analyszerNameFlag != "" {
		*analyszerNameFlag = fmt.Sprintf(*analyszerNameFlag)
		b = b.WithPerfAnalyzer(
			*analyszerNameFlag,
			*analyszerPeriodFlag,
		)
	}
	return b
}

func (r *Runner) createUnifiedGPUs() {
	// unifiedGPUID := r.platform.Driver.CreateUnifiedGPU(nil, []int{
	// 	18, 24, 25, 31})
	// 	1, 2, 3, 4, 5, 6, 7, 8,
	// 9, 10, 11, 12, 13, 14, 15, 16,
	// 17, 18, 19, 20, 21, 22, 23, 24,
	// })
	gpulist := make([]int, 48)
	for i := 0; i < 48; i++ {
		gpulist[i] = i + 1
	}
	unifiedGPUID := r.platform.Driver.CreateUnifiedGPU(nil, gpulist)

	r.GPUIDs = []int{unifiedGPUID}
}

func (r *Runner) gpuIDStringToList(gpuIDsString string) []int {
	gpuIDs := make([]int, 0)
	gpuIDTokens := strings.Split(gpuIDsString, ",")

	for _, t := range gpuIDTokens {
		gpuID, err := strconv.Atoi(t)
		if err != nil {
			panic(err)
		}
		gpuIDs = append(gpuIDs, gpuID)
	}

	return gpuIDs
}

// AddBenchmark adds an benchmark that the driver runs
func (r *Runner) AddBenchmark(b benchmarks.Benchmark) {
	b.SelectGPU(r.GPUIDs)
	if r.UseUnifiedMemory {
		b.SetUnifiedMemory()
	}
	r.benchmarks = append(r.benchmarks, b)
}

// AddBenchmarkWithoutSettingGPUsToUse allows for user specified GPUs for
// the benchmark to run.
func (r *Runner) AddBenchmarkWithoutSettingGPUsToUse(b benchmarks.Benchmark) {
	if r.UseUnifiedMemory {
		b.SetUnifiedMemory()
	}
	r.benchmarks = append(r.benchmarks, b)
}

// Run runs the benchmark on the simulator
func (r *Runner) Run() {
	r.platform.Driver.Run()

	var wg sync.WaitGroup
	for _, b := range r.benchmarks {
		wg.Add(1)
		go func(b benchmarks.Benchmark, wg *sync.WaitGroup) {
			if r.Verify {
				if b, ok := b.(verificationPreEnablingBenchmark); ok {
					b.EnableVerification()
				}
			}

			b.Run()

			if r.Verify {
				b.Verify()
			}
			wg.Done()
		}(b, &wg)
	}
	wg.Wait()

	r.platform.Driver.Terminate()
	r.platform.Engine.Finished()

	r.flushMetrics()

	atexit.Exit(0)
}

func (r *Runner) dumpMetrics() {
	r.metricsCollector.Dump(*filenameFlag)
}

func (r *Runner) flushMetrics() {
	r.reportOnce.Do(func() {
		r.reportStats()
		r.closeSharingTrace()
	})
}

// Driver returns the GPU driver used by the current runner.
func (r *Runner) Driver() *driver.Driver {
	return r.platform.Driver
}

// Engine returns the event-driven simulation engine used by the current runner.
func (r *Runner) Engine() sim.Engine {
	return r.platform.Engine
}
