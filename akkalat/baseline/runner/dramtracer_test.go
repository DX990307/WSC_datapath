package runner

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

type fixedTimeTeller struct{ now sim.VTimeInSec }

func (f *fixedTimeTeller) CurrentTime() sim.VTimeInSec { return f.now }

func TestDramTracerIgnoresSubTransactionTasks(t *testing.T) {
	clock := &fixedTimeTeller{}
	tracer := newDramTracer(clock)

	read := mem.ReadReqBuilder{}.WithAddress(0x40).WithByteSize(64).Build()
	tracer.StartTask(tracing.Task{ID: "req", Kind: "req_in",
		What: "*mem.ReadReq", Detail: read})
	// The memory controller starts sub-transaction tasks that a bank ends,
	// so the controller-side tracer never sees their end.
	tracer.StartTask(tracing.Task{ID: "sub", Kind: "sub-trans", What: "sub-trans"})
	if len(tracer.inflightTasks) != 1 {
		t.Fatalf("tracer holds %d tasks, want only the request", len(tracer.inflightTasks))
	}

	clock.now = 10e-9
	tracer.EndTask(tracing.Task{ID: "req"})
	if tracer.readCount != 1 || tracer.readSize != 64 || tracer.readAvgLatency != 10e-9 {
		t.Fatalf("read stats = %d reads, %d B, %v", tracer.readCount,
			tracer.readSize, tracer.readAvgLatency)
	}
	if len(tracer.inflightTasks) != 0 {
		t.Fatal("finished request stayed in the tracer")
	}
}
