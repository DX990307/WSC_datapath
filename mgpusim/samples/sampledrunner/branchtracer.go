package sampledrunner

import (
	//    "encoding/json"
	//    "os"
	"flag"
	"math"
	//	"github.com/sarchlab/akita/v3/mem/vm"

	"github.com/sarchlab/akita/v3/sim"
	//	"github.com/sarchlab/mgpusim/v3/utils"

	//	"github.com/sarchlab/mgpusim/v3/samples/sampledrunner"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/profiler"
	"github.com/sarchlab/mgpusim/v3/utils"
)

var BranchSampledFlag = flag.Bool("branch-sampled", false,
	"Branch sampled machanism.")
var BranchSampledThresholdFlag = flag.Float64("branch-sampled-coverage-threshold", 0.95,
	"Branch sampled machanism coverage threshold.")
var BranchSampledLeastSqureFlag = flag.Float64("branch-sampled-threshold", 0.01,
	"Branch sampled machanism threshold.")
var LoopSampledFlag = flag.Bool("loop-sampled", false,
	"Loop-level sampled timing prototype.")
var LoopSampledWarmupFlag = flag.Int("loop-sampled-warmup", 8,
	"number of loop iterations to observe before testing stability.")
var LoopSampledMinItersFlag = flag.Int("loop-sampled-min-iters", 16,
	"number of loop iterations in the stability window.")
var LoopSampledThresholdFlag = flag.Float64("loop-sampled-threshold", 0.03,
	"maximum relative loop-iteration spread for stable loop detection.")

type StaticComputeUnit interface {
	GetBBLInsts(bbl profiler.BBL) []*insts.Inst
}

type WfBranchFeature struct {
	wfStartTime         sim.VTimeInSec
	wfStartInterval     sim.VTimeInSec
	startTime           sim.VTimeInSec
	startIns            uint64
	currentIns          uint64
	PC                  uint64
	StartPC             uint64
	last_inst_is_branch bool
	bbl_seq             []profiler.BBL
	lastBBLFinishTime   sim.VTimeInSec
	predict_bb_idx      int
	lastLoopBackedge    map[loopKey]sim.VTimeInSec
	loopBackedgeCounts  map[loopKey]int
}

type loopKey struct {
	BranchPC uint64
	TargetPC uint64
}

type loopSampleState struct {
	warmup       int
	windowLen    int
	threshold    float64
	seen         int
	window       []sim.VTimeInSec
	stable       bool
	predTime     sim.VTimeInSec
	tripCounts   []int
	avgTripCount float64
}

type BranchSampledEngine struct {
	debugLabel            string
	Freq                  sim.Freq
	static_compute_unit   StaticComputeUnit
	bbModel               *BBModel
	bbv2sampledengine_map map[profiler.BBL]*SampledEngine
	bbv2bbmodeltime_map   map[profiler.BBL]sim.VTimeInSec
	bbl2rate              map[profiler.BBL]float64
	wfcount_map           map[string]*WfBranchFeature
	bbv_counts            map[profiler.BBL]uint64
	insnums               uint64
	insnums_enablesampled uint64
	enableSampled         bool
	disableEngine         bool
	loopSamples           map[loopKey]*loopSampleState
	//endtimesum sim.VTimeInSec
	//  begintimesum sim.VTimeInSec
	//    begintimenum uint64
	//endtimenum uint64
	finish_rate         float64
	last_inst_is_branch bool
}

func (sampled_engine *BranchSampledEngine) SetStaticComputeUnit(staticcomputeunit StaticComputeUnit) {
	if sampled_engine == nil {
		return
	}
	sampled_engine.static_compute_unit = staticcomputeunit
	PhotonDebugf(sampled_engine.debugLabel,
		"branch static compute unit set ok=%t", staticcomputeunit != nil)
}

