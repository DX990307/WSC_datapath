package runner

import (
	"fmt"
	"log"
	"os"

	memtraces "github.com/sarchlab/akita/v3/mem/trace"

	"github.com/sarchlab/akita/v3/analysis"
	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/mem/vm/addresstranslator"
	"github.com/sarchlab/akita/v3/mem/vm/mmu"
	"github.com/sarchlab/akita/v3/mem/vm/mmuCache"
	"github.com/sarchlab/akita/v3/mem/vm/mmuTLB"
	"github.com/sarchlab/akita/v3/monitoring"
	mesh "github.com/sarchlab/akita/v3/noc/networking/mesh"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/emu"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/timing/cp"
	"github.com/sarchlab/mgpusim/v3/timing/rdma"
)

// R9NanoPlatformBuilder can build a platform that equips R9Nano GPU.
type R9NanoPlatformBuilder struct {
	useParallelEngine        bool
	debugISA                 bool
	traceVis                 bool
	visTraceStartTime        sim.VTimeInSec
	visTraceEndTime          sim.VTimeInSec
	traceMem                 bool
	tileWidth, tileHeight    int
	numSAPerGPU              int
	numCUPerSA               int
	useMagicMemoryCopy       bool
	log2PageSize             uint64
	bandwidth                int
	switchLatency            int
	maxNumHops               int
	networkFlitSize          int
	endpointChannels         int
	endpointBufferSize       int
	l1vRemoteMaxInflight     int
	l1vMSHREntries           int
	l1vMaxConcurrentTrans    int
	forceLocalDataAccess     bool
	l2ResidentFilter         bool
	l2FilterPrefetch         bool
	l2PrefetchPredictorOnly  bool
	l2PrefetchUngated        bool
	prefetchPredictorEntries int
	l2GranularityAdaptation  bool
	l2GranularityNoFilter    bool
	l2GranularityAlways      bool
	l2GranularityPredictor   bool
	l2AdaptivePair           bool
	l2FillForwarding         bool
	typedFilterConfig        writeback.TypedFilterConfig
	dramRowContinuation      bool
	remoteDataPath           rdma.RemoteDataPathConfig
	rdmaPipelineWidth        int
	rdmaPipelineLatency      int
	rdmaMaxOutstanding       int

	engine       sim.Engine
	visTracer    tracing.Tracer
	monitor      *monitoring.Monitor
	IOMMUTLB     *mmuTLB.TLB
	IOMMUCache   *mmuCache.MMUCache
	mmu          *mmu.MMU
	mmuTopModule sim.Port

	globalStorage *mem.Storage

	perfAnalysisFileName string
	perfAnalyzingPeriod  float64
	perfAnalyzer         *analysis.PerfAnalyzer
	sharingTracer        addresstranslator.SharingTracer

	gpus []*GPU
}

// MakeR9NanoBuilder creates a EmuBuilder with default parameters.
func MakeR9NanoBuilder() R9NanoPlatformBuilder {
	b := R9NanoPlatformBuilder{
		tileWidth:                7,
		tileHeight:               7,
		log2PageSize:             12,
		visTraceStartTime:        -1,
		visTraceEndTime:          -1,
		switchLatency:            20,
		networkFlitSize:          16,
		numSAPerGPU:              8,
		numCUPerSA:               4,
		maxNumHops:               -1,
		l1vMSHREntries:           16,
		l1vMaxConcurrentTrans:    16,
		prefetchPredictorEntries: 64,
		typedFilterConfig: writeback.TypedFilterConfig{
			Mode:                writeback.TypedFilterCuckoo,
			LookupLatencyCycles: 1,
			UpdateLatencyCycles: 1,
		},
		rdmaPipelineWidth:   8,
		rdmaPipelineLatency: 10,
		rdmaMaxOutstanding:  64,
		remoteDataPath: rdma.RemoteDataPathConfig{
			MaxBatchLines: 8,
			MaxBatches:    64,
		},
	}
	return b
}

