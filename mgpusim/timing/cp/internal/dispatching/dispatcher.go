package dispatching

import (
	"fmt"
	"log"

	"github.com/sarchlab/akita/v3/monitoring"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
	"github.com/sarchlab/mgpusim/v3/emu"
	"github.com/sarchlab/mgpusim/v3/kernels"
	"github.com/sarchlab/mgpusim/v3/protocol"
	"github.com/sarchlab/mgpusim/v3/samples/sampledrunner"
	"github.com/sarchlab/mgpusim/v3/timing/cp/internal/resource"
)

// A Dispatcher is a sub-component of a command processor that can dispatch
// work-groups to compute units.
type Dispatcher interface {
	tracing.NamedHookable
	RegisterCU(cu resource.DispatchableCU)
	IsDispatching() bool
	StartDispatching(req *protocol.LaunchKernelReq)
	Tick(now sim.VTimeInSec) (madeProgress bool)
}

// A DispatcherImpl is a ticking component that can dispatch work-groups.
type DispatcherImpl struct {
	sim.HookableBase

	cp                     tracing.NamedHookable
	name                   string
	gpuID                  uint64
	respondingPort         sim.Port
	dispatchingPort        sim.Port
	alg                    algorithm
	dispatching            *protocol.LaunchKernelReq
	currWG                 dispatchLocation
	cycleLeft              int
	numDispatchedWGs       int
	numCompletedWGs        int
	numDispatchedWFs       uint64
	numCompletedWFs        uint64
	inflightWGs            map[string]dispatchLocation
	originalReqs           map[string]*protocol.MapWGReq
	latencyTable           []int
	constantKernelOverhead int

	monitor     *monitoring.Monitor
	progressBar *monitoring.ProgressBar
}

// Name returns the name of the dispatcher
func (d *DispatcherImpl) Name() string {
	return d.name
}

// RegisterCU allows the dispatcher to dispatch work-groups to the CU.
func (d *DispatcherImpl) RegisterCU(cu resource.DispatchableCU) {
	d.alg.RegisterCU(cu)
}

// IsDispatching checks if the dispatcher is dispatching another kernel.
func (d *DispatcherImpl) IsDispatching() bool {
	return d.dispatching != nil
}

func (d *DispatcherImpl) staticAnalysisKernelSampled() {
	if !*sampledrunner.BranchSampledFlag {
		return
	}

	staticCU := emu.StaticComputeUnitForGPU(d.gpuID)
	if staticCU == nil {
		sampledrunner.PhotonDebugf(
			fmt.Sprintf("GPU%d.Dispatcher", d.gpuID),
			"branch static analysis skipped: no static compute unit")
		return
	}
	if d.alg.NumWG() == 0 {
		sampledrunner.PhotonDebugf(
			fmt.Sprintf("GPU%d.Dispatcher", d.gpuID),
			"branch static analysis skipped: no workgroups")
		return
	}

	currWG := d.alg.Next()
	if !currWG.valid {
		sampledrunner.PhotonDebugf(
			fmt.Sprintf("GPU%d.Dispatcher", d.gpuID),
			"branch static analysis skipped: no dispatchable WG")
		return
	}
	defer d.alg.FreeResources(currWG)

	reqBuilder := protocol.MapWGReqBuilder{}.
		WithWG(currWG.wg).
		WithPID(d.dispatching.PID)
	for _, l := range currWG.locations {
		reqBuilder = reqBuilder.AddWf(l)
	}

	staticCU.Reset()
	staticCU.AnalysisKernel(reqBuilder.Build())
	if branchEngine := sampledrunner.BranchSampledEngineForGPU(d.gpuID); branchEngine != nil {
		branchEngine.SetStaticComputeUnit(staticCU)
	}
	sampledrunner.PhotonDebugf(
		fmt.Sprintf("GPU%d.Dispatcher", d.gpuID),
		"branch static analysis complete bbls=%d",
		len(staticCU.Bbvset))
}

