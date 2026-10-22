package rdma

import "github.com/sarchlab/akita/v3/sim"

// ISCA'27 wafer M1: distance-aware batch holding (-m1w-hold-*, off by
// default). On the composed M1 path a page batch normally leaves as soon as
// a window slot is free (work-conserving), so with a large window most
// batches carry one line. With holding, a batch to an owner more than
// MinHops away stays open until it is T old (T = FixedNS, or Alpha x the
// modeled RTT of its hop count) or full; same-page lines arriving meanwhile
// join it, so one window slot, one request and one owner transaction carry
// more lines. Near owners are never held. A drain flushes immediately.

// M1WHoldConfig configures batch holding.
type M1WHoldConfig struct {
	FixedNS float64
	Alpha   float64
	MinHops int
}

// Enabled reports whether holding is on.
func (c M1WHoldConfig) Enabled() bool { return c.FixedNS > 0 || c.Alpha > 0 }

// M1WHoldStats counts held batches.
type M1WHoldStats struct {
	Enabled      bool
	HeldSkips    uint64 // issue opportunities skipped because a batch was held
	HeldBatches  uint64 // batches that were held at least once
	ReleasedFull uint64
}

type m1wHoldState struct {
	cfg   M1WHoldConfig
	holdT []sim.VTimeInSec // destination index -> hold time
	seen  map[*remoteBatch]bool
	stats M1WHoldStats
}

// ConfigureM1WHold turns batch holding on (composed M1 only); it must be
// called after the T18 topology is set.
func (c *Comp) ConfigureM1WHold(cfg M1WHoldConfig) {
	if !cfg.Enabled() {
		return
	}
	if !c.m1Composed() || c.t18.hops == nil {
		panic("m1w hold needs the composed M1 path with its topology")
	}
	s := &m1wHoldState{cfg: cfg, seen: make(map[*remoteBatch]bool)}
	s.stats.Enabled = true
	s.holdT = make([]sim.VTimeInSec, len(c.t18.hops))
	for d, h := range c.t18.hops {
		if h < 0 || h <= cfg.MinHops {
			continue
		}
		ns := cfg.FixedNS
		if cfg.Alpha > 0 {
			ns = cfg.Alpha * t18RTTNS(h)
		}
		s.holdT[d] = sim.VTimeInSec(ns * 1e-9)
	}
	c.m1wHold = s
}

// GetM1WHoldStats returns the holding counters.
func (c *Comp) GetM1WHoldStats() M1WHoldStats {
	if c.m1wHold == nil {
		return M1WHoldStats{}
	}
	return c.m1wHold.stats
}

// m1wHeld reports whether batch must stay open now.
func (c *Comp) m1wHeld(now sim.VTimeInSec, batch *remoteBatch) bool {
	s := c.m1wHold
	if batch.lineCount() >= c.remoteConfig.MaxBatchLines {
		if s.seen[batch] {
			s.stats.ReleasedFull++
			delete(s.seen, batch)
		}
		return false
	}
	t := s.holdT[c.t18DstIndex(batch.dst)]
	if t == 0 || now-batch.createdAt >= t {
		delete(s.seen, batch)
		return false
	}
	if !s.seen[batch] {
		s.seen[batch] = true
		s.stats.HeldBatches++
	}
	s.stats.HeldSkips++
	return true
}