// WithParallelEngine lets the EmuBuilder to use parallel engine.
func (b R9NanoPlatformBuilder) WithParallelEngine() R9NanoPlatformBuilder {
	b.useParallelEngine = true
	return b
}

// WithISADebugging enables ISA debugging in the simulation.
func (b R9NanoPlatformBuilder) WithISADebugging() R9NanoPlatformBuilder {
	b.debugISA = true
	return b
}

// WithVisTracing lets the platform to record traces for visualization purposes.
func (b R9NanoPlatformBuilder) WithVisTracing() R9NanoPlatformBuilder {
	b.traceVis = true
	return b
}

// WithPartialVisTracing lets the platform to record traces for visualization
// purposes. The trace will only be collected from the start time to the end
// time.
func (b R9NanoPlatformBuilder) WithPartialVisTracing(
	start, end sim.VTimeInSec,
) R9NanoPlatformBuilder {
	b.traceVis = true
	b.visTraceStartTime = start
	b.visTraceEndTime = end

	return b
}

// WithMemTracing lets the platform to trace memory operations.
func (b R9NanoPlatformBuilder) WithMemTracing() R9NanoPlatformBuilder {
	b.traceMem = true
	return b
}

// WithSharingTracer records compact page-sharing observations.
func (b R9NanoPlatformBuilder) WithSharingTracer(
	t addresstranslator.SharingTracer,
) R9NanoPlatformBuilder {
	b.sharingTracer = t
	return b
}

// WithLog2PageSize sets the page size as a power of 2.
func (b R9NanoPlatformBuilder) WithLog2PageSize(
	n uint64,
) R9NanoPlatformBuilder {
	b.log2PageSize = n
	return b
}

// WithMonitor sets the monitor that is used to monitor the simulation
func (b R9NanoPlatformBuilder) WithMonitor(
	m *monitoring.Monitor,
) R9NanoPlatformBuilder {
	b.monitor = m
	return b
}

func (b R9NanoPlatformBuilder) WithPerfAnalyzer(
	Name string,
	Period float64,
) R9NanoPlatformBuilder {
	b.perfAnalysisFileName = Name
	b.perfAnalyzingPeriod = Period
	return b
}

// WithMagicMemoryCopy uses global storage as memory components
func (b R9NanoPlatformBuilder) WithMagicMemoryCopy() R9NanoPlatformBuilder {
	b.useMagicMemoryCopy = true
	return b
}

// WithBandwidth sets the bandwidth between adjacent GPUs in the unit of 16GB/s.
func (b R9NanoPlatformBuilder) WithBandwidth(
	bandwidth int,
) R9NanoPlatformBuilder {
	b.bandwidth = bandwidth
	return b
}

// WithSwitchLatency sets the switch latency.
func (b R9NanoPlatformBuilder) WithSwitchLatency(
	latency int,
) R9NanoPlatformBuilder {
	b.switchLatency = latency
	return b
}

// WithMaxNumHops sets the maximum number of hops that a flit can travel in the
// mesh network.
func (b R9NanoPlatformBuilder) WithMaxNumHops(
	n int,
) R9NanoPlatformBuilder {
	b.maxNumHops = n
	return b
}

// WithNetworkFlitSize sets the NoC flit payload size in bytes.
func (b R9NanoPlatformBuilder) WithNetworkFlitSize(
	n int,
) R9NanoPlatformBuilder {
	if n > 0 {
		b.networkFlitSize = n
	}
	return b
}

// WithEndpointChannels overrides local device endpoint input/output channels.
func (b R9NanoPlatformBuilder) WithEndpointChannels(
	n int,
) R9NanoPlatformBuilder {
	b.endpointChannels = n
	return b
}

// WithEndpointBufferSize overrides local device endpoint buffer capacity.
func (b R9NanoPlatformBuilder) WithEndpointBufferSize(
	n int,
) R9NanoPlatformBuilder {
	b.endpointBufferSize = n
	return b
}

