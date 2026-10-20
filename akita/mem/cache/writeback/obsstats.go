package writeback

import (
	"strings"

	"github.com/sarchlab/akita/v3/mem/cache"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
)

// ISCA'27 P13 L2 free-capacity statistic (memtrace.ObsL2*). The hooks only
// read block identity and the current time; they never change cache state.

// obsL2Index returns this cache's index in the statistic, or -1 when the
// cache is not an L2 cache or the statistic is off.
func (c *Cache) obsL2Index() int {
	if c.obsL2ID < 0 {
		return -1
	}
	if c.obsL2ID == 0 {
		if !strings.Contains(c.Name(), "L2") {
			c.obsL2ID = -1
			return -1
		}
		id := memtrace.ObsL2Register(c.Name(),
			len(c.directory.GetSets()), c.directory.WayAssociativity())
		if id < 0 {
			return -1
		}
		c.obsL2ID = id + 1
	}
	return c.obsL2ID - 1
}

// ObsRegisterL2 registers the cache with the L2 free-capacity statistic so
// that a cache never accessed is still counted (all ways invalid).
func (c *Cache) ObsRegisterL2() {
	if memtrace.ObsL2FreeEnabled() {
		c.obsL2Index()
	}
}

// obsL2Fill: a new line is allocated in block (a new lifetime starts).
func (c *Cache) obsL2Fill(block *cache.Block) {
	if !memtrace.ObsL2FreeEnabled() || block == nil {
		return
	}
	if id := c.obsL2Index(); id >= 0 {
		memtrace.ObsL2Fill(id, block.SetID, block.WayID, c.Engine.CurrentTime())
	}
}

// obsL2ReadHit: block is read (read or lookup hit).
func (c *Cache) obsL2ReadHit(block *cache.Block) {
	if !memtrace.ObsL2FreeEnabled() || block == nil {
		return
	}
	if id := c.obsL2Index(); id >= 0 {
		memtrace.ObsL2ReadHit(id, block.SetID, block.WayID,
			c.Engine.CurrentTime())
	}
}

// obsL2Invalidate: every way becomes invalid (flush/reset).
func (c *Cache) obsL2Invalidate() {
	if !memtrace.ObsL2FreeEnabled() {
		return
	}
	if id := c.obsL2Index(); id >= 0 {
		memtrace.ObsL2Invalidate(id, c.Engine.CurrentTime())
	}
}
