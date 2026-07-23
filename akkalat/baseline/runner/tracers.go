package runner

import (
	"strings"

	"github.com/sarchlab/akita/v3/mem/vm/gmmu"
	"github.com/sarchlab/akita/v3/mem/vm/l2tlb"
	"github.com/sarchlab/akita/v3/mem/vm/mmu"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
	"github.com/sarchlab/mgpusim/v3/timing/cu"
	"github.com/sarchlab/mgpusim/v3/timing/rdma"
	"github.com/tebeka/atexit"
)

type instCountTracer struct {
	tracer *instTracer
	cu     TraceableComponent
}

type wgCountTracer struct {
	tracer *wgTracer
	// cp     *cp.CommandProcessor
	cu TraceableComponent
}

type cacheLatencyTracer struct {
	tracer *tracing.AverageTimeTracer
	cache  TraceableComponent
}

type rdmaLatencyTracer struct {
	tracer *tracing.AverageTimeTracer
	rdma   TraceableComponent
}

type mmuLatencyTracer struct {
	tracer *tracing.AverageTimeTracer
	mmu    TraceableComponent
}

type gmmuLatencyTracer struct {
	tracer *tracing.AverageTimeTracer
	gmmu   TraceableComponent
}

type tlbLatencyTracer struct {
	tracer *tracing.AverageTimeTracer
	tlb    TraceableComponent
}

type cacheHitRateTracer struct {
	tracer *tracing.StepCountTracer
	cache  TraceableComponent
}

type tlbHitRateTracer struct {
	tracer *tracing.StepCountTracer
	tlb    TraceableComponent
}

type rdmaTransactionCountTracer struct {
	outgoingTracer *tracing.AverageTimeTracer
	incomingTracer *tracing.AverageTimeTracer
	rdmaEngine     *rdma.Comp
}

type gmmuTransactionCountTracer struct {
	outgoingTracer *tracing.AverageTimeTracer
	incomingTracer *tracing.AverageTimeTracer
	gmmuEngine     *gmmu.GMMU
}

//	type gmmuTransactionCountTracer struct {
//		tracer *mmuTracer
//		gmmu   *gmmu.GMMU
//	}
type mmuTransactionCountTracer struct {
	outgoingTracer *tracing.AverageTimeTracer
	incomingTracer *tracing.AverageTimeTracer
	mmuEngine      *mmu.MMU
}

type l2TLBHitRateTracer struct {
	tracer *tracing.StepCountTracer
	l2TLB  *l2tlb.L2TLB
}

type l2TLBLatencyTracer struct {
	tracer *tracing.AverageTimeTracer
	l2TLB  *l2tlb.L2TLB
}

type simdBusyTimeTracer struct {
	tracer *tracing.BusyTimeTracer
	simd   TraceableComponent
}
type dramTransactionCountTracer struct {
	tracer *dramTracer
	dram   TraceableComponent
}

type cuCPIStackTracer struct {
	cu     TraceableComponent
	tracer *cu.CPIStackTracer
}

func firstTraceForComponent(seen map[string]bool, comp sim.Named) bool {
	if comp == nil {
		return false
	}
	name := comp.Name()
	if seen[name] {
		return false
	}
	seen[name] = true
	return true
}

// type gmmuCountTracer struct {
// 	tracer *gmmuTracer
// 	gmmu   *gmmu.GMMU
// }

// type L2TLBTracer struct {
// 	tracer *L2TLBEvictTracer
// 	tlb    *l2tlb.L2TLB
// }

func (r *Runner) defineMetrics() {
	r.metricsCollector = &collector{}
	r.addMaxInstStopper()
	r.addKernelTimeTracer()
	r.addInstCountTracer()
	r.addWGCountTracer()
	r.addMaxWGStopper()
	r.addCUCPIHook()
	r.addCacheLatencyTracer()
	r.addCacheHitRateTracer()
	r.addTLBHitRateTracer()
	r.addTLBLatencyTracer()
	r.addRDMAEngineTracer()
	r.addMMUEngineTracer()
	r.addGMMUEngineTracer()
	r.addDRAMTracer()
	r.addRDMALatencyTracer()
	r.addMMULatencyTracer()
	r.addGMMULatencyTracer()
	r.addL2TLBHitRateTracer()
	r.addL2TLBLatencyTracer()
	// r.addGMMUTracer()
	// r.addL2TLBTracer()

	atexit.Register(func() { r.flushMetrics() })
}