// WithL1VRemoteMaxInflight limits in-flight remote L1V cache-line misses per
// L1V cache. A non-positive value disables the remote-only throttle.
func (b R9NanoPlatformBuilder) WithL1VRemoteMaxInflight(
	n int,
) R9NanoPlatformBuilder {
	b.l1vRemoteMaxInflight = n
	return b
}

// WithL1VMSHREntries sets the number of L1V cache MSHR entries.
func (b R9NanoPlatformBuilder) WithL1VMSHREntries(
	n int,
) R9NanoPlatformBuilder {
	if n > 0 {
		b.l1vMSHREntries = n
	}
	return b
}

// WithL1VMaxConcurrentTrans sets the L1V cache concurrency window.
func (b R9NanoPlatformBuilder) WithL1VMaxConcurrentTrans(
	n int,
) R9NanoPlatformBuilder {
	if n > 0 {
		b.l1vMaxConcurrentTrans = n
	}
	return b
}

// WithForceLocalDataAccess routes L1V data-cache misses to local L2/DRAM.
func (b R9NanoPlatformBuilder) WithForceLocalDataAccess(
	enable bool,
) R9NanoPlatformBuilder {
	b.forceLocalDataAccess = enable
	return b
}

// WithL2ResidentFilter enables local L2 Cuckoo-filter fast misses.
func (b R9NanoPlatformBuilder) WithL2ResidentFilter(
	enable bool,
) R9NanoPlatformBuilder {
	b.l2ResidentFilter = enable
	return b
}

// WithL2FilterPrefetch enables the Filter-coupled candidate path.
func (b R9NanoPlatformBuilder) WithL2FilterPrefetch(
	enable bool,
) R9NanoPlatformBuilder {
	b.l2FilterPrefetch = enable
	return b
}

// WithL2PrefetchDiagnostics configures non-paper predictor diagnostics.
func (b R9NanoPlatformBuilder) WithL2PrefetchDiagnostics(
	predictorOnly, ungated bool,
) R9NanoPlatformBuilder {
	b.l2PrefetchPredictorOnly = predictorOnly
	b.l2PrefetchUngated = ungated
	return b
}

// WithL2GranularityAdaptation configures formal M1 and its two causal
// diagnostics. The predictor and Cuckoo Filter remain per-GPU/per-slice as
// configured by the GPU builder.
func (b R9NanoPlatformBuilder) WithL2GranularityAdaptation(
	enable, withoutFilter, alwaysExpand, predictorOnly bool,
) R9NanoPlatformBuilder {
	b.l2GranularityAdaptation = enable
	b.l2GranularityNoFilter = withoutFilter
	b.l2GranularityAlways = alwaysExpand
	b.l2GranularityPredictor = predictorOnly
	return b
}

// WithL2AdaptivePair enables the restored historical M1 adapter on every GPM.
func (b R9NanoPlatformBuilder) WithL2AdaptivePair(
	enable bool,
) R9NanoPlatformBuilder {
	b.l2AdaptivePair = enable
	return b
}

// WithPrefetchPredictorEntries sets the common bounded predictor capacity for
// local and remote instances of the same candidate-generation design.
func (b R9NanoPlatformBuilder) WithPrefetchPredictorEntries(
	entries int,
) R9NanoPlatformBuilder {
	if entries < 1 {
		panic("prefetch predictor entries must be positive")
	}
	b.prefetchPredictorEntries = entries
	return b
}

// WithL2FillForwarding enables best-effort read-only local fill forwarding.
func (b R9NanoPlatformBuilder) WithL2FillForwarding(
	enable bool,
) R9NanoPlatformBuilder {
	b.l2FillForwarding = enable
	return b
}

// WithTypedFilterConfig sets the metadata implementation and ports for every
// L2 slice.
func (b R9NanoPlatformBuilder) WithTypedFilterConfig(
	config writeback.TypedFilterConfig,
) R9NanoPlatformBuilder {
	b.typedFilterConfig = config
	return b
}

