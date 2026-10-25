package runner

// ISCA'27 comparison designs: HMG-style and CARVE-style remote data caches
// with hardware coherence (akita/mem/cache/writeback/cmp_coherence.go,
// mgpusim/timing/rdma/cmp.go). Off by default (-cmp-mode none).

import (
	"flag"
	"fmt"
	"log"
	"math/bits"
	"sort"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/timing/rdma"
)

var cmpModeFlag = flag.String("cmp-mode", "none",
	"ISCA'27 comparison design: none, hmg (HMG-style hierarchical directory "+
		"coherence, region homes) or carve (CARVE-style DRAM remote data cache, "+
		"GPU-VI broadcast invalidation with an in-memory sharing tracker).")
var cmpRegionTilesFlag = flag.Int("cmp-region-tiles", 4,
	"HMG: side of the square mesh regions that play the role of GPUs "+
		"(4 = quadrants of the 7x7 mesh: 4x4, 4x3, 3x4, 3x3 tiles).")
var cmpDirEntriesFlag = flag.Int("cmp-dir-entries", 16384,
	"HMG: coherence directory entries per GPM (HMG: 12K entries x 4 lines "+
		"of 128 B = 2x the 3-MB L2; here 16K x 4 lines of 64 B = 2x the 2-MB L2).")
var cmpDirWaysFlag = flag.Int("cmp-dir-ways", 8, "HMG: directory associativity.")
var cmpGroupLinesFlag = flag.Int("cmp-group-lines", 4,
	"HMG: 64-B lines per directory entry (HMG: 4).")
var cmpRDCMBFlag = flag.Int("cmp-rdc-mb", 256,
	"CARVE: remote data cache per GPM in MiB (256 MiB = 3.1% of the 8-GiB GPM memory).")
var cmpPrivateProbFlag = flag.Float64("cmp-private-prob", 0.01,
	"CARVE: probability that a local write returns a read-write-shared line to private.")
var cmpFillLRUFlag = flag.Bool("cmp-fill-lru", true,
	"Comparison designs: remote lines compete with local lines in the L2 under the "+
		"normal replacement (no M3 protection / first-touch invalid-way rule).")

func cmpCARVEOn() bool { return *cmpModeFlag == "carve" }

// cmpRDCBytes is the RDC carve-out per GPM.
func cmpRDCBytes() uint64 { return uint64(*cmpRDCMBFlag) << 20 }

// wireCmpRDCPort (GPU builder) gives the RDMA engine a port on the L2-DRAM
// connection for the RDC; the carve-out is the top of the GPM's memory.
func (b *R9NanoGPUBuilder) wireCmpRDCPort(finder mem.LowModuleFinder) {
	if !cmpCARVEOn() {
		return
	}
	p := b.rdmaEngine.EnableRDCPort(64)
	b.l2ToDramConnection.PlugIn(p, 64)
	b.rdmaEngine.SetRDC(finder, b.memAddrOffset+8*mem.GB-cmpRDCBytes())
}

// wireCmp installs the comparison design after the GPUs are built.
func (b *R9NanoPlatformBuilder) wireCmp(drv *driver.Driver) *writeback.CmpCoherence {
	var mode writeback.CmpMode
	switch *cmpModeFlag {
	case "none", "":
		return nil
	case "hmg":
		mode = writeback.CmpHMG
	case "carve":
		mode = writeback.CmpCARVE
	default:
		log.Panicf("-cmp-mode %q", *cmpModeFlag)
	}
	if !*remoteDataPathEnableFlag {
		log.Panic("-cmp-mode needs the remote data path (requester L2 caching of remote lines)")
	}
	tiles := *cmpRegionTilesFlag
	if tiles <= 0 {
		log.Panic("-cmp-region-tiles must be positive")
	}
	cfg := writeback.CmpConfig{
		Mode: mode, Log2OwnerSize: 33,
		DirEntries: *cmpDirEntriesFlag, DirWays: *cmpDirWaysFlag,
		RegionOf: map[int]int{}, Regions: map[int][]int{},
		RDCSets: cmpRDCBytes() / 128, PrivateProb: *cmpPrivateProbFlag,
	}
	if mode == writeback.CmpHMG {
		gl := *cmpGroupLinesFlag
		if gl <= 0 || gl&(gl-1) != 0 {
			log.Panic("-cmp-group-lines must be a power of two")
		}
		cfg.Log2Group = 6 + uint(bits.TrailingZeros(uint(gl)))
	} else {
		cfg.Log2Group = 7
	}
	i := 0
	for y := 0; y < b.tileHeight; y++ {
		for x := 0; x < b.tileWidth; x++ {
			if x == b.tileWidth/2 && y == b.tileHeight/2 {
				continue
			}
			id := int(b.gpus[i].GPUID)
			reg := (y/tiles)*1000 + x/tiles
			cfg.RegionOf[id] = reg
			cfg.Regions[reg] = append(cfg.Regions[reg], id)
			cfg.GPMs = append(cfg.GPMs, id)
			i++
		}
	}
	if i != len(b.gpus) {
		panic("cmp: GPU count does not match the mesh")
	}
	for _, m := range cfg.Regions {
		sort.Ints(m)
	}
	sort.Ints(cfg.GPMs)
	t := writeback.NewCmpCoherence(cfg)
	ports := map[int]sim.Port{}
	rdmas := map[int]*rdma.Comp{}
	for _, gpu := range b.gpus {
		ports[int(gpu.GPUID)] = gpu.RDMAEngine.ToOutside
		rdmas[int(gpu.GPUID)] = gpu.RDMAEngine
	}
	t.Send = func(now sim.VTimeInSec, from, to int, g uint64) {
		rdmas[from].QueueCohInval(now, to, g)
	}
	for _, gpu := range b.gpus {
		id := int(gpu.GPUID)
		var l2s []*writeback.Cache
		for _, c := range gpu.L2Caches {
			l2, ok := c.(*writeback.Cache)
			if !ok {
				log.Panic("cmp: L2 is not a writeback cache")
			}
			l2s = append(l2s, l2)
			l2.SetCmp(t, id, gpu.RDMAEngine.ToL2, *cmpFillLRUFlag)
		}
		gpu.RDMAEngine.EnableCmp(t, id, ports, *cmpFillLRUFlag)
		gpu.RDMAEngine.ReplicaAgeFunc = func(pid vm.PID, line uint64) (sim.VTimeInSec, bool) {
			for _, l2 := range l2s {
				if a, ok := l2.ReplicaAge(pid, line); ok {
					return a, true
				}
			}
			return 0, false
		}
	}
	drv.AddPhysicalWriteHook(t.NoteWrite)
	if mode == writeback.CmpHMG {
		fmt.Printf("[Config] cmp HMG: %d regions of %dx%d tiles, directory %d entries x %d ways "+
			"per GPM, %d lines per entry, fill-lru=%v\n", len(cfg.Regions), tiles, tiles,
			cfg.DirEntries, cfg.DirWays, *cmpGroupLinesFlag, *cmpFillLRUFlag)
	} else {
		fmt.Printf("[Config] cmp CARVE: RDC %d MiB per GPM (%d sets of 128 B), "+
			"IMST private-prob %.3f, fill-lru=%v\n", *cmpRDCMBFlag, cfg.RDCSets,
			cfg.PrivateProb, *cmpFillLRUFlag)
	}
	return t
}