// func (r *Runner) addSIMDBusyTimeTracer() {
// 	if !r.ReportSIMDBusyTime {
// 		return
// 	}

// 	for _, gpu := range r.platform.GPUs {
// 		for _, simd := range gpu.SIMDs {
// 			perSIMDBusyTimeTracer := tracing.NewBusyTimeTracer(
// 				r.platform.Engine,
// 				func(task tracing.Task) bool {
// 					return task.Kind == "pipeline"
// 				})
// 			r.simdBusyTimeTracers = append(r.simdBusyTimeTracers,
// 				simdBusyTimeTracer{
// 					tracer: perSIMDBusyTimeTracer,
// 					simd:   simd,
// 				})
// 			tracing.CollectTrace(simd, perSIMDBusyTimeTracer)
// 		}
// 	}
// }

func (r *Runner) addMaxInstStopper() {
	if *maxInstCount == 0 {
		return
	}

	r.maxInstStopper = newInstStopper(*maxInstCount)
	for _, gpu := range r.platform.GPUs {
		for _, cu := range gpu.CUs {
			tracing.CollectTrace(cu.(tracing.NamedHookable), r.maxInstStopper)
		}
	}
}

func (r *Runner) addMaxWGStopper() {
	if *maxWGCount == 0 {
		return
	}

	r.maxWGStopper = newWGStopper(*maxWGCount)
	for _, gpu := range r.platform.GPUs {
		for _, cu := range gpu.CUs {
			tracing.CollectTrace(cu.(tracing.NamedHookable), r.maxWGStopper)
		}
	}
}

func (r *Runner) addKernelTimeTracer() {
	r.kernelTimeCounter = tracing.NewBusyTimeTracer(
		r.platform.Engine,
		func(task tracing.Task) bool {
			return task.What == "*driver.LaunchKernelCommand"
		})
	tracing.CollectTrace(r.platform.Driver, r.kernelTimeCounter)

	for _, gpu := range r.platform.GPUs {
		gpuKernelTimeCounter := tracing.NewBusyTimeTracer(
			r.platform.Engine,
			func(task tracing.Task) bool {
				return task.What == "*protocol.LaunchKernelReq"
			})
		r.perGPUKernelTimeCounter = append(
			r.perGPUKernelTimeCounter, gpuKernelTimeCounter)
		tracing.CollectTrace(gpu.CommandProcessor, gpuKernelTimeCounter)
	}
}

func (r *Runner) addInstCountTracer() {
	if !r.ReportInstCount {
		return
	}

	for _, gpu := range r.platform.GPUs {
		for _, cu := range gpu.CUs {
			tracer := newInstTracer()
			r.instCountTracers = append(r.instCountTracers,
				instCountTracer{
					tracer: tracer,
					cu:     cu,
				})
			tracing.CollectTrace(cu.(tracing.NamedHookable), tracer)
		}
	}
}

func (r *Runner) addWGCountTracer() {
	if !r.ReportInstCount && *maxWGCount == 0 {
		return
	}

	for _, gpu := range r.platform.GPUs {
		for _, cu := range gpu.CUs {
			tracer := newWGTracer()
			r.wgCountTracers = append(r.wgCountTracers,
				wgCountTracer{
					tracer: tracer,
					cu:     cu,
				})
			tracing.CollectTrace(cu.(tracing.NamedHookable), tracer)
		}
	}
}