// WithDRAMRowContinuation configures work-conserving same-row continuation.
func (b R9NanoPlatformBuilder) WithDRAMRowContinuation(
	enabled bool,
) R9NanoPlatformBuilder {
	b.dramRowContinuation = enabled
	return b
}

// WithRemoteDataPath configures the remote RDMA/L2 datapath mechanism.
func (b R9NanoPlatformBuilder) WithRemoteDataPath(
	config rdma.RemoteDataPathConfig,
) R9NanoPlatformBuilder {
	b.remoteDataPath = config
	return b
}

// WithRDMAPipeline configures the RDMA width, fixed per-traversal latency, and
// maximum number of distinct outstanding operations.
func (b R9NanoPlatformBuilder) WithRDMAPipeline(
	width, latency, maxOutstanding int,
) R9NanoPlatformBuilder {
	b.rdmaPipelineWidth = width
	b.rdmaPipelineLatency = latency
	b.rdmaMaxOutstanding = maxOutstanding
	return b
}

// Build builds a platform with R9Nano GPUs.
func (b R9NanoPlatformBuilder) Build(numMemoryBank int) *Platform {
	b.engine = b.createEngine()
	if b.monitor != nil {
		b.monitor.RegisterEngine(b.engine)
	}

	b.setupVisTracing()
	b.setupPerfermanceTracing()

	numGPU := b.tileWidth*b.tileHeight - 1
	b.globalStorage = mem.NewStorage(uint64(1+numGPU) * 8 * mem.GB)

	rdmaAddressTable := b.createRDMAAddrTable()
	pmcAddressTable := b.createPMCPageTable()
	l2TLBTable := b.createL2TLBTable()

	mmuComponent, pageTable := b.createMMU(b.engine, l2TLBTable)
	b.mmu = mmuComponent

	b.createIOMMUCache()
	b.createIOMMUTLB(l2TLBTable, pageTable)

	b.connIOMMUCacheWithIOMMUTLB()
	b.mmu.TopModule = b.IOMMUCache.GetPortByName("Bottom")
	b.IOMMUCache.UpModule = b.IOMMUTLB.GetPortByName("Bottom")

	b.connIOMMUWithIOMMUCache()

	gpuDriverBuilder := driver.MakeBuilder()
	if b.useMagicMemoryCopy {
		gpuDriverBuilder = gpuDriverBuilder.WithMagicMemoryCopyMiddleware()
	}
	gpuDriver := gpuDriverBuilder.
		WithEngine(b.engine).
		WithPageTable(pageTable).
		WithLog2PageSize(b.log2PageSize).
		WithGlobalStorage(b.globalStorage).
		WithMemorySize(8 * mem.GB).
		Build("Driver")

	if b.monitor != nil {
		b.monitor.RegisterComponent(gpuDriver)
	}

	connector := b.createConnection(b.engine, gpuDriver)

	gpuBuilder := b.createGPUBuilder(b.engine, gpuDriver, mmuComponent, numMemoryBank, pageTable)

	mmuComponent.MigrationServiceProvider = gpuDriver.GetPortByName("MMU")

	b.createGPUs(
		connector,
		gpuBuilder, gpuDriver,
		rdmaAddressTable,
		pmcAddressTable,
		l2TLBTable)

	connector.EstablishNetwork()

	for _, gpu := range b.gpus {
		gpu.MMUEngine = mmuComponent
	}

	disassembler := insts.NewDisassembler()
	emu.CreateUniqSampledComputeUnit(
		"cu", gpuBuilder.freq, disassembler,
		pageTable, b.log2PageSize, b.globalStorage, nil)
	emu.CreateUniqBBVComputeUnit(
		"cu", gpuBuilder.freq, disassembler,
		pageTable, b.log2PageSize, b.globalStorage, nil)
	emu.CreateUniqStaticComputeUnit(
		"cu", gpuBuilder.freq, disassembler,
		pageTable, b.log2PageSize, b.globalStorage, nil)
	for _, gpu := range b.gpus {
		name := fmt.Sprintf("GPU%dCU", gpu.GPUID)
		emu.CreateSampledComputeUnitForGPU(
			gpu.GPUID,
			name,
			gpuBuilder.freq,
			disassembler,
			pageTable,
			b.log2PageSize,
			b.globalStorage,
			nil)
		emu.CreateStaticComputeUnitForGPU(
			gpu.GPUID,
			name,
			gpuBuilder.freq,
			disassembler,
			pageTable,
			b.log2PageSize,
			b.globalStorage,
			nil)
	}

	return &Platform{
		Engine:   b.engine,
		Driver:   gpuDriver,
		GPUs:     b.gpus,
		IOMMUTLB: b.IOMMUTLB,
	}
}

