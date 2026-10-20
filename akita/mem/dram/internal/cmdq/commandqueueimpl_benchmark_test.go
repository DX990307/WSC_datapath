package cmdq

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

type alwaysReadyBenchmarkChannel struct{}

func (alwaysReadyBenchmarkChannel) GetReadyCommand(
	_ sim.VTimeInSec,
	cmd *signal.Command,
) *signal.Command {
	return cmd
}

func (alwaysReadyBenchmarkChannel) StartCommand(
	sim.VTimeInSec, *signal.Command,
) {
}

func (alwaysReadyBenchmarkChannel) UpdateTiming(
	sim.VTimeInSec, *signal.Command,
) {
}

func (alwaysReadyBenchmarkChannel) Tick(sim.VTimeInSec) bool { return false }

func BenchmarkRowContinuationCommandSelection(b *testing.B) {
	const depth = 64
	queue := make(Queue, 0, depth)
	for i := 0; i < depth; i++ {
		queue = append(queue, &signal.Command{
			ID:         string(rune(i + 1)),
			Kind:       signal.CmdKindReadPrecharge,
			EnqueuedAt: sim.VTimeInSec(i),
		})
	}
	q := CommandQueueImpl{
		Queues:           []Queue{queue},
		CapacityPerQueue: depth,
		Channel:          alwaysReadyBenchmarkChannel{},
		RowContinuation:  true,
		Freq:             sim.GHz,
	}

	// Warm the reusable candidate storage before measuring steady state.
	issued := q.GetCommandToIssue(100)
	issued.Kind = signal.CmdKindReadPrecharge
	q.Queues[0] = append(q.Queues[0], issued)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		issued = q.GetCommandToIssue(100)
		issued.Kind = signal.CmdKindReadPrecharge
		q.Queues[0] = append(q.Queues[0], issued)
	}
}
