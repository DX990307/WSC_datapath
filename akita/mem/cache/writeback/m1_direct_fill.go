package writeback

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// PredictM1DirectDramBypass is an oracle-style predictor used by the L1V
// direct-DRAM bypass experiment. It returns true only when this L2 has neither
// a resident line nor an in-flight MSHR for the requested cache line.
func (c *Cache) PredictM1DirectDramBypass(
	pid vm.PID,
	cacheLineID uint64,
) bool {
	if c.directory.Lookup(pid, cacheLineID) != nil {
		return false
	}
	if c.mshr.Query(pid, cacheLineID) != nil {
		return false
	}
	return true
}

func (p *topParser) processCleanDataFill(
	now sim.VTimeInSec,
	port sim.Port,
	fill *mem.CleanDataFill,
) bool {
	if port == nil {
		return false
	}
	p.cache.m1Stats.DirectCleanFillReceived++
	installed := p.cache.installCleanDataFill(fill)
	memtrace.RecordMemoryPathM1BackgroundL2Fill(
		p.cache.Name(),
		fill.Info,
		fill.Meta().ID,
		fill.Meta().SendTime,
		now,
		fill.Meta().Src,
		fill.Meta().Dst,
		installed,
	)
	port.Retrieve(now)
	return true
}

func (p *topParser) processCleanFillPort(now sim.VTimeInSec) bool {
	if p.cache.cleanFillPort == nil {
		return false
	}
	req := p.cache.cleanFillPort.Peek()
	if req == nil {
		return false
	}
	fill, ok := req.(*mem.CleanDataFill)
	if !ok {
		panic("clean-fill port received non CleanDataFill message")
	}
	return p.processCleanDataFill(now, p.cache.cleanFillPort, fill)
}

func (c *Cache) installCleanDataFill(fill *mem.CleanDataFill) bool {
	lineBytes := uint64(1 << c.log2BlockSize)
	if fill == nil || uint64(len(fill.Data)) < lineBytes {
		c.m1Stats.DirectCleanFillDropped++
		return false
	}

	line := fill.Address / lineBytes * lineBytes
	if c.mshr.Query(fill.PID, line) != nil {
		c.m1Stats.DirectCleanFillDropped++
		return false
	}

	block := c.directory.Lookup(fill.PID, line)
	if block == nil {
		block = c.directory.FindVictim(line)
		if block == nil ||
			block.IsLocked || block.ReadCount > 0 ||
			(block.IsValid && block.IsDirty) {
			c.m1Stats.DirectCleanFillDropped++
			return false
		}
		block.PID = fill.PID
		block.Tag = line
		block.IsValid = true
	}

	if block.IsLocked || block.ReadCount > 0 || block.IsDirty {
		c.m1Stats.DirectCleanFillDropped++
		return false
	}

	data := append([]byte(nil), fill.Data[:lineBytes]...)
	if err := c.storage.Write(block.CacheAddress, data); err != nil {
		panic(err)
	}
	block.IsDirty = false
	block.IsLocked = false
	block.DirtyMask = nil
	c.directory.Visit(block)
	c.m1Stats.DirectCleanFillInstalled++
	return true
}
