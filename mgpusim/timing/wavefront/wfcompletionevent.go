package wavefront

import "github.com/sarchlab/akita/v3/sim"

// WfCompletionEvent lets sampled timing paths complete a wavefront without
// driving it through the detailed CU pipeline.
type WfCompletionEvent struct {
	*sim.EventBase
	Wf *Wavefront
}

// NewWfCompletionEvent creates a wavefront completion event.
func NewWfCompletionEvent(
	time sim.VTimeInSec,
	handler sim.Handler,
	wf *Wavefront,
) *WfCompletionEvent {
	return &WfCompletionEvent{
		EventBase: sim.NewEventBase(time, handler),
		Wf:        wf,
	}
}