func (b *R9NanoPlatformBuilder) setupVisTracing() {
	if !b.traceVis {
		return
	}

	var backend tracing.TracerBackend
	switch *visTracerDB {
	case "sqlite":
		be := tracing.NewSQLiteTraceWriter(*visTracerDBFileName)
		be.Init()
		backend = be
	case "csv":
		be := tracing.NewCSVTraceWriter(*visTracerDBFileName)
		be.Init()
		backend = be
	case "mysql":
		be := tracing.NewMySQLTraceWriter()
		be.Init()
		backend = be
	default:
		panic(fmt.Sprintf(
			"Tracer database type must be [sqlite|csv|mysql]. "+
				"Provided value %s is not supported.",
			*visTracerDB))
	}

	visTracer := tracing.NewDBTracer(b.engine, backend)
	visTracer.SetTimeRange(b.visTraceStartTime, b.visTraceEndTime)

	b.visTracer = visTracer
}

func (b *R9NanoPlatformBuilder) createGPUs(
	connector *mesh.Connector,
	gpuBuilder R9NanoGPUBuilder,
	gpuDriver *driver.Driver,
	rdmaAddressTable *mem.BankedLowModuleFinder,
	pmcAddressTable *mem.BankedLowModuleFinder,
	l2TLBTable *mem.MultiPageFinder,
) {
	for y := 0; y < b.tileHeight; y++ {
		for x := 0; x < b.tileWidth; x++ {
			if x == b.tileWidth/2 && y == b.tileHeight/2 {
				continue
			}

			b.createGPU(x, y, gpuBuilder,
				gpuDriver, rdmaAddressTable,
				pmcAddressTable, connector,
				l2TLBTable)
		}
	}
}

func (b R9NanoPlatformBuilder) createPMCPageTable() *mem.BankedLowModuleFinder {
	pmcAddressTable := new(mem.BankedLowModuleFinder)
	pmcAddressTable.BankSize = 8 * mem.GB
	pmcAddressTable.LowModules = append(pmcAddressTable.LowModules, nil)
	return pmcAddressTable
}

func (b R9NanoPlatformBuilder) createRDMAAddrTable() *mem.BankedLowModuleFinder {
	rdmaAddressTable := new(mem.BankedLowModuleFinder)
	rdmaAddressTable.BankSize = 8 * mem.GB
	rdmaAddressTable.LowModules = append(rdmaAddressTable.LowModules, nil)
	return rdmaAddressTable
}

func (b R9NanoPlatformBuilder) createConnection(
	engine sim.Engine,
	gpuDriver *driver.Driver,
	// mmuComponent *mmu.MMU,
) *mesh.Connector {
	connector := mesh.NewConnector().
		WithEngine(engine).
		WithFreq(1 * sim.GHz).
		WithFlitSize(b.networkFlitSize).
		WithBandwidth(float64(b.bandwidth)).
		WithSwitchLatency(b.switchLatency).
		WithEndpointTransferPerCycle(b.endpointChannels).
		WithEndpointBufferSize(b.endpointBufferSize)

	if b.traceVis {
		connector = connector.WithVisTracer(b.visTracer)
	}

	connector.CreateNetwork("Mesh")
	connector.AddTile([3]int{b.tileWidth / 2, b.tileHeight / 2, 0}, []sim.Port{
		gpuDriver.GetPortByName("GPU"),
		// gpuDriver.GetPortByName("MMUCache"),
		// b.IOMMU.GetPortByName("Migration"),
		b.IOMMUTLB.GetPortByName("Top"),
	})

	return connector
}