func (sampled_engine *BranchSampledEngine) Reset() {
	if !*BranchSampledFlag && !*LoopSampledFlag {
		return
	}
	// fmt.Printf("branch engine reset\n")
	sampled_engine.bbv2sampledengine_map = make(map[profiler.BBL]*SampledEngine)
	sampled_engine.bbv2bbmodeltime_map = make(map[profiler.BBL]sim.VTimeInSec)
	sampled_engine.wfcount_map = make(map[string]*WfBranchFeature)
	sampled_engine.bbv_counts = make(map[profiler.BBL]uint64)
	sampled_engine.bbl2rate = make(map[profiler.BBL]float64)
	sampled_engine.loopSamples = make(map[loopKey]*loopSampleState)
	sampled_engine.enableSampled = false
	//  sampled_engine.  enableSampled : true,
	sampled_engine.insnums = 0
	sampled_engine.insnums_enablesampled = 0
	//sampled_engine.endtimesum = sim.VTimeInSec(0)
	//    sampled_engine.begintimesum = sim.VTimeInSec(0)
	//  sampled_engine.begintimenum = 0
	//    sampled_engine.endtimenum = 0
	sampled_engine.finish_rate = 0
	sampled_engine.last_inst_is_branch = false
	sampled_engine.disableEngine = true
	PhotonDebugf(sampled_engine.debugLabel,
		"branch engine reset disabled=%t", sampled_engine.disableEngine)
}

var Branchsampledengine *BranchSampledEngine

func NewBranchSampledEngine(
	freq sim.Freq,
	staticComputeUnit StaticComputeUnit,
	debugLabel string,
) *BranchSampledEngine {
	engine := &BranchSampledEngine{
		debugLabel:          debugLabel,
		Freq:                freq,
		static_compute_unit: staticComputeUnit,
		bbModel:             NewBBModel(freq),
	}
	engine.Reset()
	return engine
}

func InitBranchSampledFeature(freq sim.Freq) {
	if *BranchSampledFlag || *LoopSampledFlag {
		Branchsampledengine = NewBranchSampledEngine(freq, nil, "global.Branch")
		InitUniqBBModel(freq)
	}
}

func (br_engine *BranchSampledEngine) EnableSampled() bool {
	if br_engine.enableSampled {
		PhotonDebugf(br_engine.debugLabel,
			"branch sampled enabled")
	} else {
		PhotonVerbosef(br_engine.debugLabel,
			"branch sampled not enabled disabled=%t finishRate=%.6f",
			br_engine.disableEngine,
			br_engine.finish_rate)
	}
	return br_engine.enableSampled
}
func (br_engine *BranchSampledEngine) Predict(wfid string, bbv profiler.BBL) sim.VTimeInSec {
	if br_engine.enableSampled {
		PhotonDebugf(br_engine.debugLabel,
			"branch predict wfid=%s bblPC=%d bblIns=%d",
			wfid, bbv.PC, bbv.InsNum)

		wffeature, found := br_engine.wfcount_map[wfid]
		if found {
			wffeature.predict_bb_idx++
			//            fmt.Printf("  %d\n", )
			if wffeature.predict_bb_idx == len(wffeature.bbl_seq) {

				interval := wffeature.lastBBLFinishTime - wffeature.wfStartTime
				interval -= br_engine.StartTime()
				delete(br_engine.wfcount_map, wfid)
				PhotonDebugf(br_engine.debugLabel,
					"branch predict from previous wf sequence wfid=%s pred=%.3fns",
					wfid,
					interval*1e9)
				return interval

			} else if wffeature.predict_bb_idx < len(wffeature.bbl_seq) {
				return sim.VTimeInSec(0)
			}
		}
		//        for idx, elem := range br_engine.bbv2sampledengine_map{
		//            predicttime,_ := elem.Predict()
		//            fmt.Printf("%d %d %.2f\n",idx.PC,idx.InsNum,predicttime*1e9)
		//        }
		sampledmap, found := br_engine.bbv2sampledengine_map[bbv]
		if found {
			predicttime, predfound := sampledmap.Predict()
			if predfound {
				//          fmt.Printf("solved %d %d\n",bbv.PC,bbv.InsNum)
				PhotonDebugf(br_engine.debugLabel,
					"branch predict from sampled map pred=%.3fns",
					predicttime*1e9)
				return predicttime
			}
		}
		//            fmt.Printf("unsolved ")

		intervaltime, found2 := br_engine.bbv2bbmodeltime_map[bbv]
		if found2 {
			PhotonDebugf(br_engine.debugLabel,
				"branch predict from cached bb model pred=%.3fns",
				intervaltime*1e9)
			return intervaltime
		} else {
			bbModel := br_engine.bbModel
			if bbModel == nil {
				bbModel = Global_bbmode
			}
			if br_engine.static_compute_unit == nil {
				PhotonVerbosef(br_engine.debugLabel,
					"branch predict skipped bblPC=%d bblIns=%d no static compute unit",
					bbv.PC,
					bbv.InsNum)
				return sim.VTimeInSec(0)
			}
			bblInsts := getBBLInstsOrNil(
				br_engine.static_compute_unit, bbv, br_engine.debugLabel)
			if len(bblInsts) == 0 {
				PhotonVerbosef(br_engine.debugLabel,
					"branch predict skipped bblPC=%d bblIns=%d missing static bbl",
					bbv.PC,
					bbv.InsNum)
				return sim.VTimeInSec(0)
			}
			intervaltime = bbModel.IntervalModel(bblInsts)
			br_engine.bbv2bbmodeltime_map[bbv] = intervaltime
			PhotonDebugf(br_engine.debugLabel,
				"branch predict from bb model pred=%.3fns",
				intervaltime*1e9)
		}

		//          fmt.Printf("unsolved %d %d %.2f\n",bbv.PC,bbv.InsNum,intervaltime)

		return intervaltime
	} else {
		PhotonVerbosef(br_engine.debugLabel,
			"branch predict skipped because engine disabled")
		return sim.VTimeInSec(0)
	}
}

