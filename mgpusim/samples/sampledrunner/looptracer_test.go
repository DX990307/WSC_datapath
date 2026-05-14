package sampledrunner

import (
	"testing"

	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/insts"
)

func TestLoopSampleCollectsStableBackwardBranchWithoutBranchRates(t *testing.T) {
	oldLoop := *LoopSampledFlag
	oldBranch := *BranchSampledFlag
	oldWarmup := *LoopSampledWarmupFlag
	oldMinIters := *LoopSampledMinItersFlag
	oldThreshold := *LoopSampledThresholdFlag
	defer func() {
		*LoopSampledFlag = oldLoop
		*BranchSampledFlag = oldBranch
		*LoopSampledWarmupFlag = oldWarmup
		*LoopSampledMinItersFlag = oldMinIters
		*LoopSampledThresholdFlag = oldThreshold
	}()

	*LoopSampledFlag = true
	*BranchSampledFlag = false
	*LoopSampledWarmupFlag = 1
	*LoopSampledMinItersFlag = 2
	*LoopSampledThresholdFlag = 0.01

	engine := NewBranchSampledEngine(1*sim.GHz, nil, "test")
	engine.Enable()
	engine.CollectWfStart("wf0", 0)

	branchInst := &insts.Inst{
		Format:   insts.FormatTable[insts.SOPP],
		InstType: &insts.InstType{Opcode: 9},
		PC:       0x100,
		SImm16:   insts.NewIntOperand(0, -2),
	}
	tenNS := sim.VTimeInSec(10e-9)
	engine.Collect("wf0", tenNS, branchInst, nil)
	engine.Collect("wf0", 2*tenNS, branchInst, nil)
	engine.Collect("wf0", 3*tenNS, branchInst, nil)
	engine.Collect("wf0", 4*tenNS, branchInst, nil)

	key := loopKey{BranchPC: 0x100, TargetPC: 0xfc}
	state := engine.loopSamples[key]
	if state == nil {
		t.Fatalf("expected loop sample state for %#v", key)
	}
	if !state.stable {
		t.Fatalf("expected stable loop sample state, got seen=%d window=%v", state.seen, state.window)
	}
	if state.predTime != tenNS {
		t.Fatalf("expected 10ns predicted loop interval, got %.3fns", state.predTime*1e9)
	}
}
