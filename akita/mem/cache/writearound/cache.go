package writearound

import (
	"strings"

	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// A Cache is a customized L1 cache the for R9nano GPUs.
type Cache struct {
	*sim.TickingComponent

	topPort     sim.Port
	bottomPort  sim.Port
	controlPort sim.Port

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

	m1Config      M1Config
	m1Stats       M1Stats
	m1Batches     map[m1BatchKey]*m1BatchEntry
	m1BatchOrder  []m1BatchKey
	m1NextBatchID uint64
	m1BadDrainRun int
	m1BypassUntil sim.VTimeInSec

	isPaused bool
}

// SetLowModuleFinder sets the finder that tells which remote port can serve
// the data on a certain address.
func (c *Cache) SetLowModuleFinder(lmf mem.LowModuleFinder) {
	c.lowModuleFinder = lmf
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

func (c *Cache) tickCoalesceState(now sim.VTimeInSec) bool {
	madeProgress := false
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.coalesceStage.Tick(now) || madeProgress
	}
	for i := 0; i < c.numReqPerCycle; i++ {
		madeProgress = c.processM1Batches(now, false, m1DrainManual) ||
			madeProgress
	}
	return madeProgress
}