func (b R9NanoPlatformBuilder) createEngine() sim.Engine {
	var engine sim.Engine

	if b.useParallelEngine {
		engine = sim.NewParallelEngine()
	} else {
		engine = sim.NewSerialEngine()
	}
	// engine.AcceptHook(sim.NewEventLogger(log.New(os.Stdout, "", 0)))

	return engine
}

func (b R9NanoPlatformBuilder) createMMU(
	engine sim.Engine,
	l2TLBTable *mem.MultiPageFinder,
) (*mmu.MMU, vm.PageTable) {
	pageTable := vm.NewPageTable(b.log2PageSize)
	mmuBuilder := mmu.MakeBuilder().
		WithEngine(engine).
		WithFreq(1 * sim.GHz).
		WithPageWalkingLatency(100).
		WithLog2PageSize(b.log2PageSize).
		WithMaxNumReqInFlight(256).
		WithPageTable(pageTable).
		WithL2TLBTable(l2TLBTable).
		WithWalkCoalescing(*mmuWalkCoalescing).
		WithMMUTopModule(b.mmuTopModule)

	mmuComponent := mmuBuilder.Build("MMU")

	if b.monitor != nil {
		b.monitor.RegisterComponent(mmuComponent)
	}

	if b.perfAnalyzer != nil {
		b.perfAnalyzer.RegisterComponent(mmuComponent)
	}

	if b.visTracer != nil {
		tracing.CollectTrace(mmuComponent, b.visTracer)
	}

	return mmuComponent, pageTable
}

func (b *R9NanoPlatformBuilder) createGPUBuilder(
	engine sim.Engine,
	gpuDriver *driver.Driver,
	mmuComponent *mmu.MMU,
	numMemoryBank int,
	pageTable vm.PageTable,
) R9NanoGPUBuilder {
	gpuBuilder := MakeR9NanoGPUBuilder().
		WithEngine(engine).
		WithMMU(mmuComponent).
		WithIOMMUCache(b.IOMMUTLB).
		WithNumCUPerShaderArray(b.numCUPerSA).
		WithNumShaderArray(b.numSAPerGPU).
		WithNumMemoryBank(numMemoryBank).
		WithL2CacheSize(4*mem.MB).
		WithLog2MemoryBankInterleavingSize(7).
		WithLog2PageSize(b.log2PageSize).
		WithL1VRemoteMaxInflight(b.l1vRemoteMaxInflight).
		WithL1VMSHREntries(b.l1vMSHREntries).
		WithL1VMaxConcurrentTrans(b.l1vMaxConcurrentTrans).
		WithForceLocalDataAccess(b.forceLocalDataAccess).
		WithL2ResidentFilter(b.l2ResidentFilter).
		WithL2FilterPrefetch(b.l2FilterPrefetch).
		WithL2PrefetchDiagnostics(
			b.l2PrefetchPredictorOnly, b.l2PrefetchUngated).
		WithL2GranularityAdaptation(
			b.l2GranularityAdaptation,
			b.l2GranularityNoFilter,
			b.l2GranularityAlways,
			b.l2GranularityPredictor).
		WithL2AdaptivePair(b.l2AdaptivePair).
		WithPrefetchPredictorEntries(b.prefetchPredictorEntries).
		WithL2FillForwarding(b.l2FillForwarding).
		WithTypedFilterConfig(b.typedFilterConfig).
		WithDRAMRowContinuation(b.dramRowContinuation).
		WithRemoteDataPath(b.remoteDataPath).
		WithRDMAPipeline(
			b.rdmaPipelineWidth,
			b.rdmaPipelineLatency,
			b.rdmaMaxOutstanding,
		).
		WithGlobalStorage(b.globalStorage).
		WithPerfAnalyzer(b.perfAnalyzer).
		WithGMMUPageTable(pageTable)

	if b.monitor != nil {
		gpuBuilder = gpuBuilder.WithMonitor(b.monitor)
	}

	gpuBuilder = b.setVisTracer(gpuDriver, gpuBuilder)
	gpuBuilder = b.setMemTracer(gpuBuilder)
	gpuBuilder = b.setSharingTracer(gpuBuilder)
	gpuBuilder = b.setISADebugger(gpuBuilder)

	return gpuBuilder
}