func (r *Runner) addCUCPIHook() {
	if !r.ReportCPIStack {
		return
	}

	for _, gpu := range r.platform.GPUs {
		for _, cuComp := range gpu.CUs {
			tracer := cu.NewCPIStackInstHook(
				cuComp.(*cu.ComputeUnit), r.platform.Engine)
			tracing.CollectTrace(cuComp.(tracing.NamedHookable), tracer)

			r.cuCPITraces = append(r.cuCPITraces,
				cuCPIStackTracer{
					tracer: tracer,
					cu:     cuComp,
				})
		}
	}
}

func (r *Runner) addCacheLatencyTracer() {
	if !r.ReportCacheLatency {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		for _, cache := range gpu.L1ICaches {
			if !firstTraceForComponent(seen, cache) {
				continue
			}
			tracer := tracing.NewAverageTimeTracer(
				r.platform.Engine,
				func(task tracing.Task) bool {
					return task.Kind == "req_in"
				})
			r.cacheLatencyTracers = append(r.cacheLatencyTracers,
				cacheLatencyTracer{tracer: tracer, cache: cache})
			tracing.CollectTrace(cache, tracer)
		}

		for _, cache := range gpu.L1SCaches {
			if !firstTraceForComponent(seen, cache) {
				continue
			}
			tracer := tracing.NewAverageTimeTracer(
				r.platform.Engine,
				func(task tracing.Task) bool {
					return task.Kind == "req_in"
				})
			r.cacheLatencyTracers = append(r.cacheLatencyTracers,
				cacheLatencyTracer{tracer: tracer, cache: cache})
			tracing.CollectTrace(cache, tracer)
		}

		for _, cache := range gpu.L1VCaches {
			if !firstTraceForComponent(seen, cache) {
				continue
			}
			tracer := tracing.NewAverageTimeTracer(
				r.platform.Engine,
				func(task tracing.Task) bool {
					return task.Kind == "req_in"
				})
			r.cacheLatencyTracers = append(r.cacheLatencyTracers,
				cacheLatencyTracer{tracer: tracer, cache: cache})
			tracing.CollectTrace(cache, tracer)
		}

		for _, cache := range gpu.L2Caches {
			if !firstTraceForComponent(seen, cache) {
				continue
			}
			tracer := tracing.NewAverageTimeTracer(
				r.platform.Engine,
				func(task tracing.Task) bool {
					return task.Kind == "req_in"
				})
			r.cacheLatencyTracers = append(r.cacheLatencyTracers,
				cacheLatencyTracer{tracer: tracer, cache: cache})
			tracing.CollectTrace(cache, tracer)
		}
	}
}

func (r *Runner) addCacheHitRateTracer() {
	if !r.ReportCacheHitRate {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		for _, cache := range gpu.L1VCaches {
			if !firstTraceForComponent(seen, cache) {
				continue
			}
			tracer := tracing.NewStepCountTracer(
				func(task tracing.Task) bool { return true })
			r.cacheHitRateTracers = append(r.cacheHitRateTracers,
				cacheHitRateTracer{tracer: tracer, cache: cache})
			tracing.CollectTrace(cache, tracer)
		}

		for _, cache := range gpu.L1SCaches {
			if !firstTraceForComponent(seen, cache) {
				continue
			}
			tracer := tracing.NewStepCountTracer(
				func(task tracing.Task) bool { return true })
			r.cacheHitRateTracers = append(r.cacheHitRateTracers,
				cacheHitRateTracer{tracer: tracer, cache: cache})
			tracing.CollectTrace(cache, tracer)
		}

		for _, cache := range gpu.L1ICaches {
			if !firstTraceForComponent(seen, cache) {
				continue
			}
			tracer := tracing.NewStepCountTracer(
				func(task tracing.Task) bool { return true })
			r.cacheHitRateTracers = append(r.cacheHitRateTracers,
				cacheHitRateTracer{tracer: tracer, cache: cache})
			tracing.CollectTrace(cache, tracer)
		}

		for _, cache := range gpu.L2Caches {
			if !firstTraceForComponent(seen, cache) {
				continue
			}
			tracer := tracing.NewStepCountTracer(
				func(task tracing.Task) bool { return true })
			r.cacheHitRateTracers = append(r.cacheHitRateTracers,
				cacheHitRateTracer{tracer: tracer, cache: cache})
			tracing.CollectTrace(cache, tracer)
		}
	}
}

