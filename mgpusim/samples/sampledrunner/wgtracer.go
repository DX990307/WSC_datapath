package sampledrunner

import (
	//	"github.com/sarchlab/mgpusim/v3/kernels"
	//	"github.com/sarchlab/mgpusim/v3/profiler"
	"encoding/json"
	"os"
	"time"
	//    "math"
	"flag"
	"math"

	"github.com/sarchlab/akita/v3/sim"
)

//func GetWGFeatureMem(  IDX int,  IDY int,  IDZ int ) *sampled.WGFeatureMem {
//    return Wavegroup_tensor.GetWavegroupFeature( IDX , IDY , IDZ)
//}

var SampledRunnerFlag = flag.Bool("sampled", false,
	"sampled execution.")
var SampledRunnerThresholdFlag = flag.Float64("sampled-threshold", 0.03,
	"sampled execution threshold.")
var SampledRunnerWarmupFlag = flag.Int("sampled-warmup", 1024,
	"number of detailed wavefronts to ignore before fitting sampled timing.")
var SampledRunnerGranularityFlag = flag.Int("sampled-granularity", 2048,
	"number of detailed wavefronts in the long stability window.")

type WFFeature struct {
	Issuetime  sim.VTimeInSec
	Finishtime sim.VTimeInSec
}

type StableEngine struct {
	issuetime_sum        sim.VTimeInSec
	finishtime_sum       sim.VTimeInSec
	intervaltime_sum     sim.VTimeInSec
	mix_sum              sim.VTimeInSec
	issuetime_square_sum sim.VTimeInSec
	rate                 float64
	granulary            int
	Wffeatures           []WFFeature
	boundary             float64
	enableSampled        bool
	predTime             sim.VTimeInSec
}

func (stable_engine *StableEngine) Analysis() {
	stable_engine.enableSampled = false
	stable_engine.predTime = 0

	if stable_engine.granulary <= 0 ||
		len(stable_engine.Wffeatures) < stable_engine.granulary {
		return
	}

	baseIssueTime := stable_engine.Wffeatures[0].Issuetime
	var issueTimeSum sim.VTimeInSec
	var finishTimeSum sim.VTimeInSec
	var intervalTimeSum sim.VTimeInSec
	var mixSum sim.VTimeInSec
	var issueTimeSquareSum sim.VTimeInSec

	for _, wf := range stable_engine.Wffeatures {
		issueTime := wf.Issuetime - baseIssueTime
		finishTime := wf.Finishtime - baseIssueTime
		intervalTime := wf.Finishtime - wf.Issuetime

		issueTimeSum += issueTime
		finishTimeSum += finishTime
		intervalTimeSum += intervalTime
		mixSum += finishTime * issueTime
		issueTimeSquareSum += issueTime * issueTime
	}

	rateBottom := sim.VTimeInSec(stable_engine.granulary)*issueTimeSquareSum - issueTimeSum*issueTimeSum
	if rateBottom <= 0 {
		return
	}

	rateTop := sim.VTimeInSec(stable_engine.granulary)*mixSum - issueTimeSum*finishTimeSum
	rate := float64(rateTop / rateBottom)
	if math.IsNaN(rate) || math.IsInf(rate, 0) {
		return
	}
	stable_engine.rate = rate
	boundary := stable_engine.boundary
	stable_engine.predTime = intervalTimeSum / sim.VTimeInSec(stable_engine.granulary)
	if stable_engine.predTime <= 0 ||
		math.IsNaN(float64(stable_engine.predTime)) ||
		math.IsInf(float64(stable_engine.predTime), 0) {
		stable_engine.predTime = 0
		return
	}

	if rate >= (1-boundary) && rate <= (1+boundary) {
		stable_engine.enableSampled = true
		//            endtime := time.Now()
		//            duration := endtime.Sub(sampled_engine.FullSimWalltimeStart)
		//            duration_seconds := duration.Seconds()
		//            fmt.Printf("\ndetailed simulation time : %.2f predict time %.2f dataidx %d\n", duration_seconds,sampled_engine.predTime*1e9, sampled_engine.dataidx)
		//     fmt.Printf("rate %.2f\n",rate)
	} else {
		stable_engine.enableSampled = false
		//            fmt.Printf("rate %.2f\n",rate)
	}
}

func (stable_engine *StableEngine) Reset() {
	stable_engine.Wffeatures = nil
	stable_engine.issuetime_sum = 0
	stable_engine.finishtime_sum = 0
	stable_engine.intervaltime_sum = 0
	stable_engine.mix_sum = 0
	stable_engine.issuetime_square_sum = 0
	stable_engine.predTime = 0
	stable_engine.enableSampled = false
}