func (b *R9NanoPlatformBuilder) setSharingTracer(
	gpuBuilder R9NanoGPUBuilder,
) R9NanoGPUBuilder {
	if b.sharingTracer == nil {
		return gpuBuilder
	}

	return gpuBuilder.WithSharingTracer(b.sharingTracer)
}

func (b *R9NanoPlatformBuilder) setISADebugger(
	gpuBuilder R9NanoGPUBuilder,
) R9NanoGPUBuilder {
	if !b.debugISA {
		return gpuBuilder
	}

	gpuBuilder = gpuBuilder.WithISADebugging()
	return gpuBuilder
}

func (b *R9NanoPlatformBuilder) setMemTracer(
	gpuBuilder R9NanoGPUBuilder,
) R9NanoGPUBuilder {
	if !b.traceMem {
		return gpuBuilder
	}

	file, err := os.Create("mem.trace")
	if err != nil {
		panic(err)
	}
	logger := log.New(file, "", 0)
	memTracer := memtraces.NewTracer(logger, b.engine)
	gpuBuilder = gpuBuilder.WithMemTracer(memTracer)
	return gpuBuilder
}

func (b *R9NanoPlatformBuilder) setVisTracer(
	gpuDriver *driver.Driver,
	gpuBuilder R9NanoGPUBuilder,
) R9NanoGPUBuilder {
	if b.traceVis {
		gpuBuilder = gpuBuilder.WithVisTracer(b.visTracer)
	}

	return gpuBuilder
}

func (b *R9NanoPlatformBuilder) createGPU(
	x, y int,
	gpuBuilder R9NanoGPUBuilder,
	gpuDriver *driver.Driver,
	rdmaAddressTable *mem.BankedLowModuleFinder,
	pmcAddressTable *mem.BankedLowModuleFinder,
	connector *mesh.Connector,
	l2TLBTable *mem.MultiPageFinder,
) *GPU {
	index := uint64(len(b.gpus)) + 1
	gpuid := x + y*b.tileWidth
	name := fmt.Sprintf("GPU[%d]", gpuid)
	memAddrOffset := index * 8 * mem.GB
	// fmt.Printf("GPU[%d], index %d, memAddrOffset %X\n", gpuid, index, memAddrOffset)
	gpu := gpuBuilder.
		WithMemAddrOffset(memAddrOffset).
		WithL2TLBTable(l2TLBTable).
		Build(name, uint64(index))
	gpuDriver.RegisterGPU(gpu.Domain.GetPortByName("CommandProcessor"),
		driver.DeviceProperties{
			CUCount:  32,
			DRAMSize: 8 * mem.GB,
		})
	gpu.CommandProcessor.(*cp.CommandProcessor).Driver =
		gpuDriver.GetPortByName("GPU")

	gpu.GPUID = index

	b.configRDMAEngine(gpu, rdmaAddressTable)
	b.configPMC(gpu, gpuDriver, pmcAddressTable)
	b.configL2TLBTable(gpu, l2TLBTable)

	connector.AddTile([3]int{x, y, 0}, gpu.Domain.Ports())

	b.gpus = append(b.gpus, gpu)

	return gpu
}

func (b *R9NanoPlatformBuilder) configRDMAEngine(
	gpu *GPU,
	addrTable *mem.BankedLowModuleFinder,
) {
	gpu.RDMAEngine.RemoteRDMAAddressTable = addrTable

	addrTable.LowModules = append(
		addrTable.LowModules,
		gpu.RDMAEngine.ToOutside)
}