func (r *Runner) addRDMALatencyTracer() {
	if !r.ReportRDMALatency {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		rdma := gpu.RDMAEngine
		if !firstTraceForComponent(seen, rdma) {
			continue
		}
		tracer := tracing.NewAverageTimeTracer(
			r.platform.Engine,
			func(task tracing.Task) bool {
				return task.Kind == "req_in"
			})
		r.rdmaLatencyTracers = append(r.rdmaLatencyTracers,
			rdmaLatencyTracer{tracer: tracer, rdma: rdma})
		tracing.CollectTrace(rdma, tracer)
	}
}

func (r *Runner) addTLBLatencyTracer() {
	if !r.ReportTLBLatency {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		for _, tlb := range gpu.L1VTLBs {
			if !firstTraceForComponent(seen, tlb) {
				continue
			}
			tracer := tracing.NewAverageTimeTracer(
				r.platform.Engine,
				func(task tracing.Task) bool {
					return task.Kind == "req_in"
				})
			r.tlbLatencyTracers = append(r.tlbLatencyTracers,
				tlbLatencyTracer{tracer: tracer, tlb: tlb})
			tracing.CollectTrace(tlb, tracer)
		}

		for _, tlb := range gpu.L1STLBs {
			if !firstTraceForComponent(seen, tlb) {
				continue
			}
			tracer := tracing.NewAverageTimeTracer(
				r.platform.Engine,
				func(task tracing.Task) bool {
					return task.Kind == "req_in"
				})
			r.tlbLatencyTracers = append(r.tlbLatencyTracers,
				tlbLatencyTracer{tracer: tracer, tlb: tlb})
			tracing.CollectTrace(tlb, tracer)
		}

		for _, tlb := range gpu.L1ITLBs {
			if !firstTraceForComponent(seen, tlb) {
				continue
			}
			tracer := tracing.NewAverageTimeTracer(
				r.platform.Engine,
				func(task tracing.Task) bool {
					return task.Kind == "req_in"
				})
			r.tlbLatencyTracers = append(r.tlbLatencyTracers,
				tlbLatencyTracer{tracer: tracer, tlb: tlb})
			tracing.CollectTrace(tlb, tracer)
		}

		// L2 TLBs use dedicated tracers below. Registering them here as well
		// duplicates hit/miss/latency rows in the metrics CSV.
	}
}

func (r *Runner) addTLBHitRateTracer() {
	if !r.ReportTLBHitRate {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		for _, tlb := range gpu.L1VTLBs {
			if !firstTraceForComponent(seen, tlb) {
				continue
			}
			tracer := tracing.NewStepCountTracer(
				func(task tracing.Task) bool { return true })
			r.tlbHitRateTracers = append(r.tlbHitRateTracers,
				tlbHitRateTracer{tracer: tracer, tlb: tlb})
			tracing.CollectTrace(tlb, tracer)
		}

		for _, tlb := range gpu.L1STLBs {
			if !firstTraceForComponent(seen, tlb) {
				continue
			}
			tracer := tracing.NewStepCountTracer(
				func(task tracing.Task) bool { return true })
			r.tlbHitRateTracers = append(r.tlbHitRateTracers,
				tlbHitRateTracer{tracer: tracer, tlb: tlb})
			tracing.CollectTrace(tlb, tracer)
		}

		for _, tlb := range gpu.L1ITLBs {
			if !firstTraceForComponent(seen, tlb) {
				continue
			}
			tracer := tracing.NewStepCountTracer(
				func(task tracing.Task) bool { return true })
			r.tlbHitRateTracers = append(r.tlbHitRateTracers,
				tlbHitRateTracer{tracer: tracer, tlb: tlb})
			tracing.CollectTrace(tlb, tracer)
		}

		// L2 TLBs use dedicated tracers below. Registering them here as well
		// duplicates hit/miss/latency rows in the metrics CSV.
	}
}