func getBBLInstsOrNil(
	staticComputeUnit StaticComputeUnit,
	bbv profiler.BBL,
	debugLabel string,
) (insts []*insts.Inst) {
	defer func() {
		if recovered := recover(); recovered != nil {
			PhotonVerbosef(debugLabel,
				"static bbl lookup failed bblPC=%d bblIns=%d err=%v",
				bbv.PC,
				bbv.InsNum,
				recovered)
			insts = nil
		}
	}()
	return staticComputeUnit.GetBBLInsts(bbv)
}

func (br_engine *BranchSampledEngine) Print() {
	// fmt.Printf("Hello")
	// log.Printf("Hello")
}

func (br_engine *BranchSampledEngine) CollectWfStart(wfid string, now sim.VTimeInSec) {
	if !*BranchSampledFlag && !*LoopSampledFlag {
		return
	}
	if br_engine.enableSampled {
		return
	}
	wf_branch_feature := &WfBranchFeature{
		wfStartTime:         now,
		startTime:           now,
		startIns:            0,
		currentIns:          0,
		last_inst_is_branch: false,
		predict_bb_idx:      0,
		lastLoopBackedge:    make(map[loopKey]sim.VTimeInSec),
		loopBackedgeCounts:  make(map[loopKey]int),
	}
	//panic(inst.PC)
	//   fmt.Printf("collect begin wfid %s\n",wfid)
	br_engine.wfcount_map[wfid] = wf_branch_feature
	PhotonVerbosef(br_engine.debugLabel,
		"branch collect wf start wfid=%s time=%.3fns",
		wfid,
		now*1e9)
}

