package runner

import (
	"testing"

	"github.com/sarchlab/akita/v3/tracing"
)

func TestWGTracerCountsOnlyCompletedWorkgroups(t *testing.T) {
	tracer := newWGTracer()

	mapTask := tracing.Task{
		ID: "map", Kind: "req_in", What: "*protocol.MapWGReq",
	}
	tracer.StartTask(mapTask)
	tracer.EndTask(mapTask)
	if tracer.count != 0 {
		t.Fatalf("mapped workgroup counted as completed: got %d", tracer.count)
	}

	completion := tracing.Task{
		ID: "complete", Kind: "req_in", What: "*protocol.WGCompletionMsg",
	}
	tracer.StartTask(completion)
	tracer.EndTask(completion)
	if tracer.count != 1 {
		t.Fatalf("completed workgroup count = %d, want 1", tracer.count)
	}
}
