package driver

import (
	"github.com/sarchlab/akita/v3/sim"
)

// PhysicalWriteHook is told about every change of physical memory content
// the driver makes outside the GPU caches: host-to-device copies (magic and
// DMA) and the release or remapping of physical pages. A1 (cross-kernel
// retention of remote replica lines) uses it to treat such pages as written.
type PhysicalWriteHook func(now sim.VTimeInSec, pAddr, size uint64)

// AddPhysicalWriteHook registers a hook. No hook is installed by default.
func (d *Driver) AddPhysicalWriteHook(hook PhysicalWriteHook) {
	d.physicalWriteHooks = append(d.physicalWriteHooks, hook)
}

func (d *Driver) notePhysicalWrite(now sim.VTimeInSec, pAddr, size uint64) {
	for _, hook := range d.physicalWriteHooks {
		hook(now, pAddr, size)
	}
}

// notePhysicalRangeOfVirtual reports the physical pages currently backing
// [vAddr, vAddr+size) of pid as written (used before they are freed or
// remapped).
func (d *Driver) notePhysicalRangeOfVirtual(
	ctx *Context, vAddr, size uint64,
) {
	if len(d.physicalWriteHooks) == 0 || size == 0 {
		return
	}
	pageSize := uint64(1) << d.Log2PageSize
	now := d.Engine.CurrentTime()
	start := vAddr &^ (pageSize - 1)
	for addr := start; addr < vAddr+size; addr += pageSize {
		page, found := d.pageTable.Find(ctx.pid, addr)
		if !found {
			continue
		}
		d.notePhysicalWrite(now, page.PAddr, page.PageSize)
	}
}
