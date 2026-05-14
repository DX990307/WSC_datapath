package sampledrunner

import (
	"fmt"
	"sync"

	"github.com/sarchlab/akita/v3/sim"
)

var perGPUState struct {
	sync.RWMutex
	sampledEngines       map[uint64]*SampledEngine
	branchSampledEngines map[uint64]*BranchSampledEngine
	kernelSampledEngines map[uint64]*KernelSampledEngine
	sampledTimeEngines   map[uint64]*SampledTimeEngine
}

func ClearGPUSampledEngines() {
	perGPUState.Lock()
	defer perGPUState.Unlock()

	perGPUState.sampledEngines = nil
	perGPUState.branchSampledEngines = nil
	perGPUState.kernelSampledEngines = nil
	perGPUState.sampledTimeEngines = nil
}

func InitGPUSampledEngines(
	gpuID uint64,
	engine sim.Engine,
	freq sim.Freq,
	staticComputeUnit StaticComputeUnit,
) {
	perGPUState.Lock()
	defer perGPUState.Unlock()

	ensurePerGPUMaps()

	if *SampledRunnerFlag {
		sampledEngine := NewSampledEngineWithWarmup(
			*SampledRunnerGranularityFlag,
			*SampledRunnerThresholdFlag,
			false,
			*SampledRunnerWarmupFlag)
		sampledEngine.SetDebugLabel(fmt.Sprintf("GPU%d.WF", gpuID))
		perGPUState.sampledEngines[gpuID] = sampledEngine
		PhotonDebugf(fmt.Sprintf("GPU%d", gpuID),
			"init wf sampled engine threshold=%.6f warmup=%d granularity=%d",
			*SampledRunnerThresholdFlag,
			*SampledRunnerWarmupFlag,
			*SampledRunnerGranularityFlag)
	}

	if *BranchSampledFlag || *LoopSampledFlag {
		label := fmt.Sprintf("GPU%d.Branch", gpuID)
		perGPUState.branchSampledEngines[gpuID] =
			NewBranchSampledEngine(freq, staticComputeUnit, label)
		PhotonDebugf(fmt.Sprintf("GPU%d", gpuID),
			"init branch/loop sampled engine coverage=%.6f least-square=%.6f staticCU=%t loop=%t",
			*BranchSampledThresholdFlag,
			*BranchSampledLeastSqureFlag,
			staticComputeUnit != nil,
			*LoopSampledFlag)
	}

	if *KernelSampledFlag {
		kernelEngine := &KernelSampledEngine{}
		kernelEngine.SetDebugLabel(fmt.Sprintf("GPU%d.Kernel", gpuID))
		kernelEngine.Reset(true)
		kernelEngine.LoadHistoryTables()
		perGPUState.kernelSampledEngines[gpuID] = kernelEngine
		PhotonDebugf(fmt.Sprintf("GPU%d", gpuID),
			"init kernel sampled engine threshold=%d historySize=%d",
			*KernelSampledThreshold,
			kernelEngine.HistorySize())
	}

	if *SampledRunnerFlag || *BranchSampledFlag ||
		*KernelSampledFlag || *LoopSampledFlag {
		name := fmt.Sprintf("SampledTimeEngine[%d]", gpuID)
		sampledTimeEngine := NewSampledTimeEngine(name, engine, freq)
		sampledTimeEngine.SetDebugLabel(fmt.Sprintf("GPU%d.Time", gpuID))
		perGPUState.sampledTimeEngines[gpuID] = sampledTimeEngine
		PhotonDebugf(fmt.Sprintf("GPU%d", gpuID),
			"init sampled time engine name=%s", name)
	}
}

func SampledEngineForGPU(gpuID uint64) *SampledEngine {
	perGPUState.RLock()
	engine := perGPUState.sampledEngines[gpuID]
	perGPUState.RUnlock()
	if engine != nil {
		return engine
	}
	return Sampledengine
}

func BranchSampledEngineForGPU(gpuID uint64) *BranchSampledEngine {
	perGPUState.RLock()
	engine := perGPUState.branchSampledEngines[gpuID]
	perGPUState.RUnlock()
	if engine != nil {
		return engine
	}
	return Branchsampledengine
}

func KernelSampledEngineForGPU(gpuID uint64) *KernelSampledEngine {
	perGPUState.RLock()
	engine := perGPUState.kernelSampledEngines[gpuID]
	perGPUState.RUnlock()
	if engine != nil {
		return engine
	}
	return Kernelsampledengine
}

func SampledTimeEngineForGPU(gpuID uint64) *SampledTimeEngine {
	perGPUState.RLock()
	engine := perGPUState.sampledTimeEngines[gpuID]
	perGPUState.RUnlock()
	if engine != nil {
		return engine
	}
	return Sampledtimeengine
}

func ResetGPUSampledEngines(gpuID uint64) {
	if *BranchSampledFlag || *LoopSampledFlag {
		if engine := BranchSampledEngineForGPU(gpuID); engine != nil {
			PhotonDebugf(fmt.Sprintf("GPU%d", gpuID),
				"kernel launch reset branch/loop engine")
			engine.Reset()
		}
	}
	if *SampledRunnerFlag {
		if engine := SampledEngineForGPU(gpuID); engine != nil {
			PhotonDebugf(fmt.Sprintf("GPU%d", gpuID),
				"kernel launch reset wf engine")
			engine.Reset()
		}
	}
	if *KernelSampledFlag {
		if engine := KernelSampledEngineForGPU(gpuID); engine != nil {
			PhotonDebugf(fmt.Sprintf("GPU%d", gpuID),
				"kernel launch reset kernel engine")
			engine.Reset(false)
		}
	}
}

func ensurePerGPUMaps() {
	if perGPUState.sampledEngines == nil {
		perGPUState.sampledEngines = make(map[uint64]*SampledEngine)
	}
	if perGPUState.branchSampledEngines == nil {
		perGPUState.branchSampledEngines =
			make(map[uint64]*BranchSampledEngine)
	}
	if perGPUState.kernelSampledEngines == nil {
		perGPUState.kernelSampledEngines =
			make(map[uint64]*KernelSampledEngine)
	}
	if perGPUState.sampledTimeEngines == nil {
		perGPUState.sampledTimeEngines =
			make(map[uint64]*SampledTimeEngine)
	}
}
