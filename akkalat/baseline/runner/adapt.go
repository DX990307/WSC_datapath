package runner

// ISCA'27 P21 adaptive placement: flags, wiring and statistics. The
// mechanism is in mgpusim/timing/pagemigration/adapt.go. Off by default.

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/timing/cp"
	"github.com/sarchlab/mgpusim/v3/timing/pagemigration"
)

var pmAdaptFlag = flag.Bool("pm-adapt", false,
	"P21: adaptive placement on top of -page-migration=counter (4 KB): one "+
		"benefit/cost decision per page over the action set -pm-adapt-actions.")
var pmAdaptActionsFlag = flag.String("pm-adapt-actions", "move,block,spread,predict",
	"P21: action set, comma separated subset of move, block (per 1 KB block), "+
		"spread (hot pages: ring 12 / hash), replicate (hot read-only pages, 4 "+
		"copies), predict (per-buffer rule pre-places untouched pages), cmig "+
		"(r3: L2-resident migration into the reader's L2).")
var pmAdaptCostFlag = flag.Bool("pm-adapt-costmodel", true,
	"P21: benefit/cost decision; false = fixed -pm-threshold trigger and hot "+
		"actions without the cost test (ablation).")
var pmAdaptAlphaFlag = flag.Float64("pm-adapt-alpha", 2, "P21: act when benefit > alpha x cost.")
var pmAdaptFixedFlag = flag.Float64("pm-adapt-fixed-ns", 2000,
	"P21: fixed cost of one migrated page (software, shootdown, holding).")
var pmAdaptCopyWFlag = flag.Float64("pm-adapt-copy-weight", 1,
	"P21: cost of copying one line relative to one remote read over the same hops.")
var pmAdaptQueueFlag = flag.Float64("pm-adapt-queue-ns", 0,
	"P21: queueing term per access at an owner with average load (scaled by load).")
var pmAdaptEvalFlag = flag.Int("pm-adapt-eval", 8, "P21: evaluate every this many counted accesses.")
var pmAdaptHotKFlag = flag.Int("pm-adapt-hot-k", 4,
	"P21: a page is hot when this many GPMs read it for the first time within -pm-adapt-hot-window-ns.")
var pmAdaptHotWinFlag = flag.Int("pm-adapt-hot-window-ns", 2000, "P21: first-read window.")
var pmAdaptConcFlag = flag.Float64("pm-adapt-conc", 0.3,
	"P21: a buffer is concentrated when its most common device holds this share of its pages.")
var pmAdaptSpreadOtherFlag = flag.String("pm-adapt-spread-other", "stay",
	"P21: hot pages of non-concentrated buffers: stay or hash.")
var pmAdaptPropPagesFlag = flag.Int("pm-adapt-prop-pages", 256,
	"P21: hot buffers up to this many pages propagate the hot decision to all their pages.")
var pmAdaptPropGateFlag = flag.Float64("pm-adapt-prop-gate", 0.5,
	"P21: larger buffers propagate when hot / touched pages >= this.")
var pmAdaptPredictMFlag = flag.Int("pm-adapt-predict-m", 2, "P21: consecutive hits to trust a rule.")
var pmAdaptPredictWinFlag = flag.Int("pm-adapt-predict-window", 512,
	"P21: pages pre-placed on each side of a confirmed observation.")
var pmReplicaListFlag = flag.String("pm-replica-list", "",
	"P21 candidate 1 (oracle region replication): file with lines "+
		"'<original physical page number> <tile> [<tile> ...]'; each listed "+
		"page gets zero-cost read-only copies on those tiles at its first "+
		"access, vector reads use the nearest copy, a write collapses them. "+
		"Use with -page-migration=observe.")
var pmAdaptHotFutureFlag = flag.Int("pm-adapt-hot-future-readers", 48,
	"P21: readers a hot page is expected to have (spread / replicate benefit).")
var pmAdaptReplBudgetFlag = flag.Int("pm-adapt-replica-budget-mb", 64,
	"P21: replica bytes per GPM in MB (0 = no limit); the oldest replicated page with a copy there is dropped.")
