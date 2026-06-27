package writearound

import (
	"strings"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

const (
	bottomReorderPolicyNone = "none"
	bottomReorderPolicyFIFO = "fifo"
	bottomReorderPolicyHLQ  = "hlq"

	bottomReorderCachelineBytes = uint64(64)
	bottomReorderPageBytes      = uint64(4096)
	bottomReorderChannels       = uint64(8)
	bottomReorderBanks          = uint64(16)
	bottomReorderRowBytes       = uint64(2048)

	bottomReorderRowHitBonus     = 4
	bottomReorderCacheAffinity   = 2
	bottomReorderChannelBalance  = 1
	bottomReorderAgeBonusPer50NS = 1
)

type bottomReorderEntry struct {
	trans        *transaction
	req          mem.AccessReq
	bottomModule sim.Port
	enqueueTime  sim.VTimeInSec
	sequence     uint64

	address    uint64
	lineAddr   uint64
	pageID     uint64
	lineInPage uint64
	channel    uint64
	bank       uint64
	row        uint64
	remote     bool
	target     string
}

type bottomReorderBankKey struct {
	target  string
	channel uint64
	bank    uint64
}

type bottomReorderChannelKey struct {
	target  string
	channel uint64
}

type bottomReorderBucketKey struct {
	target  string
	remote  bool
	channel uint64
	bank    uint64
	row     uint64
}

func normalizeBottomReorderPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", bottomReorderPolicyNone:
		return bottomReorderPolicyNone
	case bottomReorderPolicyFIFO:
		return bottomReorderPolicyFIFO
	case bottomReorderPolicyHLQ:
		return bottomReorderPolicyHLQ
	default:
		return bottomReorderPolicyNone
	}
}

func (c *Cache) bottomReorderEnabled() bool {
	return c.bottomReorderPolicy != bottomReorderPolicyNone &&
		c.bottomReorderWindow > 0
}

func (c *Cache) canEnqueueBottomReorder() bool {
	if !c.bottomReorderEnabled() {
		return false
	}
	return len(c.bottomReorderQueue) < c.bottomReorderWindow
}

func (c *Cache) enqueueBottomReorder(
	now sim.VTimeInSec,
	trans *transaction,
	req mem.AccessReq,
	bottomModule sim.Port,
	address uint64,
) {
	entry := &bottomReorderEntry{
		trans:        trans,
		req:          req,
		bottomModule: bottomModule,
		enqueueTime:  now,
		sequence:     c.bottomReorderSequence,
		address:      address,
		remote:       c.isRemoteBottomModule(bottomModule),
		target:       bottomReorderTarget(bottomModule),
	}
	c.bottomReorderSequence++
	entry.deriveAddressFields()
	c.bottomReorderQueue = append(c.bottomReorderQueue, entry)
}

func bottomReorderTarget(module sim.Port) string {
	if module == nil {
		return ""
	}
	return module.Name()
}

func (e *bottomReorderEntry) deriveAddressFields() {
	e.lineAddr = e.address / bottomReorderCachelineBytes
	e.pageID = e.address / bottomReorderPageBytes
	e.lineInPage = (e.address % bottomReorderPageBytes) /
		bottomReorderCachelineBytes
	e.channel = e.lineAddr % bottomReorderChannels
	e.bank = (e.lineAddr / bottomReorderChannels) % bottomReorderBanks
	e.row = e.address / bottomReorderRowBytes
}

func (c *Cache) issueBottomReorder(now sim.VTimeInSec) bool {
	if len(c.bottomReorderQueue) == 0 {
		return false
	}

	idx := c.selectBottomReorderEntry(now)
	if idx < 0 {
		return false
	}

	entry := c.bottomReorderQueue[idx]
	if !c.canSendToBottomModule(entry.bottomModule) {
		return false
	}

	entry.req.Meta().SendTime = now
	err := c.bottomPort.Send(entry.req)
	if err != nil {
		return false
	}

	c.bottomReorderQueue = append(
		c.bottomReorderQueue[:idx],
		c.bottomReorderQueue[idx+1:]...)
	c.trackBottomTransaction(entry.trans, entry.bottomModule)
	c.recordBottomReorderIssue(entry)
	tracing.TraceReqInitiate(entry.req, c, entry.trans.id)

	return true
}

func (c *Cache) selectBottomReorderEntry(now sim.VTimeInSec) int {
	sendable := c.sendableBottomReorderIndices()
	if len(sendable) == 0 {
		return -1
	}

	if c.bottomReorderMaxAgeNS > 0 {
		oldest := c.oldestBottomReorderIndex(sendable)
		if c.bottomReorderAgeExceeded(
			c.bottomReorderQueue[oldest], now) {
			return oldest
		}
	}

	switch c.bottomReorderPolicy {
	case bottomReorderPolicyFIFO:
		return c.oldestBottomReorderIndex(sendable)
	case bottomReorderPolicyHLQ:
		return c.selectHLQBottomReorderIndex(sendable, now)
	default:
		return c.oldestBottomReorderIndex(sendable)
	}
}

