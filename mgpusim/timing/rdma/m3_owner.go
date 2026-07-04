package rdma

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// ConfigureM3 enables or disables owner-side per-requester fair service.
func (c *Comp) ConfigureM3(
	enable bool,
	fairQuantumLines int,
	maxConsecutive int,
	hardAgeNS uint64,
) {
	if fairQuantumLines <= 0 {
		fairQuantumLines = 8
	}
	if maxConsecutive <= 0 {
		maxConsecutive = 2
	}
	c.m3Enabled = enable
	c.m3FairQuantum = fairQuantumLines
	c.m3MaxConsecutive = maxConsecutive
	c.m3HardAge = sim.VTimeInSec(float64(hardAgeNS) * 1e-9)
	c.M3Stats.Enabled = enable
}

// GetM3Stats returns a copy of the M3 counters.
func (c *Comp) GetM3Stats() M3Stats {
	return c.M3Stats
}

func (c *Comp) ensureM3State() {
	if c.m3Queues == nil {
		c.m3Queues = make(map[string][]m3OwnerServiceRequest)
	}
	if c.m3Deficit == nil {
		c.m3Deficit = make(map[string]int)
	}
}

func (c *Comp) enqueueM3OwnerRequest(
	now sim.VTimeInSec,
	msg sim.Msg,
	firstSeen sim.VTimeInSec,
) {
	c.ensureM3State()
	requester := m2PortName(msg.Meta().Src)
	lineCount := 1
	if bitmap, ok := msg.(*BitmapReadReq); ok {
		lineCount = bitmapLineCount(bitmap.LineBitmap)
		requester = bitmap.RequesterName
	}
	if requester == "" {
		requester = "unknown"
	}

	if len(c.m3Queues[requester]) == 0 {
		c.m3Active = append(c.m3Active, requester)
	}
	c.m3Queues[requester] = append(c.m3Queues[requester], m3OwnerServiceRequest{
		requesterName: requester,
		lineCount:     lineCount,
		arrival:       firstSeen,
		msg:           msg,
	})
	c.M3Stats.OwnerReceivedPackets++
	_ = now
}

func (c *Comp) processM3OwnerQueue(now sim.VTimeInSec) bool {
	madeProgress := false
	for c.m3Enabled && c.m3HasPendingWork() {
		if !c.ToL2.CanSend() {
			return madeProgress
		}

		item, ok, selectedBy := c.selectM3OwnerRequest(now)
		if !ok {
			return madeProgress
		}

		served := c.serveM3OwnerRequest(now, item)
		if !served {
			c.pushFrontM3OwnerRequest(item)
			return madeProgress
		}

		c.recordM3OwnerService(now, item, selectedBy)
		madeProgress = true

		if _, isBitmap := item.msg.(*BitmapReadReq); isBitmap {
			return madeProgress
		}
	}

	return madeProgress
}

func (c *Comp) serveM3OwnerRequest(
	now sim.VTimeInSec,
	item m3OwnerServiceRequest,
) bool {
	switch msg := item.msg.(type) {
	case mem.AccessReq:
		return c.sendReqFromOutside(now, msg, item.arrival, false)
	case *BitmapReadReq:
		c.startM2OwnerBatch(now, msg, item.arrival)
		return true
	default:
		panic("unknown M3 owner request")
	}
}

func (c *Comp) recordM3OwnerService(
	now sim.VTimeInSec,
	item m3OwnerServiceRequest,
	selectedBy string,
) {
	wait := now - item.arrival
	c.M3Stats.TotalOwnerWaitNS += m2VTimeToNS(wait)
	c.M3Stats.OwnerWaitSamples++
	c.M3Stats.OwnerServedPackets++
	c.M3Stats.OwnerServedLines += uint64(item.lineCount)
	if item.requesterName == c.m3LastRequester {
		c.m3Consecutive++
	} else {
		c.m3LastRequester = item.requesterName
		c.m3Consecutive = 1
	}
	if uint64(c.m3Consecutive) > c.M3Stats.MaxConsecutive {
		c.M3Stats.MaxConsecutive = uint64(c.m3Consecutive)
	}

	_ = selectedBy
}