func (d *DispatcherImpl) analysisKernelSampled() (wgNumToSkip, wfPerWG uint64) {
	if emu.Bbvcomputeunit == nil {
		sampledrunner.PhotonDebugf(
			fmt.Sprintf("GPU%d.Dispatcher", d.gpuID),
			"sample analysis skipped: no BBV compute unit")
		return 0, 0
	}

	numWG := uint64(d.alg.NumWG())
	var analyzedWGs uint64
	for wgIdx := uint64(0); d.alg.HasNext(); wgIdx++ {
		currWG := d.alg.Next()
		if !currWG.valid {
			break
		}
		stopAfterWG := false

		if wfPerWG == 0 {
			wfPerWG = uint64(len(currWG.locations))
			emu.Bbvcomputeunit.Wfnum = numWG * wfPerWG
		}

		if wgIdx%100 == 0 {
			reqBuilder := protocol.MapWGReqBuilder{}.
				WithWG(currWG.wg).
				WithPID(d.dispatching.PID)
			for _, l := range currWG.locations {
				reqBuilder = reqBuilder.AddWf(l)
			}

			emu.Bbvcomputeunit.RunWG(reqBuilder.Build())
			analyzedWGs++
			stopAfterWG = (*sampledrunner.BranchSampledFlag ||
				*sampledrunner.SampledRunnerFlag) &&
				wgIdx > *sampledrunner.KernelSampledThreshold
		}

		d.alg.FreeResources(currWG)

		if stopAfterWG {
			break
		}
	}

	bbvs := emu.Bbvcomputeunit.GetAllonlineBBVs()
	if branchEngine := sampledrunner.BranchSampledEngineForGPU(d.gpuID); branchEngine != nil {
		branchEngine.Analysis(bbvs)
	}

	if wfPerWG > 0 && *sampledrunner.KernelSampledFlag {
		if kernelEngine := sampledrunner.KernelSampledEngineForGPU(d.gpuID); kernelEngine != nil {
			wfNumToSkip := kernelEngine.Analysis(bbvs, numWG*wfPerWG)
			wgNumToSkip = wfNumToSkip / wfPerWG
		}
	}

	sampledrunner.PhotonDebugf(
		fmt.Sprintf("GPU%d.Dispatcher", d.gpuID),
		"sample analysis complete analyzedWGs=%d bbvs=%d wfPerWG=%d wgSkip=%d",
		analyzedWGs,
		len(bbvs),
		wfPerWG,
		wgNumToSkip)
	return wgNumToSkip, wfPerWG
}

// StartDispatching lets the dispatcher to start dispatch another kernel.
func (d *DispatcherImpl) StartDispatching(req *protocol.LaunchKernelReq) {
	d.mustNotBeDispatchingAnotherKernel()

	info := kernels.KernelLaunchInfo{
		CodeObject: req.HsaCo,
		Packet:     req.Packet,
		PacketAddr: req.PacketAddress,
		WGFilter:   req.WGFilter,
	}
	d.alg.StartNewKernel(info)
	d.dispatching = req

	if *sampledrunner.SampledRunnerFlag ||
		*sampledrunner.BranchSampledFlag ||
		*sampledrunner.LoopSampledFlag ||
		*sampledrunner.KernelSampledFlag {
		packet := info.Packet
		workgroupSize := int(packet.WorkgroupSizeX) *
			int(packet.WorkgroupSizeY) *
			int(packet.WorkgroupSizeZ)
		wfNums := d.alg.NumWG() * workgroupSize / 64
		if sampledTimeEngine := sampledrunner.SampledTimeEngineForGPU(d.gpuID); sampledTimeEngine != nil {
			sampledTimeEngine.SetTargetCompletedWfs(uint64(wfNums))
		}
	}

	if *sampledrunner.BranchSampledFlag {
		d.staticAnalysisKernelSampled()
		d.alg.StartNewKernel(info)
	}

	kernelEngine := sampledrunner.KernelSampledEngineForGPU(d.gpuID)
	shouldAnalyze := *sampledrunner.BranchSampledFlag ||
		(*sampledrunner.KernelSampledFlag &&
			kernelEngine != nil &&
			kernelEngine.HistorySize() > 0)
	if shouldAnalyze &&
		uint64(d.alg.NumWG()) > *sampledrunner.KernelSampledThreshold {
		d.analysisKernelSampled()
		d.alg.StartNewKernel(info)
	}

	d.numDispatchedWGs = 0
	d.numCompletedWGs = 0
	d.numDispatchedWFs = 0
	d.numCompletedWFs = 0

	d.initializeProgressBar(req.ID)
}

