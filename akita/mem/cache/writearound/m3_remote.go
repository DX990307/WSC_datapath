package writearound

import (
	"github.com/sarchlab/akita/v3/mem/vm"
)

type M3RemoteDataStats struct {
	Enabled bool
	Entries uint64

	DemandHits   uint64
	DemandMisses uint64
	DemandFills  uint64

	PrefetchFills      uint64
	PrefetchFillDrops  uint64
	PrefetchDemandHits uint64

	Invalidations uint64
	Evictions     uint64
	MaxOccupancy  uint64
}

type remoteDataCacheKey struct {
	pid  vm.PID
	addr uint64
}

type remoteDataCacheLine struct {
	key        remoteDataCacheKey
	data       []byte
	prefetched bool
}

type remoteDataCache struct {
	enabled bool
	entries int

	lines map[remoteDataCacheKey]*remoteDataCacheLine
	lru   []remoteDataCacheKey
	stats M3RemoteDataStats
}

func newRemoteDataCache(enabled bool, entries int) *remoteDataCache {
	if entries <= 0 {
		entries = 128
	}
	c := &remoteDataCache{
		enabled: enabled,
		entries: entries,
		lines:   make(map[remoteDataCacheKey]*remoteDataCacheLine),
	}
	c.stats.Enabled = enabled
	c.stats.Entries = uint64(entries)
	return c
}

func (c *remoteDataCache) lookup(pid vm.PID, addr uint64) ([]byte, bool) {
	if c == nil || !c.enabled {
		return nil, false
	}
	key := remoteDataCacheKey{pid: pid, addr: addr}
	line := c.lines[key]
	if line == nil {
		c.stats.DemandMisses++
		return nil, false
	}
	c.visit(key)
	c.stats.DemandHits++
	if line.prefetched {
		c.stats.PrefetchDemandHits++
		line.prefetched = false
	}
	return append([]byte(nil), line.data...), true
}

func (c *remoteDataCache) fillDemand(pid vm.PID, addr uint64, data []byte) {
	if c == nil || !c.enabled {
		return
	}
	c.insert(pid, addr, data, false)
	c.stats.DemandFills++
}

func (c *remoteDataCache) fillPrefetch(pid vm.PID, addr uint64, data []byte) {
	if c == nil || !c.enabled {
		if c != nil {
			c.stats.PrefetchFillDrops++
		}
		return
	}
	c.insert(pid, addr, data, true)
	c.stats.PrefetchFills++
}

func (c *remoteDataCache) invalidate(pid vm.PID, addr uint64) {
	if c == nil || !c.enabled {
		return
	}
	key := remoteDataCacheKey{pid: pid, addr: addr}
	if c.lines[key] == nil {
		return
	}
	delete(c.lines, key)
	c.removeFromLRU(key)
	c.stats.Invalidations++
}

func (c *remoteDataCache) insert(
	pid vm.PID,
	addr uint64,
	data []byte,
	prefetched bool,
) {
	key := remoteDataCacheKey{pid: pid, addr: addr}
	if line := c.lines[key]; line != nil {
		line.data = append(line.data[:0], data...)
		line.prefetched = prefetched
		c.visit(key)
		return
	}

	for len(c.lines) >= c.entries && len(c.lru) > 0 {
		victim := c.lru[0]
		c.lru = c.lru[1:]
		if c.lines[victim] != nil {
			delete(c.lines, victim)
			c.stats.Evictions++
			break
		}
	}

	c.lines[key] = &remoteDataCacheLine{
		key:        key,
		data:       append([]byte(nil), data...),
		prefetched: prefetched,
	}
	c.lru = append(c.lru, key)
	if uint64(len(c.lines)) > c.stats.MaxOccupancy {
		c.stats.MaxOccupancy = uint64(len(c.lines))
	}
}

func (c *remoteDataCache) visit(key remoteDataCacheKey) {
	c.removeFromLRU(key)
	c.lru = append(c.lru, key)
}

func (c *remoteDataCache) removeFromLRU(key remoteDataCacheKey) {
	for i, candidate := range c.lru {
		if candidate == key {
			c.lru = append(c.lru[:i], c.lru[i+1:]...)
			return
		}
	}
}

func (c *Cache) RemoteDataStats() M3RemoteDataStats {
	if c.remoteDataCache == nil {
		return M3RemoteDataStats{}
	}
	return c.remoteDataCache.stats
}

func (c *Cache) remoteDataCacheEnabled() bool {
	return c.remoteDataCache != nil && c.remoteDataCache.enabled
}
