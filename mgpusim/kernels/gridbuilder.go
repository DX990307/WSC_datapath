package kernels

import "github.com/sarchlab/mgpusim/v3/insts"

// WGFilterFunc is a filter
type WGFilterFunc func(
	*HsaKernelDispatchPacket,
	*WorkGroup,
) bool

// KernelLaunchInfo includes the necessary information to launch a kernel.
type KernelLaunchInfo struct {
	CodeObject *insts.HsaCo
	Packet     *HsaKernelDispatchPacket
	PacketAddr uint64
	WGFilter   WGFilterFunc
}

// A GridBuilder is the unit that can build a grid and its internal structure
// from a kernel and its launch parameters.
type GridBuilder interface {
	SetKernel(info KernelLaunchInfo)
	NumWG() int
	NextWG() *WorkGroup
	Skip(n int)
}

// NewGridBuilder creates a default grid builder
func NewGridBuilder() GridBuilder {
	return &gridBuilderImpl{}
}

type gridBuilderImpl struct {
	hsaco      *insts.HsaCo
	packet     *HsaKernelDispatchPacket
	filter     WGFilterFunc
	packetAddr uint64
	numWG      int

	xid, yid, zid int
}

func (b *gridBuilderImpl) SetKernel(
	info KernelLaunchInfo,
) {
	b.hsaco = info.CodeObject
	b.packet = info.Packet
	b.packetAddr = info.PacketAddr
	b.filter = info.WGFilter
	b.xid = 0
	b.yid = 0
	b.zid = 0

	b.countWG()
}

func (b *gridBuilderImpl) Skip(n int) {
	for i := 0; i < n; i++ {
		b.NextWG()
	}
}

func (b *gridBuilderImpl) countWG() {
	x := int(b.packet.GridSizeX-1)/int(b.packet.WorkgroupSizeX) + 1
	y := int(b.packet.GridSizeY-1)/int(b.packet.WorkgroupSizeY) + 1
	z := int(b.packet.GridSizeZ-1)/int(b.packet.WorkgroupSizeZ) + 1

	if b.filter == nil {
		b.numWG = x * y * z
		return
	}

	b.numWG = 0
	// The filter is a predicate and does not own the WorkGroup. Reuse one
	// zeroed descriptor so counting a large sampled grid does not allocate one
	// temporary object per candidate WG.
	wg := WorkGroup{}
	for i := 0; i < x; i++ {
		for j := 0; j < y; j++ {
			for k := 0; k < z; k++ {
				wg = WorkGroup{
					IDX: i,
					IDY: j,
					IDZ: k,
				}

				if b.filter(b.packet, &wg) {
					b.numWG++
				}
			}
		}
	}
}

func (b *gridBuilderImpl) NumWG() int {
	return b.numWG
}

func (b *gridBuilderImpl) NextWG() *WorkGroup {
	wg := NewWorkGroup()

	for {
		xLeft := int(b.packet.GridSizeX) - b.xid*int(b.packet.WorkgroupSizeX)
		yLeft := int(b.packet.GridSizeY) - b.yid*int(b.packet.WorkgroupSizeY)
		zLeft := int(b.packet.GridSizeZ) - b.zid*int(b.packet.WorkgroupSizeZ)

		if xLeft <= 0 || yLeft <= 0 || zLeft <= 0 {
			return nil
		}

		xToAllocate := min(xLeft, int(b.packet.WorkgroupSizeX))
		yToAllocate := min(yLeft, int(b.packet.WorkgroupSizeY))
		zToAllocate := min(zLeft, int(b.packet.WorkgroupSizeZ))

		wg.Packet = b.packet
		wg.CodeObject = b.hsaco
		wg.SizeX = int(b.packet.WorkgroupSizeX)
		wg.SizeY = int(b.packet.WorkgroupSizeY)
		wg.SizeZ = int(b.packet.WorkgroupSizeZ)
		wg.IDX = b.xid
		wg.IDY = b.yid
		wg.IDZ = b.zid
		wg.CurrSizeX = xToAllocate
		wg.CurrSizeY = yToAllocate
		wg.CurrSizeZ = zToAllocate

		b.xid++
		xLeft -= xToAllocate
		if xLeft <= 0 {
			b.xid = 0
			b.yid++
			yLeft -= yToAllocate
			if yLeft <= 0 {
				b.yid = 0
				b.zid++
			}
		}

		if b.filter == nil {
			break
		}

		if b.filter(b.packet, wg) {
			break
		}
	}

	b.spawnWorkItems(wg)
	b.formWavefronts(wg)

	return wg
}

func (b *gridBuilderImpl) spawnWorkItems(wg *WorkGroup) {
	for z := 0; z < wg.CurrSizeZ; z++ {
		for y := 0; y < wg.CurrSizeY; y++ {
			for x := 0; x < wg.CurrSizeX; x++ {
				wi := new(WorkItem)
				wi.WG = wg
				wi.IDX = x
				wi.IDY = y
				wi.IDZ = z
				wg.WorkItems = append(wg.WorkItems, wi)
			}
		}
	}
}

func (b *gridBuilderImpl) formWavefronts(wg *WorkGroup) {
	var wf *Wavefront
	wavefrontSize := 64
	for i, wi := range wg.WorkItems {
		wg := wi.WG
		inWGID := wi.IDZ*wg.SizeX*wg.SizeY + wi.IDY*wg.SizeX + wi.IDX
		if inWGID%wavefrontSize == 0 {
			wf = NewWavefront()
			wf.FirstWiFlatID = wg.WorkItems[i].FlattenedID()
			wf.CodeObject = b.hsaco
			wf.Packet = b.packet
			wf.PacketAddress = b.packetAddr
			wf.WG = wg
			wg.Wavefronts = append(wg.Wavefronts, wf)
		}
		wf.WorkItems = append(wf.WorkItems, wi)
		wf.InitExecMask |= 1 << uint32(inWGID%wavefrontSize)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// BuildWG builds the work-group with the given flattened ID (x fastest,
// then y, then z; the order in which NextWG walks the grid), with its work
// items and wavefronts, exactly as NextWG would. Used by T19 WG stealing,
// where a GPM dispatches WGs by ID from a dynamic queue.
func BuildWG(info KernelLaunchInfo, flatID int) *WorkGroup {
	pkt := info.Packet
	nx := int(pkt.GridSizeX-1)/int(pkt.WorkgroupSizeX) + 1
	ny := int(pkt.GridSizeY-1)/int(pkt.WorkgroupSizeY) + 1
	b := &gridBuilderImpl{
		hsaco:      info.CodeObject,
		packet:     pkt,
		packetAddr: info.PacketAddr,
	}
	wg := NewWorkGroup()
	wg.Packet = pkt
	wg.CodeObject = info.CodeObject
	wg.SizeX = int(pkt.WorkgroupSizeX)
	wg.SizeY = int(pkt.WorkgroupSizeY)
	wg.SizeZ = int(pkt.WorkgroupSizeZ)
	wg.IDX = flatID % nx
	wg.IDY = (flatID / nx) % ny
	wg.IDZ = flatID / (nx * ny)
	wg.CurrSizeX = min(int(pkt.GridSizeX)-wg.IDX*wg.SizeX, wg.SizeX)
	wg.CurrSizeY = min(int(pkt.GridSizeY)-wg.IDY*wg.SizeY, wg.SizeY)
	wg.CurrSizeZ = min(int(pkt.GridSizeZ)-wg.IDZ*wg.SizeZ, wg.SizeZ)
	b.spawnWorkItems(wg)
	b.formWavefronts(wg)
	return wg
}