func (br_engine *BranchSampledEngine) CollectWfEnd(wfid string, now sim.VTimeInSec) {
	if !*BranchSampledFlag && !*LoopSampledFlag {
		return
	}
	if br_engine.enableSampled && !*LoopSampledFlag {
		return
	}
	wf_branch_feature, found := br_engine.wfcount_map[wfid]
	if found {
		if *LoopSampledFlag {
			br_engine.collectLoopTripCounts(wf_branch_feature)
		}
		//        endtime := now - wf_branch_feature.wfStartTime
		//    fmt.Printf("wfid %s endtime %f %f %f\n",wfid,endtime * 1e9, now*1e9,wf_branch_feature.startTime*1e9)
		//        br_engine.endtimesum += endtime
		//        br_engine.endtimenum++
		delete(br_engine.wfcount_map, wfid)
		PhotonVerbosef(br_engine.debugLabel,
			"branch collect wf end wfid=%s time=%.3fns",
			wfid,
			now*1e9)
	}
}
func (br_engine *BranchSampledEngine) EndTime() sim.VTimeInSec {

	return br_engine.Freq.NextTick(sim.VTimeInSec(0))
	//	return sim.VTimeInSec(0))
	//
	//	if br_engine.endtimenum == 0 {
	//	   return sim.VTimeInSec(0)
	//	} else {
	//
	//	   return br_engine.endtimesum / sim.VTimeInSec(br_engine.endtimenum)
	//	}
}

func (br_engine *BranchSampledEngine) StartTime() sim.VTimeInSec {
	return br_engine.Freq.NextTick(sim.VTimeInSec(0))
	//br_engine.begintimesum / sim.VTimeInSec(br_engine.begintimenum)
}

func (br_engine *BranchSampledEngine) Analysis(bbls []*profiler.OnlineBbv) {
	var sum uint64
	sum = 0
	bbl2count := make(map[profiler.BBL]uint64)
	for _, onlinebbl := range bbls {
		for bbl, count := range *onlinebbl.Bbv_count() {
			num := count * bbl.InsNum
			bbl2count[bbl] += num
			sum += num
		}
	}
	if sum == 0 {
		PhotonDebugf(br_engine.debugLabel,
			"branch analysis skipped empty instruction coverage")
		return
	}
	for bbl, count := range bbl2count {
		br_engine.bbl2rate[bbl] = float64(count) / float64(sum)
	}
	for bbl, rate := range br_engine.bbl2rate {
		_ = bbl
		_ = rate
		// bbl.Print()
		// fmt.Printf(" %.2f\n", rate)
	}
}

func (br_engine *BranchSampledEngine) Flush(wf_branch_feature *WfBranchFeature, now sim.VTimeInSec, PC uint64) {
	inscount := wf_branch_feature.currentIns - wf_branch_feature.startIns
	bbl := profiler.BBL{
		PC:     wf_branch_feature.PC - wf_branch_feature.StartPC,
		InsNum: inscount,
	}
	branchsampled_engine, found := br_engine.bbv2sampledengine_map[bbl]
	issuetime := wf_branch_feature.startTime
	finishtime := now

	/////////update wavefront information
	wf_branch_feature.startIns = wf_branch_feature.currentIns
	wf_branch_feature.PC = PC
	wf_branch_feature.startTime = now
	///////////
	wf_branch_feature.lastBBLFinishTime = finishtime
	wf_branch_feature.bbl_seq = append(wf_branch_feature.bbl_seq, bbl)
	if found {
		PhotonVerbosef(br_engine.debugLabel,
			"branch flush existing bblPC=%d bblIns=%d issue=%.3fns finish=%.3fns",
			bbl.PC,
			bbl.InsNum,
			issuetime*1e9,
			finishtime*1e9)
		if branchsampled_engine.enableSampled {
			//updated :=
			branchsampled_engine.Update(issuetime, finishtime)
			//                if updated{
			//                    bbl.Print()
			//                    fmt.Printf("updated pred time %.2f\n", branchsampled_engine.predTime*1e9)
			//                }
			return
		} else {

			branchsampled_engine.Collect(issuetime, finishtime)
		}

	} else {
		branchsampled_engine = NewSampledEngine(4096, *BranchSampledLeastSqureFlag, true)
		branchsampled_engine.SetDebugLabel(br_engine.debugLabel + ".BBL")
		//            branchsampled_engine.Reset()
		branchsampled_engine.Collect(issuetime, finishtime)
		br_engine.bbv2sampledengine_map[bbl] = branchsampled_engine
		PhotonDebugf(br_engine.debugLabel,
			"branch create bbl sampled engine bblPC=%d bblIns=%d",
			bbl.PC,
			bbl.InsNum)
	}
	current_enable_sampled := branchsampled_engine.enableSampled
	if current_enable_sampled {
		br_engine.Update(bbl)
		// bbl.Print()
		// fmt.Printf(" pred time %.2f\n", branchsampled_engine.predTime*1e9)

	}

}
func (sampled_engine *BranchSampledEngine) Disabled() {
	sampled_engine.disableEngine = true
}
func (sampled_engine *BranchSampledEngine) Enable() {
	PhotonDebugf(sampled_engine.debugLabel,
		"branch engine enabled previousDisabled=%t",
		sampled_engine.disableEngine)
	sampled_engine.disableEngine = false
}
func (sampled_engine *BranchSampledEngine) IfDisable() bool {
	return sampled_engine.disableEngine
}

