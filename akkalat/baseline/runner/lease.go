package runner

// ISCA'27 lease: cache a remote line in the requester's L2 iff its value
// (E[re-use] x re-fetch cost, the R3 round-trip model) exceeds the
// lowest-value victim; E[re-use] measured per page (akita
// mem/cache/writeback/lease.go). Off by default.

import (
	"flag"
	"fmt"
	"log"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/mgpusim/v3/timing/pagemigration"
)

var leaseFlag = flag.Bool("lease", false,
	"Lease: remote fills are installed iff their value (E[re-use] x re-fetch cost) exceeds "+
		"the lowest-value victim's; needs -l2res-cost-repl (whose victim values it uses).")
var leaseReuseFlag = flag.String("lease-reuse", "page",
	"Lease: E[re-use] of an incoming remote line: page (per-GPM page table of measured "+
		"re-uses / fetches) or class (R4 b: class x insertion state).")
var leaseTableFlag = flag.Int("lease-table", 4096,
	"Lease: pages per GPM in the re-use table (LRU); <= 0 = unlimited.")
var leaseLineBitsFlag = flag.Bool("lease-line-bits", false,
	"Lease: re-fetches are detected by a bit per line in the page entry (across kernels) "+
		"instead of the requester's seen filter.")
var leaseEpisodesFlag = flag.Bool("lease-episodes", false,
	"Lease: E[re-use] = share of the page's fetches (episodes) whose line was accessed again "+
		"(L2 hit or re-fetch) before its next fetch (implies line bits); else every hit counts.")
var leaseBuyFlag = flag.Bool("lease-buy", true,
	"Lease: lines of a page with a migration pending or in flight have no value "+
		"(they may use an invalid way, never evict).")

func (b *R9NanoPlatformBuilder) wireLease(m *pagemigration.Manager) {
	if !*leaseFlag {
		return
	}
	if r := *leaseReuseFlag; r != "page" && r != "class" {
		log.Panicf("-lease-reuse %q", r)
	}
	for _, gpu := range b.gpus {
		var t *writeback.LeaseTable
		if *leaseReuseFlag == "page" {
			t = writeback.NewLeaseTable(writeback.LeaseTableConfig{
				Log2PageSize: 12, Entries: *leaseTableFlag, LineBits: *leaseLineBitsFlag, Episodes: *leaseEpisodesFlag,
			})
		}
		var blocked func(uint64) bool
		if *leaseBuyFlag {
			blocked = m.LeaseBlocked
		}
		for _, l2 := range gpu.pm.l2s {
			l2.SetLease(t, blocked)
		}
		gpu.pm.lease = t
	}
	fmt.Printf("[Config] lease: re-use %s, table %d pages/GPM, line bits %v, episodes %v, buy interplay %v\n",
		*leaseReuseFlag, *leaseTableFlag, *leaseLineBitsFlag, *leaseEpisodesFlag, *leaseBuyFlag)
}

// reportLeaseStats writes the lease counters (summed over GPMs / slices).
func (r *Runner) reportLeaseStats() {
	if !*leaseFlag {
		return
	}
	var s writeback.LeaseStats
	var t writeback.LeaseTableStats
	for _, gpu := range r.platform.GPUs {
		if gpu.pm == nil {
			continue
		}
		for _, l2 := range gpu.pm.l2s {
			x := l2.GetLeaseStats()
			s.BlockedFills += x.BlockedFills
			s.BlockedDrops += x.BlockedDrops
			s.ValueSum += x.ValueSum
			s.ValueN += x.ValueN
		}
		if gpu.pm.lease != nil {
			x := gpu.pm.lease.Stats()
			t.Fetches += x.Fetches
			t.Refetches += x.Refetches
			t.Hits += x.Hits
			t.Allocs += x.Allocs
			t.Replacements += x.Replacements
			t.Lookups += x.Lookups
			t.LookupMisses += x.LookupMisses
		}
	}
	col := func(what string, v float64) { r.metricsCollector.Collect("Lease", what, v) }
	col("lease_blocked_fills", float64(s.BlockedFills))
	col("lease_blocked_drops", float64(s.BlockedDrops))
	col("lease_value_evals", float64(s.ValueN))
	if s.ValueN > 0 {
		col("lease_value_mean_ns", s.ValueSum/float64(s.ValueN))
	}
	col("lease_tab_fetches", float64(t.Fetches))
	col("lease_tab_refetches", float64(t.Refetches))
	col("lease_tab_hits", float64(t.Hits))
	col("lease_tab_allocs", float64(t.Allocs))
	col("lease_tab_replacements", float64(t.Replacements))
	col("lease_tab_lookups", float64(t.Lookups))
	col("lease_tab_lookup_misses", float64(t.LookupMisses))
}
