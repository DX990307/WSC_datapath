package runner

import (
	"fmt"
	"sync"

	"github.com/sarchlab/akita/v3/mem/dram"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
)

// windowedPhysicalDRAMObserver keeps the O3 raw trace bounded while always
// forwarding architectural DRAM boundaries to the O1 path tracer. Selection
// is by physical read subtransaction, using the mapper-produced identity.
type windowedPhysicalDRAMObserver struct {
	mu sync.Mutex

	csv    *dram.PhysicalDRAMCSVObserver
	warmup uint64
	max    uint64
	seen   uint64
	kept   uint64

	selected map[string]struct{}
	closed   bool
}

func (o *windowedPhysicalDRAMObserver) ObservePhysicalDRAM(
	e dram.PhysicalDRAMEvent,
) {
	// These calls operate on immutable event snapshots and are independent of
	// the bounded O3 selection below.
	now := sim.VTimeInSec(float64(e.TimePS) * 1e-12)
	switch e.Kind {
	case dram.PhysicalDRAMSubtransactionArrive:
		memtrace.ObservationTransitionByRequest(
			e.RequestID, "dram_subtransaction_arrive",
			"dram_queue_service", now)
		memtrace.MarkObservationSource(
			e.RequestID, "dram", e.ControllerName)
	case dram.PhysicalDRAMTransactionComplete:
		memtrace.ObservationTransitionByRequest(
			e.RequestID, "dram_transaction_complete", "dram_to_l2", now)
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.csv == nil {
		return
	}

	selected := false
	if e.Kind == dram.PhysicalDRAMSubtransactionArrive && e.IsRead {
		o.seen++
		if o.seen > o.warmup && (o.max == 0 || o.kept < o.max) {
			o.kept++
			o.selected[e.SubtransactionID] = struct{}{}
			selected = true
		}
	} else {
		_, selected = o.selected[e.SubtransactionID]
	}
	if selected {
		o.csv.ObservePhysicalDRAM(e)
	}
	if selected && e.Kind == dram.PhysicalDRAMTransactionComplete {
		delete(o.selected, e.SubtransactionID)
	}
}

func (o *windowedPhysicalDRAMObserver) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	o.closed = true
	if o.csv == nil {
		return nil
	}
	return o.csv.Close()
}

func (r *Runner) attachObservationDRAMObserver() {
	if !*observationTracing || !r.Timing {
		return
	}
	prefix := *observationTraceFile
	if prefix == "" {
		prefix = *filenameFlag + "_observation"
	}
	csvObserver, err := dram.NewPhysicalDRAMCSVObserver(
		prefix+"_dram_physical.csv.gz",
		prefix+"_dram_locality.csv",
	)
	if err != nil {
		panic(err)
	}
	observer := &windowedPhysicalDRAMObserver{
		csv:      csvObserver,
		warmup:   *observationDRAMWarmupAccesses,
		max:      *observationDRAMMaxRecords,
		selected: make(map[string]struct{}),
	}
	r.observationDRAM = observer

	for _, gpu := range r.platform.GPUs {
		owner := fmt.Sprintf("GPU[%d]", gpu.GPUID)
		for controllerIndex, component := range gpu.MemControllers {
			controller, ok := component.(*dram.MemController)
			if !ok {
				continue
			}
			controller.SetPhysicalDRAMObserver(
				observer, owner, controllerIndex)
		}
	}
}