func (stable_engine *StableEngine) Collect(issuetime, finishtime sim.VTimeInSec) {
	wffeature := WFFeature{
		Issuetime:  issuetime,
		Finishtime: finishtime,
	}

	stable_engine.Wffeatures = append(stable_engine.Wffeatures, wffeature)
	stable_engine.issuetime_sum += issuetime
	stable_engine.finishtime_sum += finishtime
	stable_engine.mix_sum += finishtime * issuetime
	stable_engine.issuetime_square_sum += issuetime * issuetime
	stable_engine.intervaltime_sum += (finishtime - issuetime)

	if len(stable_engine.Wffeatures) == stable_engine.granulary {
		stable_engine.Analysis()
		///delete old data
		wffeature2 := stable_engine.Wffeatures[0]
		stable_engine.Wffeatures = stable_engine.Wffeatures[1:]
		issuetime = wffeature2.Issuetime
		finishtime = wffeature2.Finishtime
		stable_engine.issuetime_sum -= issuetime
		stable_engine.finishtime_sum -= finishtime
		stable_engine.mix_sum -= finishtime * issuetime
		stable_engine.issuetime_square_sum -= issuetime * issuetime
		stable_engine.intervaltime_sum -= (finishtime - issuetime)
	}
}

type SampledEngine struct {
	debugLabel           string
	predTime             sim.VTimeInSec
	enableSampled        bool
	disableEngine        bool
	Simtime              float64 `json:"simtime"`
	Walltime             float64 `json:"walltime"`
	FullSimWalltime      float64 `json:"fullsimwalltime"`
	FullSimWalltimeStart time.Time
	datanum              uint64
	dataidx              uint64
	stable_engine        *StableEngine
	short_stable_engine  *StableEngine
	predTimeSum          sim.VTimeInSec
	predTimeNum          uint64
	granulary            int
	warmup               int
}

func (sampled_engine *SampledEngine) SetDebugLabel(label string) {
	if sampled_engine == nil {
		return
	}
	sampled_engine.debugLabel = label
}

func (sampled_engine *SampledEngine) Reset() {
	sampled_engine.FullSimWalltimeStart = time.Now()
	sampled_engine.stable_engine.Reset()
	sampled_engine.short_stable_engine.Reset()
	sampled_engine.predTime = 0
	sampled_engine.predTimeNum = 0
	sampled_engine.predTimeSum = 0
	sampled_engine.dataidx = 0
	sampled_engine.enableSampled = false
	sampled_engine.disableEngine = true
	PhotonDebugf(sampled_engine.debugLabel,
		"wf engine reset disabled=%t", sampled_engine.disableEngine)
}

func ReportSampledResult(simtime, walltime float64) {
	if *SampledRunnerFlag || *BranchSampledFlag {

		Sampledengine.Simtime = simtime
		Sampledengine.Walltime = walltime
		jsonStr, _ := json.MarshalIndent(Sampledengine, "", " ")
		file, _ := os.Create("sampled_result.json")
		defer file.Close()
		file.Write(jsonStr)
	}

}

func NewSampledEngine(granulary int, boundary float64, control bool) *SampledEngine {
	return NewSampledEngineWithWarmup(granulary, boundary, control, 1024)
}

// const granulary = 512
func NewSampledEngineWithWarmup(
	granulary int,
	boundary float64,
	control bool,
	warmup int,
) *SampledEngine {
	if granulary < 2 {
		granulary = 2
	}
	if warmup < 0 {
		warmup = 0
	}
	shortGranularity := granulary / 2
	if shortGranularity < 2 {
		shortGranularity = 2
	}
	stable_engine := &StableEngine{
		granulary: granulary,
		boundary:  boundary,
	}
	short_stable_engine := &StableEngine{
		granulary: shortGranularity,
		boundary:  boundary,
	}
	ret := &SampledEngine{
		stable_engine:       stable_engine,
		short_stable_engine: short_stable_engine,
		granulary:           shortGranularity,
		warmup:              warmup,
	}
	ret.Reset()
	if control {
		ret.disableEngine = false
	}
	return ret
}

var Sampledengine *SampledEngine

func InitSampledEngine() {
	Sampledengine = NewSampledEngineWithWarmup(
		*SampledRunnerGranularityFlag,
		*SampledRunnerThresholdFlag,
		false,
		*SampledRunnerWarmupFlag)
}