var pmAdaptBatchAmortFlag = flag.Float64("pm-adapt-batch-amort", 16,
	"P21: a migration that joins a predicted batch pays fixed-ns / this.")
var pmAdaptPredictTouchedFlag = flag.Bool("pm-adapt-predict-touched", false,
	"P21: confident buffer rules also migrate touched neighbours in the same batch.")
var pmAdaptCopyFirstFlag = flag.Bool("pm-adapt-copy-first", true,
	"P21: copy a touched page while its old frame stays mapped; only the switch "+
		"(shootdown, drain, recopy of blocks written meanwhile) holds accesses.")
var pmAdaptEarlyObsFlag = flag.Bool("pm-adapt-early-observe", true,
	"P21: a page whose first -pm-adapt-eval counted accesses have a dominant GPM "+
		"is an observation for its buffer's rule.")
var pmAdaptHoldPreFlag = flag.Bool("pm-adapt-hold-preplace", true,
	"P21: an access to a page being pre-placed waits for the copy (moved to the "+
		"front of the copy queue) instead of cancelling the pre-placement.")
var pmAdaptReplAnyFlag = flag.Bool("pm-adapt-replicate-any", true,
	"P21: hot read-only pages of buffers spread over many owners may be replicated too.")
var pmAdaptCountHotFlag = flag.Bool("pm-adapt-count-hot", true,
	"P21: a page whose counted accesses have no dominant GPM and come from at "+
		"least -pm-adapt-hot-k GPMs is hot (written shared pages).")
var pmAdaptPreCopyWFlag = flag.Float64("pm-adapt-pre-copy-weight", 0.25,
	"P21: copy cost of a pre-placement (background bulk copy) relative to -pm-adapt-copy-weight.")
var pmAdaptConstWinFlag = flag.Int("pm-adapt-predict-const-window", 2,
	"P21: pages pre-placed around an observation when the trusted rule is adjacent-same.")
var pmAdaptObsReadersFlag = flag.Bool("pm-adapt-observed-readers", true,
	"P21: replication of hot pages of non-concentrated buffers expects 2x the readers seen so far, each reading the lines readers so far read on average.")
var pmAdaptHoldCostFlag = flag.Bool("pm-adapt-hold-cost", true,
	"P21: move / block / spread also pay rate x hold^2 / 2 (hold = shootdown + recent drain at the home).")
var pmAdaptTrackCapFlag = flag.Int("pm-adapt-track-cap", 0,
	"P21 realism: tracking entries (counters, first-read signals) per GPM for the pages homed there, LRU; 0 = unlimited.")
var pmAdaptDecideNSFlag = flag.Int("pm-adapt-decide-ns", 0,
	"P21 realism: delay from a decision to its action (counts gathered at the driver).")
var pmAdaptBatchSWFlag = flag.Bool("pm-adapt-batch-sw", false,
	"P21 realism: pre-placements and replica creations go through driver batches that pay -pm-sw-latency-ns each.")
var pmAdaptLookupNSFlag = flag.Int("pm-adapt-lookup-ns", 0,
	"P21 realism: accesses to pages with run-time block homes or replicas wait this long at translation.")
var pmAdaptBlockHomeCapFlag = flag.Int("pm-adapt-blockhome-cap", 0,
	"P21 realism: 1 KB block-home entries per GPM; 0 = unlimited. When set, uniform pre-placements remap the page to a fresh frame; block falls back to move and spread is skipped when full.")
var pmAdaptWinPerHitFlag = flag.Int("pm-adapt-predict-win-per-hit", 8,
	"P21: pre-placement window (each side) in pages per consecutive hit of the trusted rule, up to -pm-adapt-predict-window (0 = always the full window).")
var pmAdaptPreMinPrecFlag = flag.Float64("pm-adapt-pre-min-precision", 0.5,
	"P21: a buffer whose pre-placement precision (hits+1)/(checks+2) is below this stops pre-placing; 0 = off.")