func (d *DispatcherImpl) initializeProgressBar(kernelID string) {
	if d.monitor != nil {
		d.progressBar = d.monitor.CreateProgressBar(
			fmt.Sprintf("At %s, Kernel: %s, ", d.Name(), kernelID),
			uint64(d.alg.NumWG()),
		)
	}
}

func (d *DispatcherImpl) mustNotBeDispatchingAnotherKernel() {
	if d.IsDispatching() {
		panic("dispatcher is dispatching another request")
	}
}

// Tick updates the state of the dispatcher.
func (d *DispatcherImpl) Tick(now sim.VTimeInSec) (madeProgress bool) {
	if d.cycleLeft > 0 {
		d.cycleLeft--
		return true
	}

	if d.dispatching != nil {
		if d.kernelCompleted() {
			madeProgress = d.completeKernel(now) || madeProgress
		} else {
			madeProgress = d.dispatchNextWG(now) || madeProgress
		}
	}

	madeProgress = d.processMessagesFromCU(now) || madeProgress

	return madeProgress
}

func (d *DispatcherImpl) processMessagesFromCU(now sim.VTimeInSec) bool {
	msg := d.dispatchingPort.Peek()
	if msg == nil {
		return false
	}

	switch msg := msg.(type) {
	case *protocol.WGCompletionMsg:
		count := 0
		for _, rspToID := range msg.RspTo {
			_, ok := d.inflightWGs[rspToID]
			if ok {
				count += 1
			}
		}

		if count == 0 {
			return false
		} else if count < len(msg.RspTo) {
			log.Panic("In emulation all finished WGs from more than one dispatcher")
		}

		for _, rspToID := range msg.RspTo {
			location := d.inflightWGs[rspToID]
			d.alg.FreeResources(location)
			delete(d.inflightWGs, rspToID)
			d.numCompletedWGs++
			d.numCompletedWFs += uint64(len(location.locations))
			if d.numCompletedWGs == d.alg.NumWG() {
				d.cycleLeft = d.constantKernelOverhead
			}

			originalReq := d.originalReqs[rspToID]
			delete(d.originalReqs, rspToID)
			tracing.TraceReqFinalize(originalReq, d)

			if d.progressBar != nil {
				d.progressBar.MoveInProgressToFinished(1)
			}
		}

		// tracing.TraceReqInitiate(msg, d,
		// 	tracing.MsgIDAtReceiver(d.dispatching, d.cp))

		// tracing.StartTask(
		// 	msg.ID+"_wg_complete",
		// 	tracing.MsgIDAtReceiver(d.dispatching, d.cp),
		// 	d,
		// 	"req_in",
		// 	reflect.TypeOf(msg).String(),
		// 	msg,
		// )
		// tracing.EndTask(
		// 	msg.ID+"_wg_complete",
		// 	d,
		// )

		d.dispatchingPort.Retrieve(now)
		return true
	}

	return false
}

func (d *DispatcherImpl) kernelCompleted() bool {
	if d.currWG.valid {
		return false
	}

	if d.alg.HasNext() {
		return false
	}

	if d.numCompletedWGs < d.numDispatchedWGs {
		return false
	}

	return true
}

func (d *DispatcherImpl) completeKernel(now sim.VTimeInSec) (
	madeProgress bool,
) {
	req := d.dispatching

	rsp := protocol.NewLaunchKernelRsp(now, req.Dst, req.Src, req.ID)

	err := d.respondingPort.Send(rsp)
	if err == nil {
		d.dispatching = nil

		if d.monitor != nil {
			d.monitor.CompleteProgressBar(d.progressBar)
		}

		tracing.TraceReqComplete(req, d.cp)
		if (*sampledrunner.BranchSampledFlag ||
			*sampledrunner.LoopSampledFlag ||
			*sampledrunner.KernelSampledFlag) &&
			emu.Bbvcomputeunit != nil {
			emu.Bbvcomputeunit.FFlush()
		}

		return true
	}

	return false
}