func (c *Cache) sendableBottomReorderIndices() []int {
	indices := make([]int, 0, len(c.bottomReorderQueue))
	for i, entry := range c.bottomReorderQueue {
		if c.canSendToBottomModule(entry.bottomModule) {
			indices = append(indices, i)
		}
	}
	return indices
}

func (c *Cache) oldestBottomReorderIndex(indices []int) int {
	oldest := indices[0]
	for _, idx := range indices[1:] {
		if bottomReorderOlder(
			c.bottomReorderQueue[idx],
			c.bottomReorderQueue[oldest]) {
			oldest = idx
		}
	}
	return oldest
}

func bottomReorderOlder(a, b *bottomReorderEntry) bool {
	if a.enqueueTime != b.enqueueTime {
		return a.enqueueTime < b.enqueueTime
	}
	return a.sequence < b.sequence
}

func (c *Cache) bottomReorderAgeExceeded(
	entry *bottomReorderEntry,
	now sim.VTimeInSec,
) bool {
	age := now - entry.enqueueTime
	return age >= sim.VTimeInSec(c.bottomReorderMaxAgeNS)*1e-9
}

func (c *Cache) selectHLQBottomReorderIndex(
	indices []int,
	now sim.VTimeInSec,
) int {
	buckets := make(map[bottomReorderBucketKey][]int)
	for _, idx := range indices {
		entry := c.bottomReorderQueue[idx]
		key := bottomReorderBucketKey{
			target:  entry.target,
			remote:  entry.remote,
			channel: entry.channel,
			bank:    entry.bank,
			row:     entry.row,
		}
		buckets[key] = append(buckets[key], idx)
	}

	bestScore := int64(0)
	bestBucketSet := false
	bestBucket := bottomReorderBucketKey{}
	for key, bucketIndices := range buckets {
		score := c.scoreBottomReorderBucket(key, bucketIndices, now)
		if !bestBucketSet || score > bestScore {
			bestScore = score
			bestBucketSet = true
			bestBucket = key
		}
	}

	return c.chooseInsideHLQBottomBucket(buckets[bestBucket])
}

func (c *Cache) scoreBottomReorderBucket(
	key bottomReorderBucketKey,
	indices []int,
	now sim.VTimeInSec,
) int64 {
	score := int64(0)
	bankKey := bottomReorderBankKey{
		target:  key.target,
		channel: key.channel,
		bank:    key.bank,
	}
	if row, ok := c.bottomReorderOpenRows[bankKey]; ok && row == key.row {
		score += bottomReorderRowHitBonus
	}

	score += int64(bottomReorderCacheAffinity *
		c.largestBottomPageGroup(indices))

	channelKey := bottomReorderChannelKey{
		target:  key.target,
		channel: key.channel,
	}
	maxIssued := c.maxBottomChannelIssued()
	score += int64(bottomReorderChannelBalance) *
		int64(maxIssued-c.bottomReorderChannels[channelKey])

	oldest := c.oldestBottomReorderIndex(indices)
	ageNS := uint64((now - c.bottomReorderQueue[oldest].enqueueTime) * 1e9)
	score += int64(bottomReorderAgeBonusPer50NS * int(ageNS/50))

	score += int64(len(indices))
	return score
}

func (c *Cache) largestBottomPageGroup(indices []int) int {
	counts := make(map[uint64]int)
	largest := 0
	for _, idx := range indices {
		entry := c.bottomReorderQueue[idx]
		counts[entry.pageID]++
		if counts[entry.pageID] > largest {
			largest = counts[entry.pageID]
		}
	}
	return largest
}

func (c *Cache) maxBottomChannelIssued() uint64 {
	var max uint64
	for _, count := range c.bottomReorderChannels {
		if count > max {
			max = count
		}
	}
	return max
}

func (c *Cache) chooseInsideHLQBottomBucket(indices []int) int {
	pageCounts := make(map[uint64]int)
	for _, idx := range indices {
		pageCounts[c.bottomReorderQueue[idx].pageID]++
	}

	best := indices[0]
	for _, idx := range indices[1:] {
		entry := c.bottomReorderQueue[idx]
		bestEntry := c.bottomReorderQueue[best]
		if pageCounts[entry.pageID] > pageCounts[bestEntry.pageID] {
			best = idx
			continue
		}
		if pageCounts[entry.pageID] < pageCounts[bestEntry.pageID] {
			continue
		}
		if entry.lineInPage < bestEntry.lineInPage {
			best = idx
			continue
		}
		if entry.lineInPage > bestEntry.lineInPage {
			continue
		}
		if bottomReorderOlder(entry, bestEntry) {
			best = idx
		}
	}
	return best
}

func (c *Cache) recordBottomReorderIssue(entry *bottomReorderEntry) {
	bankKey := bottomReorderBankKey{
		target:  entry.target,
		channel: entry.channel,
		bank:    entry.bank,
	}
	channelKey := bottomReorderChannelKey{
		target:  entry.target,
		channel: entry.channel,
	}
	c.bottomReorderOpenRows[bankKey] = entry.row
	c.bottomReorderChannels[channelKey]++
}