var pmAdaptApproxCountFlag = flag.Bool("pm-adapt-approx-count", false,
	"P21 realism: hardware counter summary (Misra-Gries: 2 GPM counters per page, 1 per 1 KB block) instead of one counter per GPM.")
var pmAdaptPreProbeFlag = flag.Int("pm-adapt-pre-probe", 16,
	"P21: a buffer keeps at most this many + -pm-adapt-pre-probe-per-hit x (checked hits) pre-placements unchecked (0 = no limit).")
var pmAdaptPreProbePerHitFlag = flag.Int("pm-adapt-pre-probe-per-hit", 32, "P21: see -pm-adapt-pre-probe.")
var pmAdaptRuleFlag = flag.String("pm-adapt-rule", "",
	"P21 round 3: \"r3\" = one break-even (ski-rental) rule for move / block / replicate / pre-placement with constants from the platform (see pagemigration/r3.go).")
var pmAdaptR3BlindFlag = flag.Bool("pm-adapt-r3-blind", false,
	"P21 round 3 experiment: targets and replica sites ignore distance (GPM with most accesses, random sites).")
var pmAdaptDumpFlag = flag.String("pm-adapt-dump", "",
	"P21 diagnostics: write one line per tracked page to this file at the end.")
var pmFirstTouchFlag = flag.Bool("pm-first-touch", false,
	"P21 baseline: the first vector access to a page nobody touched makes the "+
		"accessing GPM the home of the page (zero cost; host copies are not "+
		"accesses). Use with -page-migration=observe.")

func adaptConfigFromFlags() pagemigration.AdaptConfig {
	pagemigration.SetMeshWidth(MeshSize())
	a := pagemigration.DefaultAdaptConfig()
	a.Enabled = *pmAdaptFlag
	a.FirstTouch = *pmFirstTouchFlag
	if a.Enabled && a.FirstTouch {
		panic("-pm-adapt and -pm-first-touch are exclusive")
	}
	a.Move, a.Block, a.Spread, a.Replicate, a.Predict = false, false, false, false, false
	a.Cmig = false
	a.L2Pull = false
	a.L2Prefetch = false
	a.L2PullSlots = *l2resPullSlotsFlag
	a.L2DecideNS = uint64(*l2resDecideNSFlag)
	for _, s := range strings.Split(*pmAdaptActionsFlag, ",") {
		switch strings.TrimSpace(s) {
		case "":
		case "move":
			a.Move = true
		case "block":
			a.Block = true
		case "spread":
			a.Spread = true
		case "replicate":
			a.Replicate = true
		case "predict":
			a.Predict = true
		case "cmig":
			a.Cmig = true
		case "l2pull":
			a.L2Pull = true
		case "l2prefetch":
			a.L2Prefetch = true
		default:
			panic(fmt.Sprintf("-pm-adapt-actions: unknown action %q", s))
		}
	}
	a.CostModel = *pmAdaptCostFlag
	a.Alpha = *pmAdaptAlphaFlag
	a.FixedNS = *pmAdaptFixedFlag
	a.CopyWeight = *pmAdaptCopyWFlag
	a.QueueNS = *pmAdaptQueueFlag
	a.EvalEvery = uint32(*pmAdaptEvalFlag)
	a.HotK = *pmAdaptHotKFlag
	a.HotWindowNS = uint64(*pmAdaptHotWinFlag)
	a.ConcShare = *pmAdaptConcFlag
	a.SpreadOther = *pmAdaptSpreadOtherFlag
	if a.SpreadOther != "stay" && a.SpreadOther != "hash" {
		panic("-pm-adapt-spread-other must be stay or hash")
	}
	a.PropSmallPages = *pmAdaptPropPagesFlag
	a.PropGate = *pmAdaptPropGateFlag
	a.PredictM = *pmAdaptPredictMFlag
	a.PredictWindow = *pmAdaptPredictWinFlag
	a.HotFutureReaders = *pmAdaptHotFutureFlag
	a.ReplicaBudget = uint64(*pmAdaptReplBudgetFlag) << 20
	a.BatchAmort = *pmAdaptBatchAmortFlag
	a.PredictTouched = *pmAdaptPredictTouchedFlag
	a.CopyFirst = *pmAdaptCopyFirstFlag
	a.EarlyObserve = *pmAdaptEarlyObsFlag
	a.HoldPrePlace = *pmAdaptHoldPreFlag
	a.ReplicateAny = *pmAdaptReplAnyFlag
	a.CountHot = *pmAdaptCountHotFlag
	a.PreCopyWeight = *pmAdaptPreCopyWFlag
	a.PredictConstWindow = *pmAdaptConstWinFlag
	a.ObservedReaders = *pmAdaptObsReadersFlag
	a.HoldCost = *pmAdaptHoldCostFlag
	a.PredictWinPerHit = *pmAdaptWinPerHitFlag
	a.PreMinPrecision = *pmAdaptPreMinPrecFlag
	a.PreProbe = *pmAdaptPreProbeFlag
	a.PreProbePerHit = *pmAdaptPreProbePerHitFlag
	a.TrackCap = *pmAdaptTrackCapFlag
	a.DecideNS = uint64(*pmAdaptDecideNSFlag)
	a.BatchSW = *pmAdaptBatchSWFlag
	a.LookupNS = uint64(*pmAdaptLookupNSFlag)
	a.BlockHomeCap = *pmAdaptBlockHomeCapFlag
	a.ApproxCount = *pmAdaptApproxCountFlag
	a.Rule = *pmAdaptRuleFlag
	if a.Rule != "" && a.Rule != "r3" {
		panic("-pm-adapt-rule must be empty or r3")
	}
	a.R3Blind = *pmAdaptR3BlindFlag
	// zero-load round trip: 65.7 ns of RDMA pipelines plus, per hop, the
	// switch latency and 4.1 ns of link and router pipeline in each direction
	a.R3HopNS = 2 * (float64(*switchLatencyFlag) + 4.1)
	a.R3BaseNS = 65.7
	a.R3LinkBytesPerNS = float64(*bandwidthFlag * *networkFlitSizeFlag)
	if *flatNetworkFlag {
		a.FlatHops = -1 // the manager computes the average pair distance
	}
	if a.BatchAmort <= 0 {
		panic("-pm-adapt-batch-amort must be positive")
	}
	if *pmReplicaListFlag != "" {
		a.ReplicaList = loadReplicaTiles(*pmReplicaListFlag)
	}
	if a.EvalEvery == 0 {
		panic("-pm-adapt-eval must be positive")
	}
	return a
}