func (r *Runner) addL2TLBHitRateTracer() {
	if !r.ReportL2TLBHitRate {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		if !firstTraceForComponent(seen, gpu.L2TLB) {
			continue
		}
		tracer := tracing.NewStepCountTracer(
			func(task tracing.Task) bool { return true })
		r.l2TLBHitRateTracers = append(
			r.l2TLBHitRateTracers,
			l2TLBHitRateTracer{
				tracer: tracer,
				l2TLB:  gpu.L2TLB,
			},
		)
		tracing.CollectTrace(gpu.L2TLB, tracer)
	}
}

func (r *Runner) addL2TLBLatencyTracer() {
	if !r.ReportL2TLBLatency {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		if !firstTraceForComponent(seen, gpu.L2TLB) {
			continue
		}
		tracer := tracing.NewAverageTimeTracer(
			r.platform.Engine,
			func(task tracing.Task) bool {
				return task.Kind == "req_in"
			},
		)
		r.l2TLBLatencyTracers = append(
			r.l2TLBLatencyTracers,
			l2TLBLatencyTracer{
				tracer: tracer,
				l2TLB:  gpu.L2TLB,
			},
		)
		tracing.CollectTrace(gpu.L2TLB, tracer)
	}
}

func (r *Runner) addRDMAEngineTracer() {
	if !r.ReportRDMATransactionCount {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		if !firstTraceForComponent(seen, gpu.RDMAEngine) {
			continue
		}
		t := rdmaTransactionCountTracer{}
		t.rdmaEngine = gpu.RDMAEngine
		t.incomingTracer = tracing.NewAverageTimeTracer(
			r.platform.Engine,
			func(task tracing.Task) bool {
				if task.Kind != "req_in" {
					return false
				}

				isFromOutside := strings.Contains(
					task.Detail.(sim.Msg).Meta().Src.Name(), "RDMA")
				if !isFromOutside {
					return false
				}

				return true
			})
		t.outgoingTracer = tracing.NewAverageTimeTracer(
			r.platform.Engine,
			func(task tracing.Task) bool {
				if task.Kind != "req_in" {
					return false
				}

				isFromOutside := strings.Contains(
					task.Detail.(sim.Msg).Meta().Src.Name(), "RDMA")
				if isFromOutside {
					return false
				}

				return true
			})

		tracing.CollectTrace(t.rdmaEngine, t.incomingTracer)
		tracing.CollectTrace(t.rdmaEngine, t.outgoingTracer)

		r.rdmaTransactionCounters = append(r.rdmaTransactionCounters, t)
	}
}

func (r *Runner) addMMUEngineTracer() {
	if !r.ReportMMUTransactionCount {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		if !firstTraceForComponent(seen, gpu.MMUEngine) {
			continue
		}

		t := mmuTransactionCountTracer{}
		// t.mmuEngine = gpu.MMUEngine
		t.mmuEngine = gpu.MMUEngine
		t.incomingTracer = tracing.NewAverageTimeTracer(
			r.platform.Engine,
			func(task tracing.Task) bool {
				if task.Kind != "req_in" {
					return false
				}

				isFromOutside := strings.Contains(
					task.Detail.(sim.Msg).Meta().Dst.Name(), "MMU")
				if !isFromOutside {
					return false
				}

				return true
			})
		t.outgoingTracer = tracing.NewAverageTimeTracer(
			r.platform.Engine,
			func(task tracing.Task) bool {
				if task.Kind != "req_in" {
					return false
				}

				isFromOutside := strings.Contains(
					task.Detail.(sim.Msg).Meta().Src.Name(), "MMU")
				if isFromOutside {
					return false
				}

				return true
			})

		tracing.CollectTrace(t.mmuEngine, t.incomingTracer)
		tracing.CollectTrace(t.mmuEngine, t.outgoingTracer)

		r.mmuTransactionCounters = append(r.mmuTransactionCounters, t)
	}
}

