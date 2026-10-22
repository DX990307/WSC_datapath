package writearound

// ISCA'27 P19 (-report-qt-diag): time-weighted L1V MSHR occupancy.
// Statistics only; nil (off) by default, and nothing is read back into the
// timing model.

import "github.com/sarchlab/akita/v3/sim"

// MSHRStats is the time-weighted MSHR occupancy of one cache.
type MSHRStats struct {
	Capacity   int
	StartNS    float64
	LastNS     float64
	FullNS     float64 // time with every MSHR entry in use
	AreaNS     float64 // integral of entries in use over time
	Used       int
	FullStalls uint64 // read misses that found the MSHR full
}

// EnableMSHRStats starts recording the MSHR occupancy.
func (c *Cache) EnableMSHRStats() {
	c.mshrStats = &MSHRStats{Capacity: c.mshrCapacity, StartNS: -1}
}

// MSHRStatsResult closes the occupancy integral at endNS and returns it
// (nil when off).
func (c *Cache) MSHRStatsResult(endNS float64) *MSHRStats {
	if c.mshrStats == nil {
		return nil
	}
	c.mshrStatsAdvance(endNS)
	return c.mshrStats
}

func (c *Cache) mshrStatsAdvance(nowNS float64) {
	s := c.mshrStats
	if s.StartNS < 0 {
		s.StartNS, s.LastNS = 0, 0
	}
	if dt := nowNS - s.LastNS; dt > 0 {
		s.AreaNS += float64(s.Used) * dt
		if s.Used >= s.Capacity {
			s.FullNS += dt
		}
		s.LastNS = nowNS
	}
}

// mshrStatsChange records the MSHR occupancy after a change at now.
func (c *Cache) mshrStatsChange(now sim.VTimeInSec) {
	if c.mshrStats == nil {
		return
	}
	c.mshrStatsAdvance(float64(now) * 1e9)
	c.mshrStats.Used = len(c.mshr.AllEntries())
}

func (c *Cache) mshrStatsFullStall() {
	if c.mshrStats != nil {
		c.mshrStats.FullStalls++
	}
}