func (c *Comp) selectM3OwnerRequest(
	now sim.VTimeInSec,
) (m3OwnerServiceRequest, bool, string) {
	if item, ok := c.findM3HardAgeRequest(now); ok {
		c.removeM3Specific(item)
		c.M3Stats.HardAgeEscapes++
		return item, true, m3SelectHardAge
	}

	c.cleanupM3Active()
	n := len(c.m3Active)
	if n == 0 {
		return m3OwnerServiceRequest{}, false, ""
	}
	if !c.hasM3Competition() {
		req := c.popM3Front(c.m3Active[0])
		return req, true, m3SelectFIFO
	}

	for rounds := 0; rounds < 2*n; rounds++ {
		requester := c.m3Active[c.m3RoundRobin%len(c.m3Active)]
		c.m3RoundRobin = (c.m3RoundRobin + 1) % len(c.m3Active)
		queue := c.m3Queues[requester]
		if len(queue) == 0 {
			continue
		}

		c.m3Deficit[requester] += c.m3FairQuantum
		item := queue[0]
		if c.shouldSkipM3Consecutive(requester) {
			c.M3Stats.ConsecutiveCapHits++
			continue
		}
		if c.m3Deficit[requester] >= item.lineCount {
			c.m3Deficit[requester] -= item.lineCount
			return c.popM3Front(requester), true, m3SelectDRR
		}
	}

	requester := c.oldestM3Requester()
	if requester == "" {
		return m3OwnerServiceRequest{}, false, ""
	}
	c.M3Stats.FallbackOldest++
	return c.popM3Front(requester), true, m3SelectFallbackOldest
}

func (c *Comp) findM3HardAgeRequest(
	now sim.VTimeInSec,
) (m3OwnerServiceRequest, bool) {
	if c.m3HardAge <= 0 {
		return m3OwnerServiceRequest{}, false
	}
	var oldest m3OwnerServiceRequest
	found := false
	for _, queue := range c.m3Queues {
		for _, item := range queue {
			if now-item.arrival < c.m3HardAge {
				continue
			}
			if !found || item.arrival < oldest.arrival {
				oldest = item
				found = true
			}
		}
	}
	return oldest, found
}

func (c *Comp) shouldSkipM3Consecutive(requester string) bool {
	return c.hasM3Competition() &&
		requester == c.m3LastRequester &&
		c.m3Consecutive >= c.m3MaxConsecutive
}

func (c *Comp) hasM3Competition() bool {
	active := 0
	for _, requester := range c.m3Active {
		if len(c.m3Queues[requester]) > 0 {
			active++
		}
	}
	return active > 1
}

func (c *Comp) oldestM3Requester() string {
	oldestRequester := ""
	var oldest sim.VTimeInSec
	for requester, queue := range c.m3Queues {
		if len(queue) == 0 {
			continue
		}
		if oldestRequester == "" || queue[0].arrival < oldest {
			oldestRequester = requester
			oldest = queue[0].arrival
		}
	}
	return oldestRequester
}

func (c *Comp) popM3Front(requester string) m3OwnerServiceRequest {
	queue := c.m3Queues[requester]
	item := queue[0]
	c.m3Queues[requester] = queue[1:]
	c.cleanupM3Active()
	return item
}

func (c *Comp) pushFrontM3OwnerRequest(item m3OwnerServiceRequest) {
	c.ensureM3State()
	queue := c.m3Queues[item.requesterName]
	if len(queue) == 0 {
		c.m3Active = append(c.m3Active, item.requesterName)
	}
	c.m3Queues[item.requesterName] = append(
		[]m3OwnerServiceRequest{item},
		queue...,
	)
}

func (c *Comp) removeM3Specific(target m3OwnerServiceRequest) {
	for requester, queue := range c.m3Queues {
		for i, item := range queue {
			if item.msg.Meta().ID == target.msg.Meta().ID {
				c.m3Queues[requester] = append(queue[:i], queue[i+1:]...)
				c.cleanupM3Active()
				return
			}
		}
	}
}

func (c *Comp) cleanupM3Active() {
	if len(c.m3Active) == 0 {
		return
	}
	active := c.m3Active[:0]
	for _, requester := range c.m3Active {
		if len(c.m3Queues[requester]) > 0 {
			active = append(active, requester)
		}
	}
	c.m3Active = active
	if len(c.m3Active) == 0 {
		c.m3RoundRobin = 0
	} else if c.m3RoundRobin >= len(c.m3Active) {
		c.m3RoundRobin = c.m3RoundRobin % len(c.m3Active)
	}
}

func (c *Comp) m3HasPendingWork() bool {
	for _, queue := range c.m3Queues {
		if len(queue) > 0 {
			return true
		}
	}
	return false
}
