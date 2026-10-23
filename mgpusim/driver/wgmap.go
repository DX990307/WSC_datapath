package driver

// ISCA27 wg-to-data: optional per-kernel WG -> GPM dispatch map.
//
// Unified multi-GPU launches are numbered 0, 1, ... in launch order. Without a
// map (the default) the driver keeps distributeWGToGPUs' contiguous blocks.
// With a map, kernel k's flattened WG w runs on GPM index m[k][w] (position in
// UnifiedGPUIDs). A kernel with no entry, or whose WG count differs from the
// map, falls back to the default distribution.

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/sarchlab/mgpusim/v3/kernels"
)

// UnifiedLaunchInfo describes one unified launch (for statistics).
type UnifiedLaunchInfo struct {
	Index   int
	TotalWG int
	Mapped  bool
	PerGPM  []int // WGs assigned to each GPM index
}

type wgMapState struct {
	mu       sync.Mutex
	next     int
	m        map[int][]int32
	launches []UnifiedLaunchInfo
}

// LoadWGMap reads a map file: one line per kernel,
// "<kernel> <totalWG> <gpm of WG 0> <gpm of WG 1> ...". '#' starts a comment.
func LoadWGMap(path string) (map[int][]int32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[int][]int32{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<30)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		fs := strings.Fields(line)
		if len(fs) < 2 {
			return nil, fmt.Errorf("wg-map: bad line %q", line)
		}
		k, err := strconv.Atoi(fs[0])
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(fs[1])
		if err != nil {
			return nil, err
		}
		if len(fs)-2 != n {
			return nil, fmt.Errorf("wg-map: kernel %d has %d entries, want %d",
				k, len(fs)-2, n)
		}
		v := make([]int32, n)
		for i := 0; i < n; i++ {
			g, err := strconv.Atoi(fs[2+i])
			if err != nil {
				return nil, err
			}
			v[i] = int32(g)
		}
		out[k] = v
	}
	return out, sc.Err()
}

// SetWGMap installs a WG -> GPM dispatch map (nil = default distribution).
func (d *Driver) SetWGMap(m map[int][]int32) {
	d.wgMap.mu.Lock()
	d.wgMap.m = m
	d.wgMap.mu.Unlock()
}

// UnifiedLaunches returns the per-launch statistics so far.
func (d *Driver) UnifiedLaunches() []UnifiedLaunchInfo {
	d.wgMap.mu.Lock()
	defer d.wgMap.mu.Unlock()
	return append([]UnifiedLaunchInfo(nil), d.wgMap.launches...)
}

// nextUnifiedLaunch numbers a launch, registers its packets and returns the
// WG -> GPM map to use (nil = default contiguous distribution).
func (d *Driver) nextUnifiedLaunch(
	cmd *LaunchUnifiedMultiGPUKernelCommand,
	numGPM int,
	wgDist []int,
) []int32 {
	pkt := cmd.PacketArray[0]
	numWGX := (pkt.GridSizeX-1)/uint32(pkt.WorkgroupSizeX) + 1
	numWGY := (pkt.GridSizeY-1)/uint32(pkt.WorkgroupSizeY) + 1
	numWGZ := (pkt.GridSizeZ-1)/uint32(pkt.WorkgroupSizeZ) + 1
	total := int(numWGX * numWGY * numWGZ)

	d.wgMap.mu.Lock()
	defer d.wgMap.mu.Unlock()
	k := d.wgMap.next
	d.wgMap.next++
	for _, p := range cmd.PacketArray {
		kernels.RegisterLaunchIndex(p, k)
	}

	info := UnifiedLaunchInfo{Index: k, TotalWG: total, PerGPM: make([]int, numGPM)}
	var m []int32
	if d.wgMap.m != nil {
		if v, ok := d.wgMap.m[k]; ok {
			if len(v) == total {
				m = v
				for _, g := range v {
					if g < 0 || int(g) >= numGPM {
						m = nil
						break
					}
				}
			} else {
				fmt.Printf("[wg-map] kernel %d: map has %d WGs, launch has %d; default\n",
					k, len(v), total)
			}
		}
	}
	if m != nil {
		info.Mapped = true
		for _, g := range m {
			info.PerGPM[g]++
		}
	} else {
		for i := 0; i < numGPM; i++ {
			info.PerGPM[i] = wgDist[i+1] - wgDist[i]
		}
	}
	d.wgMap.launches = append(d.wgMap.launches, info)
	return m
}