func (d *DispatcherImpl) dispatchNextWG(
	now sim.VTimeInSec,
) (madeProgress bool) {
	if !d.currWG.valid {
		if !d.alg.HasNext() {
			d.enableDisabledSampleEngines()
			return false
		}

		d.currWG = d.alg.Next()
		if !d.currWG.valid {
			d.enableDisabledSampleEngines()
			return false
		}
		d.enableDisabledSampleEngines()
	}

	reqBuilder := protocol.MapWGReqBuilder{}.
		WithSrc(d.dispatchingPort).
		WithDst(d.currWG.cu).
		WithSendTime(now).
		WithPID(d.dispatching.PID).
		WithWG(d.currWG.wg)

	wfIntervalTime := sim.VTimeInSec(0)
	wfSkip := false
	sampledEngine := sampledrunner.SampledEngineForGPU(d.gpuID)
	if *sampledrunner.SampledRunnerFlag && sampledEngine != nil {
		wfIntervalTime, wfSkip = sampledEngine.Predict()
		if wfSkip {
			sampledrunner.PhotonDebugf(
				fmt.Sprintf("GPU%d.Dispatcher", d.gpuID),
				"wf sampled dispatch prediction skip=true pred=%.3fns",
				wfIntervalTime*1e9)
		} else {
			sampledrunner.PhotonVerbosef(
				fmt.Sprintf("GPU%d.Dispatcher", d.gpuID),
				"wf sampled dispatch prediction skip=false pred=%.3fns",
				wfIntervalTime*1e9)
		}
	}

	kernelSkip := false
	kernelEngine := sampledrunner.KernelSampledEngineForGPU(d.gpuID)
	if *sampledrunner.KernelSampledFlag &&
		kernelEngine != nil {
		kernelSkip = kernelEngine.EnableSampled()
		if kernelSkip {
			sampledrunner.PhotonDebugf(
				fmt.Sprintf("GPU%d.Dispatcher", d.gpuID),
				"kernel sampled dispatch enabled")
		} else {
			sampledrunner.PhotonVerbosef(
				fmt.Sprintf("GPU%d.Dispatcher", d.gpuID),
				"kernel sampled dispatch disabled")
		}
	}

	for idx, l := range d.currWG.locations {
		skip := kernelSkip
		intervalTime := sim.VTimeInSec(0)
		if kernelSkip {
			wfIdx := uint64(idx + d.numDispatchedWGs*len(d.currWG.locations))
			intervalTime, _ = kernelEngine.Predict(wfIdx)
			sampledrunner.PhotonDebugf(
				fmt.Sprintf("GPU%d.Dispatcher", d.gpuID),
				"kernel sampled wf marked skip wfidx=%d pred=%.3fns",
				wfIdx,
				intervalTime*1e9)
		}
		if !skip && *sampledrunner.SampledRunnerFlag {
			skip = wfSkip
			intervalTime = wfIntervalTime
		}
		l.Wavefront.Skip = skip
		l.Wavefront.Predtime = intervalTime
		reqBuilder = reqBuilder.AddWf(l)
	}
	req := reqBuilder.Build()
	err := d.dispatchingPort.Send(req)

	// fmt.Printf("%.10f, %d, %d\n", now, d.currWG.wg.IDX, d.currWG.cuID)

	if err == nil {
		d.currWG.valid = false
		d.numDispatchedWGs++
		d.numDispatchedWFs += uint64(len(d.currWG.locations))
		d.inflightWGs[req.ID] = d.currWG
		d.originalReqs[req.ID] = req
		d.cycleLeft = d.latencyTable[len(d.currWG.locations)]
		if sampledTimeEngine := sampledrunner.SampledTimeEngineForGPU(d.gpuID); sampledTimeEngine != nil {
			sampledTimeEngine.UpdateMaxWFS(d.numDispatchedWFs - d.numCompletedWFs)
		}

		if d.progressBar != nil {
			d.progressBar.IncrementInProgress(1)
		}

		tracing.TraceReqInitiate(req, d,
			tracing.MsgIDAtReceiver(d.dispatching, d.cp))

		return true
	}

	return false
}

func (d *DispatcherImpl) enableDisabledSampleEngines() {
	if *sampledrunner.SampledRunnerFlag {
		if sampledEngine := sampledrunner.SampledEngineForGPU(d.gpuID); sampledEngine != nil &&
			sampledEngine.IfDisable() {
			sampledEngine.Enable()
		}
	}

	if *sampledrunner.BranchSampledFlag || *sampledrunner.LoopSampledFlag {
		if branchEngine := sampledrunner.BranchSampledEngineForGPU(d.gpuID); branchEngine != nil &&
			branchEngine.IfDisable() {
			branchEngine.Enable()
		}
	}
}