func printAdaptConfig(c pagemigration.Config) {
	a := c.Adapt
	if a.FirstTouch {
		fmt.Printf("[Config] P21 first-touch placement\n")
	}
	if !a.Enabled {
		return
	}
	fmt.Printf("[Config] P21 adapt actions=%s costmodel=%v alpha=%g fixed=%gns "+
		"copy-weight=%g queue=%gns eval=%d hot-k=%d hot-window=%dns conc=%g "+
		"spread-other=%s prop-pages=%d prop-gate=%g predict-m=%d predict-window=%d "+
		"base=%gns hop=%gns dom-share=%g max-migrations=%d hot-future=%d "+
		"replica-budget=%dMB batch-amort=%g predict-touched=%v copy-first=%v "+
		"early-observe=%v hold-preplace=%v replicate-any=%v count-hot=%v pre-copy-weight=%g const-window=%d observed-readers=%v hold-cost=%v win-per-hit=%d pre-min-precision=%g pre-probe=%d+%d/hit track-cap=%d decide=%dns batch-sw=%v lookup=%dns blockhome-cap=%d approx-count=%v rule=%q r3-base=%gns r3-hop=%gns r3-link=%gB/ns flat=%v r3-blind=%v\n",
		*pmAdaptActionsFlag, a.CostModel, a.Alpha, a.FixedNS, a.CopyWeight,
		a.QueueNS, a.EvalEvery, a.HotK, a.HotWindowNS, a.ConcShare,
		a.SpreadOther, a.PropSmallPages, a.PropGate, a.PredictM, a.PredictWindow,
		a.BaseNS, a.HopNS, c.DomShare, c.MaxMigrations, a.HotFutureReaders,
		a.ReplicaBudget>>20, a.BatchAmort, a.PredictTouched, a.CopyFirst,
		a.EarlyObserve, a.HoldPrePlace, a.ReplicateAny, a.CountHot, a.PreCopyWeight, a.PredictConstWindow, a.ObservedReaders, a.HoldCost, a.PredictWinPerHit, a.PreMinPrecision, a.PreProbe, a.PreProbePerHit, a.TrackCap, a.DecideNS, a.BatchSW, a.LookupNS, a.BlockHomeCap, a.ApproxCount, a.Rule, a.R3BaseNS, a.R3HopNS,
		a.R3LinkBytesPerNS, a.FlatHops != 0, a.R3Blind)
}