func (br_engine *BranchSampledEngine) Collect(wfid string, now sim.VTimeInSec, inst *insts.Inst, state utils.InstEmuState) {
	if !*BranchSampledFlag && !*LoopSampledFlag {
		return
	}
	if br_engine.enableSampled || br_engine.disableEngine {
		PhotonVerbosef(br_engine.debugLabel,
			"branch collect inst ignored wfid=%s enabled=%t disabled=%t",
			wfid,
			br_engine.enableSampled,
			br_engine.disableEngine)
		return
	}
	wf_branch_feature, found := br_engine.wfcount_map[wfid]
	if !found || wf_branch_feature == nil {
		PhotonVerbosef(br_engine.debugLabel,
			"branch collect inst ignored wfid=%s no wf feature", wfid)
		return
	}

	br_engine.collectLoopSample(wf_branch_feature, now, inst)

	if !*BranchSampledFlag {
		return
	}
	if len(br_engine.bbl2rate) == 0 {
		PhotonVerbosef(br_engine.debugLabel,
			"branch collect inst ignored wfid=%s no bbl rates",
			wfid)
		return
	}
	inswidth := uint64(inst.InstWidth())
	if wf_branch_feature.currentIns == 0 {
		wf_branch_feature.PC = inst.PC
		wf_branch_feature.StartPC = inst.PC
		wf_branch_feature.wfStartInterval = now - wf_branch_feature.wfStartTime
		//br_engine.begintimesum += wf_branch_feature.wfStartTime
		//        br_engine.begintimenum++
		wf_branch_feature.currentIns = inswidth
		wf_branch_feature.startIns = inswidth

	} else {
		wf_branch_feature.currentIns += inswidth
	}

	if wf_branch_feature.last_inst_is_branch {
		br_engine.Flush(wf_branch_feature, now, inst.PC)
		wf_branch_feature.last_inst_is_branch = false
	}
	flush_data := false
	if inst.FormatType == insts.SOPP {
		switch inst.Opcode {
		case 2, 4, 5, 6, 7, 8, 9, 10: // S_CBRANCH_SCC0
			wf_branch_feature.last_inst_is_branch = true
		case 1:
			flush_data = true
			wf_branch_feature.currentIns += inswidth
		default:
		}
	}
	if flush_data {

		br_engine.Flush(wf_branch_feature, now, inst.PC)
	}
}

