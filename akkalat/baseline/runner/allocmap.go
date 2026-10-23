package runner

// P21 offline analysis helper: -dump-alloc-map writes the page table (one
// line per 4 KiB page of process 1: virtual page, physical page, device and
// allocation id = driver PageBlock) right before the first kernel launch and
// exits. Statistics only; nothing is simulated.

import (
	"bufio"
	"flag"
	"fmt"
	"os"

	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/mgpusim/v3/driver"
)

var dumpAllocMapFlag = flag.String("dump-alloc-map", "",
	"P21: write '<vpn> <ppn> <device> <alloc id>' for every mapped page of "+
		"process 1 to this file before the first kernel launch, then exit.")

func (r *Runner) configureAllocMapDump() {
	r.platform.Driver.SetBeforeFirstKernelLaunchHook(
		func(driver.AllocationStatsSnapshot) {
			if err := dumpAllocMap(r.platform.PageTable, *dumpAllocMapFlag); err != nil {
				panic(err)
			}
			os.Exit(0)
		})
}

func dumpAllocMap(pt vm.PageTable, path string) error {
	fh, err := os.Create(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	w := bufio.NewWriter(fh)
	defer w.Flush()
	fmt.Fprintln(w, "vpn ppn dev block")
	const pageSize = 4096
	missing := 0
	n := 0
	for va := uint64(pageSize); missing < 1<<16; va += pageSize {
		p, ok := pt.Find(1, va)
		if !ok || p.VAddr != va {
			missing++
			continue
		}
		missing = 0
		n++
		fmt.Fprintf(w, "%d %d %d %d\n", va>>12, p.PAddr>>12, p.DeviceID, p.PageBlock)
	}
	fmt.Printf("[alloc-map] %d pages -> %s\n", n, path)
	return nil
}