// wireAdaptive installs what the adaptive placement needs beyond the page
// migration: copy engines that read any address from their own L2 slices
// (blocks homed away from their address range), and the kernel-boundary
// notification (replica collapse).
func (b *R9NanoPlatformBuilder) wireAdaptive(m *pagemigration.Manager) {
	a := b.pageMigration.Adapt
	if a.ReplicaList != nil {
		m.Storage = b.globalStorage
		tileDev := map[uint64]uint64{}
		for _, gpu := range b.gpus {
			tileDev[uint64(pgclsTile(gpu.pm.rdma.Name()))] = gpu.GPUID
		}
		for ppn, tiles := range a.ReplicaList {
			for i, t := range tiles {
				d, ok := tileDev[t]
				if !ok {
					panic(fmt.Sprintf("-pm-replica-list: tile %d is not a GPM", t))
				}
				tiles[i] = d
			}
			a.ReplicaList[ppn] = tiles
		}
		fmt.Printf("[Config] P21 replica list: %d pages\n", len(a.ReplicaList))
	}
	if !a.Enabled && a.ReplicaList == nil {
		return
	}
	for _, gpu := range b.gpus {
		if e := gpu.pm.engine; e != nil && a.Enabled {
			f, ok := e.L2Finder.(*mem.InterleavedLowModuleFinder)
			if !ok {
				panic("P21: unexpected copy engine L2 finder")
			}
			local := *f
			local.UseAddressSpaceLimitation = false
			local.ModuleForOtherAddresses = nil
			e.L2Finder = &local
		}
		c, ok := gpu.CommandProcessor.(*cp.CommandProcessor)
		if !ok {
			panic("P21: command processor type")
		}
		c.KernelLaunchHooks = append(c.KernelLaunchHooks,
			func(now sim.VTimeInSec) { m.KernelBoundary(now) })
	}
}

