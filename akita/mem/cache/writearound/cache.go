package writearound

import (
	"strings"

	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// M1DirectBypassPredictor predicts whether an L2 cache line is absent.
type M1DirectBypassPredictor interface {
	PredictM1DirectDramBypass(pid vm.PID, cacheLineID uint64) bool
}

// M1DirectBypassTarget describes the L2-side state needed by the L1V direct
// DRAM bypass path.
type M1DirectBypassTarget struct {
	Predictor     M1DirectBypassPredictor
	CleanFillPort sim.Port
}

// A Cache is a customized L1 cache the for R9nano GPUs.
type Cache struct {
	*sim.TickingComponent

	topPort        sim.Port
	bottomPort     sim.Port
	directDramPort sim.Port
	controlPort    sim.Port

	numReqPerCycle   int
	log2BlockSize    uint64
	storage          *mem.Storage
	directory        cache.Directory
	mshr             cache.MSHR
	dirLatency       int
	bankLatency      int
	wayAssociativity int
	lowModuleFinder  mem.LowModuleFinder

	dirBuf   sim.Buffer
	bankBufs []sim.Buffer

	coalesceStage    *coalescer
	directoryStage   *directory
	bankStages       []*bankStage
	parseBottomStage *bottomParser
	respondStage     *respondStage
	controlStage     *controlStage

	maxNumConcurrentTrans    int
	transactions             []*transaction
	postCoalesceTransactions []*transaction

	maxRemoteBottomTrans int
	remoteBottomTrans    int

	remoteDataCache *remoteDataCache

	m1DirectDramBypassEnabled bool
	m1DirectDramFinder        mem.LowModuleFinder
	m1DirectBypassTargets     map[string]M1DirectBypassTarget

	m1Config                M1Config
	m1Stats                 M1Stats
	m1DirectDRAMBatches     map[m1DirectDRAMBatchKey]*m1DirectDRAMBatchEntry
	m1DirectDRAMBatchOrder  []m1DirectDRAMBatchKey
	m1NextDirectDRAMBatchID uint64

	isPaused bool
}

// SetLowModuleFinder sets the finder that tells which remote port can serve
// the data on a certain address.
func (c *Cache) SetLowModuleFinder(lmf mem.LowModuleFinder) {
	c.lowModuleFinder = lmf
}

// ConfigureM1DirectDramBypass configures L1-side L2-miss bypass. The normal
// low-module finder still identifies the target L2/RDMA path, while dramFinder
// identifies the local DRAM bank for direct local reads.
func (c *Cache) ConfigureM1DirectDramBypass(
	enable bool,
	dramFinder mem.LowModuleFinder,
	targets map[string]M1DirectBypassTarget,
) {
	c.m1DirectDramBypassEnabled = enable
	c.m1DirectDramFinder = dramFinder
	c.m1DirectBypassTargets = targets
	c.m1Stats.L1VDirectDramBypassEnabled = enable
}

func (c *Cache) canSendToBottomModule(module sim.Port) bool {
	if !c.isRemoteBottomModule(module) || c.maxRemoteBottomTrans <= 0 {
		return true
	}
	return c.remoteBottomTrans < c.maxRemoteBottomTrans
}

func (c *Cache) trackBottomTransaction(trans *transaction, module sim.Port) {
	if !c.isRemoteBottomModule(module) {
		return
	}
	trans.remoteBottom = true
	c.remoteBottomTrans++
}

func (c *Cache) releaseBottomTransaction(trans *transaction) {
	if trans == nil || !trans.remoteBottom {
		return
	}
	trans.remoteBottom = false
	if c.remoteBottomTrans > 0 {
		c.remoteBottomTrans--
	}
}

func (c *Cache) isRemoteBottomModule(module sim.Port) bool {
	if module == nil {
		return false
	}
	return strings.Contains(module.Name(), ".RDMA.")
}

// Tick update the state of the cache
func (c *Cache) Tick(now sim.VTimeInSec) bool {
	madeProgress := false

	if !c.isPaused {
		madeProgress = c.runPipeline(now) || madeProgress
	}

	madeProgress = c.controlStage.Tick(now) || madeProgress

	return madeProgress
}

func (c *Cache) runPipeline(now sim.VTimeInSec) bool {
	madeProgress := false
	madeProgress = c.tickRespondStage(now) || madeProgress
	madeProgress = c.tickParseBottomStage(now) || madeProgress
	madeProgress = c.tickBankStage(now) || madeProgress
	madeProgress = c.tickDirectoryStage(now) || madeProgress
	madeProgress = c.tickM1DirectDRAMStage(now) || madeProgress
	madeProgress = c.tickCoalesceState(now) || madeProgress
	return madeProgress
}

func (c *Cache) tickRespondStage(now sim.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.respondStage.Tick(now) || madeProgress
	}
	return madeProgress
}

func (c *Cache) tickParseBottomStage(now sim.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.parseBottomStage.Tick(now) || madeProgress
	}

	return madeProgress
}

func (c *Cache) tickBankStage(now sim.VTimeInSec) bool {
	madeProgress := false
	for _, bs := range c.bankStages {
		madeProgress = bs.Tick(now) || madeProgress
	}
	return madeProgress
}

func (c *Cache) tickDirectoryStage(now sim.VTimeInSec) bool {
	return c.directoryStage.Tick(now)
}

func (c *Cache) tickM1DirectDRAMStage(now sim.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.processM1DirectDRAMBatches(
			now, false, m1DrainManual) || madeProgress
	}
	return madeProgress
}

func (c *Cache) tickCoalesceState(now sim.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.coalesceStage.Tick(now) || madeProgress
	}
	return madeProgress
}
