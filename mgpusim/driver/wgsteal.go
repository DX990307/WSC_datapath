package driver

import (
	"fmt"

	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
	"github.com/sarchlab/mgpusim/v3/protocol"
	"github.com/sarchlab/mgpusim/v3/wgsteal"
)

// T19 WG stealing (off unless the runner calls SetWGSteal).

// SetWGSteal turns on T19 WG stealing for unified multi-GPM kernels.
func (d *Driver) SetWGSteal(c *wgsteal.Coordinator) {
	d.wgSteal = c
}

// WGSteal returns the T19 coordinator (nil when off).
func (d *Driver) WGSteal() *wgsteal.Coordinator {
	return d.wgSteal
}

// SetWGGroupHint lets a benchmark tell T19 which WGs of a kernel form a
// group (e.g. the WGs of one MoE expert). Without a hint, groups are runs
// of consecutive WG IDs. The hint applies to launches of co enqueued after
// the call. It has no effect when T19 is off.
func (d *Driver) SetWGGroupHint(co *insts.HsaCo, fn func(flat int) int) {
	if d.wgGroupHints == nil {
		d.wgGroupHints = map[*insts.HsaCo]func(int) int{}
	}
	d.wgGroupHints[co] = fn
}

func (d *Driver) processStealLaunch(
	now sim.VTimeInSec,
	cmd *LaunchUnifiedMultiGPUKernelCommand,
	queue *CommandQueue,
) bool {
	dev := d.devices[queue.GPUID]
	cfg := d.wgSteal.Config()

	var wgDist []int
	if cfg.InitialSplit == "balanced" {
		pkt := cmd.PacketArray[0]
		numCUs := make([]int, len(dev.UnifiedGPUIDs))
		for i, id := range dev.UnifiedGPUIDs {
			numCUs[i] = d.devices[id].Properties.CUCount
		}
		total := totalWGsOf(pkt)
		wgDist = wgsteal.Split("balanced", total, numCUs)
		fmt.Printf("total WG: %d T19 balanced split over %d GPMs\n",
			total, len(dev.UnifiedGPUIDs))
	} else {
		wgDist = d.distributeWGToGPUs(queue, cmd)
	}

	members := make([]int, len(dev.UnifiedGPUIDs))
	for i, id := range dev.UnifiedGPUIDs {
		members[i] = id - 1
	}
	name := ""
	if cmd.CodeObject != nil && cmd.CodeObject.Symbol != nil {
		name = cmd.CodeObject.Symbol.Name
	}
	var groupFn wgsteal.GroupFunc
	if cmd.wgGroupHint != nil {
		groupFn = wgsteal.GroupFunc(cmd.wgGroupHint)
	}
	launch := d.wgSteal.NewLaunch(name, members, wgDist, groupFn, now)

	// Every GPM gets the launch, including those with an empty static
	// range: they can steal.
	for i, gpuID := range dev.UnifiedGPUIDs {
		req := protocol.NewLaunchKernelReq(now, d.gpuPort, d.GPUs[gpuID-1])
		req.PID = queue.Context.pid
		req.HsaCo = cmd.CodeObject
		req.Packet = cmd.PacketArray[i]
		req.PacketAddress = uint64(cmd.DPacketArray[i])
		req.WGFilter = staticWGFilter(wgDist[i], wgDist[i+1])
		req.WGSteal = launch

		queue.IsRunning = true
		cmd.Reqs = append(cmd.Reqs, req)

		d.requestsToSend = append(d.requestsToSend, req)

		queue.Context.l2Dirty = true
		queue.Context.markAllBuffersDirty()

		d.logTaskToGPUInitiate(now, cmd, req)
	}

	return true
}

func totalWGsOf(pkt *kernels.HsaKernelDispatchPacket) int {
	nx := (pkt.GridSizeX-1)/uint32(pkt.WorkgroupSizeX) + 1
	ny := (pkt.GridSizeY-1)/uint32(pkt.WorkgroupSizeY) + 1
	nz := (pkt.GridSizeZ-1)/uint32(pkt.WorkgroupSizeZ) + 1
	return int(nx * ny * nz)
}

// staticWGFilter describes a GPM's static range (T19 dispatchers do not use
// it; tracers read it).
func staticWGFilter(lo, hi int) kernels.WGFilterFunc {
	return func(pkt *kernels.HsaKernelDispatchPacket, wg *kernels.WorkGroup) bool {
		numWGX := (pkt.GridSizeX-1)/uint32(pkt.WorkgroupSizeX) + 1
		numWGY := (pkt.GridSizeY-1)/uint32(pkt.WorkgroupSizeY) + 1
		flat := wg.IDZ*int(numWGX)*int(numWGY) + wg.IDY*int(numWGX) + wg.IDX
		return flat >= lo && flat < hi
	}
}
