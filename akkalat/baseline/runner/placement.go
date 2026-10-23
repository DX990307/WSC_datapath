package runner

// Placement oracle (ISCA27 P20, two-pass): every 1 KB physical block listed
// in -place-table lives at the given GPM (tile) for the whole run instead of
// at the GPM that owns its physical address. There is no migration cost: the
// table is applied from the start, as if the allocator had placed the block
// there. Off by default (no table, no wrappers).
//
// How: the data itself stays in the global storage; only the home that
// serves an address changes, consistently for every GPM:
//   - L1V / L1S of GPM g: a placed block whose target is g goes to g's own L2
//     slices (no address-range check); a placed block with another target
//     goes to g's RDMA; unlisted addresses use the original finder.
//   - RDMA, requester side: the shared address -> owner-RDMA table returns
//     the target's RDMA for placed blocks (so batching, merging, M3 retention
//     and the statistics all see the new owner).
//   - RDMA, owner side: a placed block whose target is this GPM is served by
//     its own L2 slices (the original finder would reject the address).
// Every access to a block therefore goes through exactly one home L2, as in
// the baseline, so the oracle keeps the baseline's coherence (unlike
// -pgcls-oracle-pages / -force-local-data-access). Instruction fetches (L1I)
// keep the original routing; instruction blocks are never written.

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

const placeLog2Block = 10 // 1 KB blocks

var placeTableFlag = flag.String("place-table", "",
	"Placement oracle: file with lines '<1KB block = phys addr >> 10> <tile>'; "+
		"each listed block is served by the GPM at <tile> for the whole run. "+
		"Empty = off.")
var placeRandomFlag = flag.Uint64("place-random-seed", 0,
	"Placement oracle self-test: every 1 KB block accessed through L1V/L1S "+
		"is served by a pseudo-random GPM (hash of block and seed). 0 = off.")

type placeTable struct {
	blocks map[uint64]int // block -> tile
	seed   uint64
	tiles  []int // GPM tiles, for the random mode
	// dyn (P21) are the 1 KB block homes changed at run time by the page
	// migration manager (adaptive placement, first touch); checked first.
	dyn func(blk uint64) (int, bool)
	// statistics
	l1Local, l1Remote, ownerLocal, misrouted uint64
}

func (t *placeTable) lookup(addr uint64) (int, bool) {
	blk := addr >> placeLog2Block
	if t.dyn != nil {
		if tile, ok := t.dyn(blk); ok {
			return tile, true
		}
	}
	if t.seed != 0 {
		return t.tiles[placeMix(blk^t.seed)%uint64(len(t.tiles))], true
	}
	tile, ok := t.blocks[blk]
	return tile, ok
}

func placeMix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// placeL1Finder routes the L1 caches of one GPM.
type placeL1Finder struct {
	t     *placeTable
	self  int
	local mem.LowModuleFinder // own L2 slices, any address
	rdma  sim.Port
	def   mem.LowModuleFinder
}

func (f *placeL1Finder) Find(addr uint64) sim.Port {
	tile, ok := f.t.lookup(addr)
	if !ok {
		return f.def.Find(addr)
	}
	if tile == f.self {
		f.t.l1Local++
		return f.local.Find(addr)
	}
	f.t.l1Remote++
	return f.rdma
}

// placeOwnerFinder is the RDMA's local (owner-side) finder.
type placeOwnerFinder struct {
	t     *placeTable
	self  int
	local mem.LowModuleFinder
	def   mem.LowModuleFinder
}

func (f *placeOwnerFinder) Find(addr uint64) sim.Port {
	tile, ok := f.t.lookup(addr)
	if !ok {
		return f.def.Find(addr)
	}
	if tile != f.self {
		// Should not happen: every requester routes by the same table.
		f.t.misrouted++
	} else {
		f.t.ownerLocal++
	}
	return f.local.Find(addr)
}

// placeAddrTable is the requester-side address -> owner RDMA table.
type placeAddrTable struct {
	t     *placeTable
	ports map[int]sim.Port
	def   mem.LowModuleFinder
}

func (f *placeAddrTable) Find(addr uint64) sim.Port {
	tile, ok := f.t.lookup(addr)
	if !ok {
		return f.def.Find(addr)
	}
	return f.ports[tile]
}

type lowModuleFinderSetter interface {
	SetLowModuleFinder(lmf mem.LowModuleFinder)
}