// reportCmpStats writes the comparison design counters.
func (r *Runner) reportCmpStats() {
	t := r.platform.Cmp
	if t == nil {
		return
	}
	col := func(what string, v float64) { r.metricsCollector.Collect("Cmp", what, v) }
	s := t.Stats()
	col("cmp_inval_msgs", float64(s.InvalMsgs))
	col("cmp_inval_forwarded", float64(s.InvalForwarded))
	col("cmp_inval_applied", float64(s.InvalApplied))
	col("cmp_lines_invalidated", float64(s.LinesInvalidated))
	col("cmp_dir_allocs", float64(s.DirAllocs))
	col("cmp_dir_evictions", float64(s.DirEvictions))
	col("cmp_dir_evict_invals", float64(s.DirEvictInvals))
	col("cmp_dir_peak", float64(s.DirPeak))
	col("cmp_write_notes", float64(s.WriteNotes))
	col("cmp_writes_with_sharers", float64(s.WritesWithSharers))
	col("cmp_broadcasts", float64(s.Broadcasts))
	col("cmp_broadcast_msgs", float64(s.BroadcastMsgs))
	col("cmp_imst_to_rs", float64(s.IMSTToRS))
	col("cmp_imst_to_rws", float64(s.IMSTToRWS))
	col("cmp_imst_to_private", float64(s.IMSTToPrivate))
	col("cmp_rdc_inserts", float64(s.RDCInserts))
	col("cmp_rdc_replacements", float64(s.RDCReplacements))
	col("cmp_rdc_invalidated", float64(s.RDCInvalidated))
	col("cmp_rdc_stale_drops", float64(s.RDCStaleDrops))
	col("cmp_rdc_lines_end", float64(t.RDCLines()))
	col("cmp_boundary_kept", float64(s.BoundaryKept))
	col("cmp_boundary_dropped", float64(s.BoundaryDropped))
	var n rdma.CmpStats
	for _, gpu := range r.platform.GPUs {
		x := gpu.RDMAEngine.GetCmpStats()
		n.InvalSent += x.InvalSent
		n.InvalRecv += x.InvalRecv
		n.RoutedRegion += x.RoutedRegion
		n.RoutedHome += x.RoutedHome
		n.RelaysIn += x.RelaysIn
		n.RelayServedL2 += x.RelayServedL2
		n.RelayServedRemote += x.RelayServedRemote
		n.RDCProbes += x.RDCProbes
		n.RDCHits += x.RDCHits
		n.RDCMisses += x.RDCMisses
		n.RDCSiblingFetches += x.RDCSiblingFetches
		n.RDCInsertsDone += x.RDCInsertsDone
		n.RDCInsertSkips += x.RDCInsertSkips
		n.RDCDRAMWrites += x.RDCDRAMWrites
	}
	col("cmp_net_inval_sent", float64(n.InvalSent))
	col("cmp_net_inval_recv", float64(n.InvalRecv))
	col("cmp_routed_region", float64(n.RoutedRegion))
	col("cmp_routed_home", float64(n.RoutedHome))
	col("cmp_relays_in", float64(n.RelaysIn))
	col("cmp_relay_served_l2", float64(n.RelayServedL2))
	col("cmp_relay_served_remote", float64(n.RelayServedRemote))
	col("cmp_rdc_probes", float64(n.RDCProbes))
	col("cmp_rdc_hits", float64(n.RDCHits))
	col("cmp_rdc_misses", float64(n.RDCMisses))
	col("cmp_rdc_sibling_fetches", float64(n.RDCSiblingFetches))
	col("cmp_rdc_inserts_done", float64(n.RDCInsertsDone))
	col("cmp_rdc_insert_skips", float64(n.RDCInsertSkips))
	col("cmp_rdc_dram_writes", float64(n.RDCDRAMWrites))
}
