package writearound

import (
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// M1Config controls the narrow M1 path: direct local DRAM bypass plus 128B
// access-unit coalescing for L1V misses that are predicted absent in L2.
type M1Config struct {
	DirectDRAMBatchEntries int
	DirectDRAMBatchLines   int
	DirectDRAMBatchWaitNS  uint64
}

// M1Stats reports only the direct bypass and AU coalescing counters.
type M1Stats struct {
	L1VDirectDramBypassEnabled bool
	L1VDirectBypassLookups     uint64
	L1VDirectBypassPredAbsent  uint64
	L1VDirectBypassIssued      uint64
	L1VDirectBypassBlocked     uint64
	L1VDirectBypassResponses   uint64
	L1VDirectL2FillIssued      uint64
	L1VDirectL2FillDropped     uint64

	L1VDirectDRAMBatchesCreated     uint64
	L1VDirectDRAMBatchesDrained     uint64
	L1VDirectDRAMLinesInBatches     uint64
	L1VDirectDRAMSingletonFallbacks uint64
	L1VDirectDRAMFullDrains         uint64
	L1VDirectDRAMTimeoutDrains      uint64
	L1VDirectDRAMCapacityDrains     uint64
	L1VDirectDRAMManualDrains       uint64
	L1VDirectDRAMMaxLinesPerBatch   uint64
	L1VDirectDRAMTotalWaitNS        float64
	L1VDirectDRAMWaitSamples        uint64
	L1VDirectDRAMMultiLineReads     uint64
	L1VDirectDRAMSingleLineReads    uint64
}

type m1DirectDRAMBatchKey struct {
	pid          vm.PID
	dramPortName string
	auBase       uint64
}

type m1DirectDRAMBatchEntry struct {
	id            uint64
	key           m1DirectDRAMBatchKey
	dramPort      sim.Port
	oldestArrival sim.VTimeInSec
	newestArrival sim.VTimeInSec
	lineSet       map[uint64]bool
	transactions  []*transaction
}

const (
	m1DrainFull = iota
	m1DrainTimeout
	m1DrainCapacity
	m1DrainManual
)

func normalizeM1Config(c M1Config) M1Config {
	if c.DirectDRAMBatchEntries <= 0 {
		c.DirectDRAMBatchEntries = 32
	}
	if c.DirectDRAMBatchLines <= 0 || c.DirectDRAMBatchLines > m1DirectDRAMAULines {
		c.DirectDRAMBatchLines = m1DirectDRAMAULines
	}
	if c.DirectDRAMBatchWaitNS == 0 {
		c.DirectDRAMBatchWaitNS = 10
	}
	return c
}

// ConfigureM1 configures the direct local DRAM bypass coalescer.
func (c *Cache) ConfigureM1(config M1Config) {
	c.m1Config = normalizeM1Config(config)
}

// GetM1Stats returns a copy of the M1 counters.
func (c *Cache) GetM1Stats() M1Stats {
	return c.m1Stats
}

func (c *Cache) resetM1Batches() {
	c.m1DirectDRAMBatches = nil
	c.m1DirectDRAMBatchOrder = nil
	c.m1NextDirectDRAMBatchID = 0
}

func (c *Cache) m1LineID(trans *transaction) uint64 {
	lineBytes := uint64(1 << c.log2BlockSize)
	return trans.Address() / lineBytes * lineBytes
}

func (c *Cache) m1Expired(
	now sim.VTimeInSec,
	arrival sim.VTimeInSec,
	waitNS uint64,
) bool {
	if waitNS == 0 {
		return true
	}
	wait := sim.VTimeInSec(float64(waitNS) * 1e-9)
	return now-arrival >= wait
}

func m1VTimeToNS(v sim.VTimeInSec) float64 {
	return float64(v) * 1e9
}
