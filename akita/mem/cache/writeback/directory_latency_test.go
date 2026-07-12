package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func TestCacheTicksDirectoryPipelineOnlyOncePerCycle(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(4 * 64).
		WithWayAssociativity(2).
		WithNumReqPerCycle(16).
		WithDirectoryLatency(10).
		WithRemoteReplicaFilter(true).
		Build("L2")

	pid := vm.PID(1)
	line := uint64(0x1000)
	if !cache.remoteReplicaFilter.Insert(pid, line) {
		t.Fatal("could not seed filter false positive")
	}
	req := mem.ReadReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithByteSize(64).
		WithLookupOnly().
		Build()
	if err := cache.topPort.Recv(req); err != nil {
		t.Fatal(err)
	}
	if !cache.topParser.Tick(1) {
		t.Fatal("top parser did not accept request")
	}

	// One cache cycle must advance the 10-stage directory pipeline only one
	// stage, even though its throughput width is 16 requests/cycle.
	cache.runPipeline(2)
	if len(cache.inFlightTransactions) != 1 {
		t.Fatal("directory lookup incorrectly completed in one cycle")
	}
	for cycle := 3; cycle <= 11; cycle++ {
		cache.runPipeline(sim.VTimeInSec(cycle))
	}
	if len(cache.inFlightTransactions) != 1 {
		t.Fatal("directory lookup completed before its configured latency")
	}
	cache.runPipeline(12)
	if len(cache.inFlightTransactions) != 0 {
		t.Fatal("directory lookup did not complete after 10 pipeline cycles")
	}
}