func (br_engine *BranchSampledEngine) collectLoopSample(
	wfBranchFeature *WfBranchFeature,
	now sim.VTimeInSec,
	inst *insts.Inst,
) {
	if !*LoopSampledFlag || !isLoopSampleCandidate(inst) {
		return
	}

	target, ok := soppBranchTarget(inst)
	if !ok || target >= inst.PC {
		return
	}

	key := loopKey{BranchPC: inst.PC, TargetPC: target}
	state := br_engine.loopSamples[key]
	if state == nil {
		state = newLoopSampleState(
			*LoopSampledWarmupFlag,
			*LoopSampledMinItersFlag,
			*LoopSampledThresholdFlag)
		br_engine.loopSamples[key] = state
		PhotonDebugf(br_engine.debugLabel,
			"loop candidate branchPC=%#x targetPC=%#x",
			key.BranchPC,
			key.TargetPC)
	}

	if wfBranchFeature.lastLoopBackedge == nil {
		wfBranchFeature.lastLoopBackedge = make(map[loopKey]sim.VTimeInSec)
	}
	if wfBranchFeature.loopBackedgeCounts == nil {
		wfBranchFeature.loopBackedgeCounts = make(map[loopKey]int)
	}
	wfBranchFeature.loopBackedgeCounts[key]++
	last, found := wfBranchFeature.lastLoopBackedge[key]
	wfBranchFeature.lastLoopBackedge[key] = now
	if !found || now <= last {
		return
	}

	interval := now - last
	wasStable := state.stable
	if state.collect(interval) && !wasStable {
		PhotonDebugf(br_engine.debugLabel,
			"loop sampled stable branchPC=%#x targetPC=%#x iters=%d pred=%.3fns threshold=%.6f",
			key.BranchPC,
			key.TargetPC,
			state.seen,
			state.predTime*1e9,
			state.threshold)
	}
}

func (br_engine *BranchSampledEngine) collectLoopTripCounts(
	wfBranchFeature *WfBranchFeature,
) {
	for key, count := range wfBranchFeature.loopBackedgeCounts {
		state := br_engine.loopSamples[key]
		if state == nil || !state.stable {
			continue
		}

		state.collectTripCount(count)
		if state.avgTripCount <= 0 {
			continue
		}

		if !br_engine.enableSampled {
			PhotonDebugf(br_engine.debugLabel,
				"loop-level sampled start branchPC=%#x targetPC=%#x avgTripCount=%.2f predIter=%.3fns",
				key.BranchPC,
				key.TargetPC,
				state.avgTripCount,
				state.predTime*1e9)
		}
		br_engine.enableSampled = true
	}
}

// PredictStableLoop returns the predicted time for a previously observed stable
// loop backedge. It intentionally only enables fast-forward for conditional
// backedges. Unconditional backward branches usually rely on an earlier exit
// branch, so jumping to their fall-through would be too aggressive.
func (br_engine *BranchSampledEngine) PredictStableLoop(
	inst *insts.Inst,
	completedIters int,
) (
	predTime sim.VTimeInSec,
	target uint64,
	fallthroughPC uint64,
	skippedIters int,
	ok bool,
) {
	if br_engine == nil ||
		!*LoopSampledFlag ||
		!isConditionalLoopSampleCandidate(inst) {
		return 0, 0, 0, 0, false
	}

	target, ok = soppBranchTarget(inst)
	if !ok || target >= inst.PC {
		return 0, 0, 0, 0, false
	}

	state := br_engine.loopSamples[loopKey{
		BranchPC: inst.PC,
		TargetPC: target,
	}]
	if state == nil || !state.stable || state.predTime <= 0 ||
		state.avgTripCount <= 0 {
		return 0, 0, 0, 0, false
	}

	skippedIters = state.remainingIters(completedIters)
	if skippedIters <= 0 {
		return 0, 0, 0, 0, false
	}

	return state.predTime * sim.VTimeInSec(skippedIters),
		target,
		soppFallthroughPC(inst),
		skippedIters,
		true
}

func isLoopSampleCandidate(inst *insts.Inst) bool {
	return inst != nil &&
		inst.FormatType == insts.SOPP &&
		inst.SImm16 != nil &&
		inst.Opcode >= 2 &&
		inst.Opcode <= 9
}

func isConditionalLoopSampleCandidate(inst *insts.Inst) bool {
	return isLoopSampleCandidate(inst) && inst.Opcode != 2
}