func (b *R9NanoPlatformBuilder) configPMC(
	gpu *GPU,
	gpuDriver *driver.Driver,
	addrTable *mem.BankedLowModuleFinder,
) {
	gpu.PMC.RemotePMCAddressTable = addrTable
	addrTable.LowModules = append(
		addrTable.LowModules,
		gpu.PMC.GetPortByName("Remote"))
	gpuDriver.RemotePMCPorts = append(
		gpuDriver.RemotePMCPorts, gpu.PMC.GetPortByName("Remote"))
}

func (b *R9NanoPlatformBuilder) setupPerfermanceTracing() {

	if b.perfAnalysisFileName != "" {
		b.perfAnalyzer = analysis.MakePerfAnalyzerBuilder().
			WithPeriod(sim.VTimeInSec(b.perfAnalyzingPeriod)).
			WithDBFilename(b.perfAnalysisFileName).
			WithEngine(b.engine).
			Build()
	}
}

func (b *R9NanoPlatformBuilder) createL2TLBTable() *mem.MultiPageFinder {
	table := mem.NewMultiPageFinder()
	return table
}

func (b *R9NanoPlatformBuilder) configL2TLBTable(
	gpu *GPU,
	table *mem.MultiPageFinder,
) {
	gpu.L2TLB.PageFinder = table
	table.LowModules[uint64(gpu.GPUID)] = gpu.L2TLB.OutsidePort
}

func (b *R9NanoPlatformBuilder) createIOMMUTLB(
	l2TLBTable *mem.MultiPageFinder,
	pageTable vm.PageTable,
) {
	name := fmt.Sprintf("IOMMUTLB")
	b.IOMMUTLB = mmuTLB.MakeBuilder().
		WithEngine(b.engine).
		WithFreq(1 * sim.GHz).
		WithNumWays(32).
		WithNumSets(64).
		WithNumMSHREntry(64).
		WithMSHREntryDepth(64).
		WithNumReqPerCycle(32).
		WithPageSize(1 << b.log2PageSize).
		WithLowModule(b.IOMMUCache.GetPortByName("Top")).
		WithPageTable(pageTable).
		WithL2TLBTable(l2TLBTable).
		WithLog2PageSize(b.log2PageSize).
		WithLookupLatencyCycles(*mmutlbLookupLatency).
		Build(name)

	if b.monitor != nil {
		b.monitor.RegisterComponent(b.IOMMUTLB)
	}
}

func (b *R9NanoPlatformBuilder) createIOMMUCache() {
	name := "IOMMUCache"
	b.IOMMUCache = mmuCache.MakeBuilder().
		WithEngine(b.engine).
		WithFreq(1 * sim.GHz).
		WithNumWays(1).
		WithNumSets(32).
		WithNumMSHREntry(64).
		WithMSHREntryDepth(64).
		WithNumReqPerCycle(256).
		WithLog2PageSize(b.log2PageSize).
		WithLowModule(b.mmu.GetPortByName("Top")).
		Build(name)

	b.mmuTopModule = b.IOMMUCache.GetPortByName("Bottom")

	if b.monitor != nil {
		b.monitor.RegisterComponent(b.IOMMUCache)
	}
}

func (b *R9NanoPlatformBuilder) connIOMMUWithIOMMUCache() {
	conn := sim.NewDirectConnection(
		b.IOMMUTLB.Name()+".IOMMUCache",
		b.engine, 1*sim.GHz,
	)
	conn.PlugIn(b.IOMMUCache.GetPortByName("Bottom"), 48)
	conn.PlugIn(b.mmu.GetPortByName("Top"), 48)
}

func (b *R9NanoPlatformBuilder) connIOMMUCacheWithIOMMUTLB() {
	conn := sim.NewDirectConnection(
		b.IOMMUCache.Name()+".IOMMU",
		b.engine, 1*sim.GHz,
	)
	conn.PlugIn(b.IOMMUTLB.GetPortByName("Bottom"), 48)
	conn.PlugIn(b.IOMMUCache.GetPortByName("Top"), 48)
}