func loadPlaceTable(path string) map[uint64]int {
	fh, err := os.Open(path)
	if err != nil {
		panic(err)
	}
	defer fh.Close()
	blocks := map[uint64]int{}
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			panic(fmt.Sprintf("-place-table: bad line %q", line))
		}
		blk, err1 := strconv.ParseUint(f[0], 10, 64)
		tile, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			panic(fmt.Sprintf("-place-table: bad line %q", line))
		}
		blocks[blk] = tile
	}
	if err := sc.Err(); err != nil {
		panic(err)
	}
	return blocks
}

func (r *Runner) addPlacement() {
	if !r.Timing {
		return
	}
	pm := r.platform.PageMigration
	dyn := pm != nil && pm.UsesBlockHomes()
	if *placeTableFlag == "" && *placeRandomFlag == 0 && !dyn {
		return
	}
	if *placeTableFlag != "" && *placeRandomFlag != 0 {
		panic("-place-table and -place-random-seed are exclusive")
	}
	if *pgclsOracleFlag != "" || *forceLocalDataAccessFlag {
		panic("-place-table cannot be combined with -pgcls-oracle-pages / " +
			"-force-local-data-access")
	}
	t := &placeTable{seed: *placeRandomFlag}
	if dyn {
		t.dyn = pm.BlockHomeTile
	}
	if *placeTableFlag != "" {
		t.blocks = loadPlaceTable(*placeTableFlag)
	}
	ports := map[int]sim.Port{}
	for _, gpu := range r.platform.GPUs {
		tile := pgclsTile(gpu.RDMAEngine.Name())
		if tile < 0 {
			panic("-place-table: cannot parse GPM tile of " +
				gpu.RDMAEngine.Name())
		}
		ports[tile] = gpu.RDMAEngine.ToOutside
		t.tiles = append(t.tiles, tile)
	}
	for blk, tile := range t.blocks {
		if _, ok := ports[tile]; !ok {
			panic(fmt.Sprintf("-place-table: block %d -> %d is not a GPM tile",
				blk, tile))
		}
	}
	var sharedTable mem.LowModuleFinder
	for _, gpu := range r.platform.GPUs {
		self := pgclsTile(gpu.RDMAEngine.Name())
		def, ok := gpu.RDMAEngine.LocalModuleFinder().(*mem.InterleavedLowModuleFinder)
		if !ok || !def.UseAddressSpaceLimitation {
			panic("-place-table: unexpected RDMA local module finder")
		}
		local := *def
		local.UseAddressSpaceLimitation = false
		local.ModuleForOtherAddresses = nil
		l1 := &placeL1Finder{t: t, self: self, local: &local,
			rdma: def.ModuleForOtherAddresses, def: def}
		n := 0
		for _, group := range [][]TraceableComponent{gpu.L1VCaches, gpu.L1SCaches} {
			for _, c := range group {
				s, ok := c.(lowModuleFinderSetter)
				if !ok {
					panic("-place-table: L1 cache without SetLowModuleFinder")
				}
				s.SetLowModuleFinder(l1)
				n++
			}
		}
		if n == 0 {
			panic("-place-table: no L1 caches")
		}
		gpu.RDMAEngine.SetLocalModuleFinder(&placeOwnerFinder{t: t, self: self,
			local: &local, def: def})
		if sharedTable == nil {
			sharedTable = gpu.RDMAEngine.RemoteRDMAAddressTable
		} else if gpu.RDMAEngine.RemoteRDMAAddressTable != sharedTable {
			panic("-place-table: RDMA address tables are not shared")
		}
	}
	at := &placeAddrTable{t: t, ports: ports, def: sharedTable}
	for _, gpu := range r.platform.GPUs {
		gpu.RDMAEngine.RemoteRDMAAddressTable = at
	}
	r.place = t
	fmt.Printf("[Config] placement: table=%s blocks=%d random-seed=%d gpms=%d\n",
		*placeTableFlag, len(t.blocks), t.seed, len(ports))
}

func (r *Runner) reportPlacement() {
	if r.place == nil {
		return
	}
	t := r.place
	fmt.Printf("[placement] l1_to_local=%d l1_to_remote=%d owner_side=%d misrouted=%d\n",
		t.l1Local, t.l1Remote, t.ownerLocal, t.misrouted)
	fh, err := os.Create(*filenameFlag + "_place_stats.csv")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[placement] %v\n", err)
		return
	}
	defer fh.Close()
	fmt.Fprintf(fh, "key,value\nblocks,%d\nl1_to_local,%d\nl1_to_remote,%d\n"+
		"owner_side,%d\nmisrouted,%d\n", len(t.blocks), t.l1Local, t.l1Remote,
		t.ownerLocal, t.misrouted)
}
