package trace

// ISCA27 wg-to-data pass 1: per (kernel, flattened WG, owner device) access
// counts of translated vector-memory demands (at the address translator, so
// L1 hits are included). Statistics only; off by default.

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"sync"
	"sync/atomic"
)

type wgAffKey struct {
	kernel int32
	wg     uint32
	owner  int32
}

type wgAffVal struct {
	reads, writes, bytes uint64
}

var wgAffEnabled uint32
var wgAffMu sync.Mutex
var wgAffData = map[wgAffKey]*wgAffVal{}

// EnableWGAffinity turns the statistics on.
func EnableWGAffinity() { atomic.StoreUint32(&wgAffEnabled, 1) }

// WGAffinityEnabled is a cheap gate.
func WGAffinityEnabled() bool { return atomic.LoadUint32(&wgAffEnabled) != 0 }

// RecordWGAffinity counts one translated demand.
func RecordWGAffinity(kernel int, wg uint64, owner int, isWrite bool, bytes uint64) {
	if kernel < 0 {
		return
	}
	k := wgAffKey{int32(kernel), uint32(wg), int32(owner)}
	wgAffMu.Lock()
	v := wgAffData[k]
	if v == nil {
		v = &wgAffVal{}
		wgAffData[k] = v
	}
	if isWrite {
		v.writes++
	} else {
		v.reads++
	}
	v.bytes += bytes
	wgAffMu.Unlock()
}

// WriteWGAffinity writes "kernel,wg,owner_dev,reads,writes,bytes" rows.
func WriteWGAffinity(path string) error {
	wgAffMu.Lock()
	defer wgAffMu.Unlock()
	keys := make([]wgAffKey, 0, len(wgAffData))
	for k := range wgAffData {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.kernel != b.kernel {
			return a.kernel < b.kernel
		}
		if a.wg != b.wg {
			return a.wg < b.wg
		}
		return a.owner < b.owner
	})
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)
	fmt.Fprintln(w, "kernel,wg,owner_dev,reads,writes,bytes")
	for _, k := range keys {
		v := wgAffData[k]
		fmt.Fprintf(w, "%d,%d,%d,%d,%d,%d\n",
			k.kernel, k.wg, k.owner, v.reads, v.writes, v.bytes)
	}
	return w.Flush()
}