func soppBranchTarget(inst *insts.Inst) (uint64, bool) {
	if inst == nil || inst.SImm16 == nil {
		return 0, false
	}
	imm := int16(uint16(inst.SImm16.IntValue))
	target := int64(soppFallthroughPC(inst)) + int64(imm)*4
	if target < 0 {
		return 0, false
	}
	return uint64(target), true
}

func soppFallthroughPC(inst *insts.Inst) uint64 {
	byteSize := inst.ByteSize
	if byteSize <= 0 {
		byteSize = 4
	}
	return inst.PC + uint64(byteSize)
}

func newLoopSampleState(
	warmup int,
	windowLen int,
	threshold float64,
) *loopSampleState {
	if warmup < 0 {
		warmup = 0
	}
	if windowLen < 2 {
		windowLen = 2
	}
	if threshold <= 0 {
		threshold = 0.03
	}
	return &loopSampleState{
		warmup:    warmup,
		windowLen: windowLen,
		threshold: threshold,
		window:    make([]sim.VTimeInSec, 0, windowLen),
	}
}

func (state *loopSampleState) collect(interval sim.VTimeInSec) bool {
	state.seen++
	if state.seen <= state.warmup || interval <= 0 {
		return state.stable
	}

	state.window = append(state.window, interval)
	if len(state.window) > state.windowLen {
		copy(state.window, state.window[1:])
		state.window = state.window[:state.windowLen]
	}
	if len(state.window) < state.windowLen {
		return state.stable
	}

	minTime := state.window[0]
	maxTime := state.window[0]
	var sum sim.VTimeInSec
	for _, sample := range state.window {
		if sample < minTime {
			minTime = sample
		}
		if sample > maxTime {
			maxTime = sample
		}
		sum += sample
	}

	avg := sum / sim.VTimeInSec(len(state.window))
	if avg <= 0 {
		return state.stable
	}

	spread := float64((maxTime - minTime) / avg)
	if spread <= state.threshold {
		state.stable = true
		state.predTime = avg
	}

	return state.stable
}

func (state *loopSampleState) collectTripCount(count int) {
	if count <= 0 {
		return
	}

	state.tripCounts = append(state.tripCounts, count)
	if len(state.tripCounts) > state.windowLen {
		copy(state.tripCounts, state.tripCounts[1:])
		state.tripCounts = state.tripCounts[:state.windowLen]
	}

	minCount := state.tripCounts[0]
	maxCount := state.tripCounts[0]
	sum := 0
	for _, sample := range state.tripCounts {
		if sample < minCount {
			minCount = sample
		}
		if sample > maxCount {
			maxCount = sample
		}
		sum += sample
	}

	avg := float64(sum) / float64(len(state.tripCounts))
	if avg <= 0 {
		return
	}

	spread := float64(maxCount-minCount) / avg
	if len(state.tripCounts) == 1 || spread <= state.threshold {
		state.avgTripCount = avg
	}
}

func (state *loopSampleState) remainingIters(completedIters int) int {
	tripCount := int(math.Round(state.avgTripCount))
	remaining := tripCount - completedIters
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (br_engine *BranchSampledEngine) Update(bbl profiler.BBL) {
	rate, found := br_engine.bbl2rate[bbl]
	if found {
		br_engine.finish_rate += rate
		if br_engine.finish_rate >= *BranchSampledThresholdFlag {
			br_engine.enableSampled = true
			PhotonDebugf(br_engine.debugLabel,
				"branch-level sampled start bblPC=%d bblIns=%d finishRate=%.6f threshold=%.6f",
				bbl.PC,
				bbl.InsNum,
				br_engine.finish_rate,
				*BranchSampledThresholdFlag)
			// fmt.Printf("branch-level sampled start\n")
		} else {
			PhotonDebugf(br_engine.debugLabel,
				"branch bbl solved bblPC=%d bblIns=%d finishRate=%.6f threshold=%.6f",
				bbl.PC,
				bbl.InsNum,
				br_engine.finish_rate,
				*BranchSampledThresholdFlag)
			// fmt.Printf("rate %.2f\n", br_engine.finish_rate)
		}
	}
}