func (r *Runner) reportAdaptStats(m *pagemigration.Manager) {
	a := m.Config().Adapt
	if !a.Enabled && !a.FirstTouch && a.ReplicaList == nil {
		return
	}
	s := m.AStats
	col := func(what string, v float64) {
		r.metricsCollector.Collect("PageMigration", what, v)
	}
	col("pa_evaluations", float64(s.Evaluations))
	buckets := []string{"r1", "r2_3", "r4_15", "r16p"}
	for act, name := range pagemigration.ActionNames {
		var tot uint64
		for b, n := range s.Decided[act] {
			tot += n
			if n > 0 {
				col(fmt.Sprintf("pa_decided_%s_%s", name, buckets[b]), float64(n))
			}
		}
		col("pa_decided_"+name, float64(tot))
		col("pa_blocks_moved_"+name, float64(s.BlocksMoved[act]))
	}
	col("pa_hot_detected", float64(s.HotDetected))
	col("pa_hot_buffers", float64(s.HotBuffers))
	col("pa_propagated", float64(s.Propagated))
	col("pa_preplaced", float64(s.PrePlaced))
	col("pa_pre_blocks", float64(s.PreBlocks))
	col("pa_pre_raced", float64(s.PreRaced))
	col("pa_pre_by_predict", float64(s.PreByPredict))
	col("pa_pre_by_hot", float64(s.PreByHot))
	col("pa_predict_obs", float64(s.PredictObs))
	col("pa_predict_hits", float64(s.PredictHits))
	col("pa_predict_confident", float64(s.PredictConf))
	col("pa_predict_miss", float64(s.PredictMiss))
	col("pa_pre_fallback", float64(s.PreFallback))
	col("pa_replica_on", float64(s.ReplicaOn))
	col("pa_replica_abort", float64(s.ReplicaAbort))
	col("pa_replica_collapse_write", float64(s.ReplicaWrite))
	col("pa_replica_collapse_kernel", float64(s.ReplicaKern))
	col("pa_replica_reads", float64(s.ReplicaReads))
	col("pa_first_touch_pages", float64(s.FirstTouch))
	col("pa_copy_bytes", float64(s.CopyBytes))
	col("pa_replica_copy_bytes", float64(s.ReplCopy))
	col("pa_replica_inval_msgs", float64(s.ReplInvals))
	col("pa_replica_evictions", float64(s.ReplEvict))
	col("pa_replica_peak_bytes", float64(s.ReplPeak))
	col("pa_batch_triggered", float64(s.BatchTrig))
	col("pa_kernels", float64(s.Kernels))
	col("pa_precopies", float64(s.PreCopies))
	col("pa_recopies", float64(s.Recopies))
	col("pa_recopy_blocks", float64(s.RecopyBlocks))
	col("pa_pre_held", float64(s.PreHeld))
	col("pa_pre_held_pages", float64(s.PreHeldPages))
	col("pa_pre_cost_skip", float64(s.PreCostSkip))
	col("pa_pre_touched", float64(s.PreTouched))
	col("pa_pre_home_ok", float64(s.PreHomeOK))
	col("pa_early_obs", float64(s.EarlyObs))
	col("pa_count_hot", float64(s.CountHotN))
	col("pa_track_evict", float64(s.TrackEvict))
	col("pa_cmig_pages", float64(s.CmigPages))
	col("pa_cmig_lines", float64(s.CmigLines))
	col("pa_cmig_dropped_lines", float64(s.CmigDropped))
	col("pa_cmig_aborts", float64(s.CmigAborts))
	col("pa_cmig_holder_hits", float64(s.CmigHolderHits))
	col("pa_cmig_invals", float64(s.CmigInvals))
	col("pa_cmig_upgrades", float64(s.CmigUpgrades))
	col("pa_pre_checks", float64(s.PreChecks))
	col("pa_pre_check_hits", float64(s.PreCheckHits))
	col("pa_pre_prec_skip", float64(s.PrePrecSkip))
	col("pa_pre_budget_skip", float64(s.PreBudgetSkip))
	col("pa_decision_msgs", float64(s.DecisionMsgs))
	col("pa_deferred", float64(s.Deferred))
	col("pa_drv_batches", float64(s.DrvBatches))
	col("pa_pre_late", float64(s.PreLate))
	col("pa_pre_remaps", float64(s.PreRemaps))
	col("pa_lookup_holds", float64(s.LookupHolds))
	col("pa_bh_full", float64(s.BhFull))
	col("pa_bh_peak", float64(s.BhPeak))
	if *pmAdaptDumpFlag != "" && a.Enabled {
		f, err := os.Create(*pmAdaptDumpFlag)
		if err != nil {
			panic(err)
		}
		m.DumpAdapt(f)
		f.Close()
	}
	col("pa_block_homes", float64(m.BlockHomes()))
}

// loadReplicaTiles reads the replica list (tiles; converted to device IDs
// in wireAdaptive).
func loadReplicaTiles(path string) map[uint64][]uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	out := map[uint64][]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		ppn, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			panic(fmt.Sprintf("-pm-replica-list: bad line %q", line))
		}
		for _, x := range f[1:] {
			t, err := strconv.ParseUint(x, 10, 64)
			if err != nil {
				panic(fmt.Sprintf("-pm-replica-list: bad line %q", line))
			}
			out[ppn] = append(out[ppn], t)
		}
	}
	return out
}
