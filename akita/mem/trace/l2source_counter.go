package trace

import "github.com/sarchlab/akita/v3/sim"

func (c *l2SourceCounter) add(
	bytes uint64,
	latency sim.VTimeInSec,
	receiveTime sim.VTimeInSec,
) {
	_ = receiveTime
	latencyNS := timeToNS(latency)

	c.accesses++
	c.bytes += bytes
	c.latencySumNS += latencyNS

	timeNS := timeToNS(receiveTime)
	if timeNS > 0 {
		if c.firstTimeNS == 0 || timeNS < c.firstTimeNS {
			c.firstTimeNS = timeNS
		}
		if timeNS > c.lastTimeNS {
			c.lastTimeNS = timeNS
		}
	}
}

func (c *l2SourceCounter) avgLatencyNS() uint64 {
	if c.accesses == 0 {
		return 0
	}
	return c.latencySumNS / c.accesses
}
