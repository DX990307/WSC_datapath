package runner

import (
	"fmt"

	"github.com/sarchlab/akita/v3/tracing"
	"github.com/tebeka/atexit"
)

// wgTracer can trace the number of instruction completed.
type wgTracer struct {
	count    uint64
	maxCount uint64

	inflightInst map[string]tracing.Task
}

// newWGStopper creates one tracer shared by every CU. Its count is therefore
// the number of workgroups completed across all GPUs.
func newWGStopper(maxWG uint64) *wgTracer {
	return &wgTracer{
		maxCount:     maxWG,
		inflightInst: map[string]tracing.Task{},
	}
}

// newWGTracer creates a tracer that can count the number of instructions.
func newWGTracer() *wgTracer {
	t := &wgTracer{
		inflightInst: map[string]tracing.Task{},
	}
	return t
}

func (t *wgTracer) StartTask(task tracing.Task) {
	if task.Kind != "req_in" {
		return
	}

	if task.What != "*protocol.WGCompletionMsg" {
		return
	}

	if _, exists := t.inflightInst[task.ID]; exists {
		return
	}

	// fmt.Printf("SIMD instruction started: %s\n", task.ID)

	t.inflightInst[task.ID] = task
}

func (t *wgTracer) StepTask(task tracing.Task) {
	// Do nothing
}

func (t *wgTracer) EndTask(task tracing.Task) {
	_, found := t.inflightInst[task.ID]
	if !found {
		return
	}

	delete(t.inflightInst, task.ID)

	t.count++
	if t.maxCount > 0 && t.count == t.maxCount {
		fmt.Printf("[Runner] reached max-wg=%d completed_wg=%d\n",
			t.maxCount, t.count)
		atexit.Exit(0)
	}
}
