package runner

import (
	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/mem/vm/gmmu"
	"github.com/sarchlab/akita/v3/mem/vm/l2tlb"
	"github.com/sarchlab/akita/v3/mem/vm/mmu"
	"github.com/sarchlab/akita/v3/mem/vm/mmuTLB"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/timing/pagemigration"
	"github.com/sarchlab/mgpusim/v3/timing/pagemigrationcontroller"
	"github.com/sarchlab/mgpusim/v3/timing/rdma"
)

// TraceableComponent is a component that can accept traces
type TraceableComponent interface {
	sim.Component
	tracing.NamedHookable
}

// A Platform is a collection of the hardware under simulation.
type Platform struct {
	Engine   sim.Engine
	Driver   *driver.Driver
	GPUs     []*GPU
	IOMMUTLB *mmuTLB.TLB
	// RemoteRetain is the A1 home write tracker (nil when A1 is off).
	RemoteRetain *writeback.RemoteRetainTracker

	// PageMigration is nil unless -page-migration is set.
	PageMigration *pagemigration.Manager
	// Holders is the R4 holder table (nil unless -l2res-holders).
	Holders *writeback.HolderTable

	// PageTable is the global page table (P21 -dump-alloc-map).
	PageTable vm.PageTable
}

// A GPU is a collection of GPU internal Components
type GPU struct {
	GPUID  uint64
	Domain *sim.Domain
	// CommandProcessor *cp.CommandProcessor
	CommandProcessor TraceableComponent
	RDMAEngine       *rdma.Comp
	MMUEngine        *mmu.MMU
	GMMUEngine       *gmmu.GMMU
	L2TLB            *l2tlb.L2TLB
	// L2TLB *tlb.TLB

	PMC            *pagemigrationcontroller.PageMigrationController
	CUs            []TraceableComponent
	L1VCaches      []TraceableComponent
	L1SCaches      []TraceableComponent
	L1ICaches      []TraceableComponent
	L2Caches       []TraceableComponent
	L1VTLBs        []TraceableComponent
	L1STLBs        []TraceableComponent
	L1ITLBs        []TraceableComponent
	L2TLBs         []TraceableComponent
	MemControllers []TraceableComponent

	// pm holds what page migration needs; nil when it is off.
	pm *gpmPageMigrationParts
	// L2TLB      []TraceableComponent
	// MMUEngine      []TraceableComponent
	// GMMUEngine     []TraceableComponent

}