func (sampled_engine *SampledEngine) Update(issuetime sim.VTimeInSec, finishtime sim.VTimeInSec) bool {
	sampled_engine.short_stable_engine.Collect(issuetime, finishtime)
	if sampled_engine.short_stable_engine.enableSampled {
		sampled_engine.predTime = sampled_engine.short_stable_engine.predTime
	}

	//    sampled_engine.predTimeSum += (finishtime - issuetime )
	//    sampled_engine.predTimeNum ++
	//    sampled_engine.predTime = sampled_engine.predTimeSum / sim.VTimeInSec( sampled_engine.predTimeNum)
	return true
}

func (sampled_engine *SampledEngine) Disabled() {
	sampled_engine.disableEngine = true
}
func (sampled_engine *SampledEngine) Enable() {
	PhotonDebugf(sampled_engine.debugLabel,
		"wf engine enabled previousDisabled=%t", sampled_engine.disableEngine)
	sampled_engine.disableEngine = false
}
func (sampled_engine *SampledEngine) IfDisable() bool {
	return sampled_engine.disableEngine
}

func (sampled_engine *SampledEngine) Collect(issuetime sim.VTimeInSec, finishtime sim.VTimeInSec) {
	if sampled_engine.enableSampled || sampled_engine.disableEngine {
		PhotonVerbosef(sampled_engine.debugLabel,
			"wf collect ignored enabled=%t disabled=%t issue=%.3fns finish=%.3fns",
			sampled_engine.enableSampled,
			sampled_engine.disableEngine,
			issuetime*1e9,
			finishtime*1e9)
		return
	}

	sampled_engine.dataidx++
	if sampled_engine.dataidx < uint64(sampled_engine.warmup) {
		PhotonVerbosef(sampled_engine.debugLabel,
			"wf collect warmup dataidx=%d issue=%.3fns finish=%.3fns",
			sampled_engine.dataidx,
			issuetime*1e9,
			finishtime*1e9)
		return
	}

	sampled_engine.stable_engine.Collect(issuetime, finishtime)
	sampled_engine.short_stable_engine.Collect(issuetime, finishtime)
	stable_engine := sampled_engine.stable_engine
	short_stable_engine := sampled_engine.short_stable_engine
	//    if stable_engine.enableSampled && short_stable_engine.enableSampled {
	if stable_engine.enableSampled {
		//        sampled_engine.enableSampled = true
		long_time := stable_engine.predTime
		short_time := short_stable_engine.predTime
		if long_time <= 0 || short_time <= 0 {
			return
		}

		sampled_engine.predTime = short_stable_engine.predTime
		diffDenominator := long_time + short_time
		if diffDenominator <= 0 {
			return
		}

		diff := float64((long_time - short_time) / diffDenominator)
		if math.IsNaN(diff) || math.IsInf(diff, 0) {
			return
		}

		diff_boundary := *SampledRunnerThresholdFlag
		if diff <= diff_boundary && diff >= -diff_boundary {
			sampled_engine.enableSampled = true
			//sampled_engine.predTime = (long_time + short_time) / 2.0;
			sampled_engine.predTime = short_time
			sampled_engine.predTimeSum = short_time * sim.VTimeInSec(sampled_engine.granulary)
			sampled_engine.predTimeNum = uint64(sampled_engine.granulary)
			PhotonDebugf(sampled_engine.debugLabel,
				"wf sampled enabled long=%.3fns short=%.3fns diff=%.6f threshold=%.6f",
				stable_engine.predTime*1e9,
				short_stable_engine.predTime*1e9,
				diff,
				diff_boundary)
			// fmt.Printf("long %.2f short %.2f relatively diff %.2f \n", stable_engine.predTime*1e9, short_stable_engine.predTime*1e9, diff)
		}

	} else if short_stable_engine.enableSampled {
		sampled_engine.predTime = stable_engine.predTime
	}
	//    else if stable_engine.enableSampled{
	//        sampled_engine.predTime = stable_engine.predTime
	//    } else if short_stable_engine.enableSampled{
	//
	//        sampled_engine.predTime = short_stable_engine.predTime
	////        fmt.Printf("long %.2f short %.2f\n",stable_engine.rate,short_stable_engine.rate)
	//    }

}

func (sampled_engine *SampledEngine) DebugPrint() {
	// fmt.Printf("%t\n", sampled_engine.enableSampled)
}
func (sampled_engine *SampledEngine) Predict() (sim.VTimeInSec, bool) {

	if sampled_engine.enableSampled {
		PhotonDebugf(sampled_engine.debugLabel,
			"wf predict skip=true pred=%.3fns",
			sampled_engine.predTime*1e9)
	} else {
		PhotonVerbosef(sampled_engine.debugLabel,
			"wf predict skip=false pred=%.3fns disabled=%t",
			sampled_engine.predTime*1e9,
			sampled_engine.disableEngine)
	}
	return sampled_engine.predTime, sampled_engine.enableSampled
}