func (r *Runner) addGMMUEngineTracer() {
	if !r.ReportGMMUTransactionCount {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		if !firstTraceForComponent(seen, gpu.GMMUEngine) {
			continue
		}
		t := gmmuTransactionCountTracer{}
		// t := mmuTransactionCountTracer{}
		t.gmmuEngine = gpu.GMMUEngine
		t.incomingTracer = tracing.NewAverageTimeTracer(
			r.platform.Engine,
			func(task tracing.Task) bool {
				if task.Kind != "req_in" {
					return false
				}

				isFromOutside := strings.Contains(
					task.Detail.(sim.Msg).Meta().Dst.Name(), "GMMU")
				if !isFromOutside {
					return false
				}

				return true
			})
		t.outgoingTracer = tracing.NewAverageTimeTracer(
			r.platform.Engine,
			func(task tracing.Task) bool {
				if task.Kind != "req_in" {
					return false
				}

				isFromOutside := strings.Contains(
					task.Detail.(sim.Msg).Meta().Src.Name(), "GMMU")
				if isFromOutside {
					return false
				}

				return true
			})

		tracing.CollectTrace(t.gmmuEngine, t.incomingTracer)
		tracing.CollectTrace(t.gmmuEngine, t.outgoingTracer)

		r.gmmuTransactionCounters = append(r.gmmuTransactionCounters, t)
	}
}

func (r *Runner) addMMULatencyTracer() {
	if !r.ReportMMULatency {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		mmu := gpu.MMUEngine
		if !firstTraceForComponent(seen, mmu) {
			continue
		}

		tracer := tracing.NewAverageTimeTracer(
			r.platform.Engine,
			func(task tracing.Task) bool {
				return task.Kind == "req_in"
			})
		r.mmuLatencyTracers = append(r.mmuLatencyTracers,
			mmuLatencyTracer{tracer: tracer, mmu: mmu})
		tracing.CollectTrace(mmu, tracer)

	}
}

func (r *Runner) addGMMULatencyTracer() {
	if !r.ReportGMMULatency {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		gmmu := gpu.GMMUEngine
		if !firstTraceForComponent(seen, gmmu) {
			continue
		}
		tracer := tracing.NewAverageTimeTracer(
			r.platform.Engine,
			func(task tracing.Task) bool {
				return task.Kind == "req_in"
			})
		r.gmmuLatencyTracers = append(r.gmmuLatencyTracers,
			gmmuLatencyTracer{tracer: tracer, gmmu: gmmu})
		tracing.CollectTrace(gmmu, tracer)

	}
}

func (r *Runner) addDRAMTracer() {
	if !r.ReportDRAMTransactionCount {
		return
	}

	seen := make(map[string]bool)
	for _, gpu := range r.platform.GPUs {
		for _, dram := range gpu.MemControllers {
			if !firstTraceForComponent(seen, dram.(TraceableComponent)) {
				continue
			}
			t := dramTransactionCountTracer{}
			t.dram = dram.(TraceableComponent)
			t.tracer = newDramTracer(r.platform.Engine)
			// t.tracer = newDramTracer(r)

			tracing.CollectTrace(t.dram, t.tracer)

			r.dramTracers = append(r.dramTracers, t)
		}
	}
}

// func (r *Runner) addGMMUTracer() {
// 	for _, gpu := range r.platform.GPUs {
// 		t := gmmuCountTracer{}
// 		t.gmmu = gpu.GMMUEngine
// 		t.tracer = newGMMUTracer(r.platform.Engine, t.gmmu)

// 		tracing.CollectTrace(t.gmmu, t.tracer)

// 		r.gmmuCountTracers = append(r.gmmuCountTracers, t)
// 	}
// }

// func (r *Runner) addL2TLBTracer() {
// 	for _, gpu := range r.platform.GPUs {
// 		t := L2TLBTracer{}
// 		t.tlb = gpu.L2TLB
// 		t.tracer = newGMMUEvictTracer(r.platform.Engine, t.tlb)

// 		tracing.CollectTrace(t.tlb, t.tracer)

// 		r.L2TLBTracers = append(r.L2TLBTracers, t)

// 	}
// }
