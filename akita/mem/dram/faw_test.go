package dram

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

var _ = Describe("MemController with tFAW and cross-rank tRRD", func() {
	const tRRDS = 7

	var (
		observer *collectingPhysicalDRAMObserver
		memCtrl  *MemController
		agent    *streamAgent
	)

	// run builds the MI200-like HBM2e channel of the refresh tests, without
	// refresh, and keeps up to window reads to address(i) in flight until
	// stopAt.
	run := func(
		b Builder,
		window int,
		stopAt sim.VTimeInSec,
		address func(i uint64) uint64,
	) {
		engine := sim.NewSerialEngine()
		memCtrl = b.
			WithEngine(engine).
			WithFreq(941 * sim.MHz).
			WithTCL(13).WithTCWL(13).
			WithTRCDRD(13).WithTRCDWR(13).WithTRP(13).WithTRAS(32).
			WithTRRDS(tRRDS).WithTRRDL(10).WithTCCDS(2).WithTCCDL(5).
			WithTRTP(6).WithTWR(15).WithTWTRS(10).WithTWTRL(13).
			WithTRTRS(0).
			WithCommandQueueSize(16).
			WithTransactionQueueSize(64).
			WithDualCommandBus(true).
			WithActivateCommandCycles(2).
			WithHBM2Turnaround(true).
			WithTransactionRowHold(true).
			WithPipelinedColumnCommands(true).
			WithGlobalStorage(mem.NewStorage(1 * mem.GB)).
			Build("MemCtrl")
		observer = &collectingPhysicalDRAMObserver{}
		memCtrl.SetPhysicalDRAMObserver(observer, "GPU[0]", 0)

		agent = &streamAgent{window: window, stopAt: stopAt, address: address}
		agent.TickingComponent = sim.NewTickingComponent(
			"Agent", engine, 1*sim.GHz, agent)
		agent.port = sim.NewLimitNumMsgPort(agent, 64, "Agent.Port")
		agent.dst = memCtrl.topPort
		conn := sim.NewDirectConnection("Conn", engine, 1*sim.GHz)
		conn.PlugIn(agent.port, 64)
		conn.PlugIn(memCtrl.topPort, 64)

		agent.TickLater(0)
		Expect(engine.Run()).To(Succeed())
		Expect(agent.inflight).To(BeZero())
	}

	// activates returns the activates of each pseudo channel in issue
	// order.
	activates := func() [][]PhysicalDRAMEvent {
		acts := make([][]PhysicalDRAMEvent, 2)
		for _, e := range observer.events {
			if e.Kind == PhysicalDRAMCommandIssue &&
				e.CommandKind == "Activate" {
				acts[e.Channel] = append(acts[e.Channel], e)
			}
		}
		return acts
	}

	// closeCrossRankActivates counts the consecutive activates of a pseudo
	// channel that go to different stack IDs less than tRRDS apart.
	closeCrossRankActivates := func() (n int) {
		for _, acts := range activates() {
			for i := 1; i < len(acts); i++ {
				if acts[i].Rank != acts[i-1].Rank &&
					acts[i].Cycle-acts[i-1].Cycle < tRRDS {
					n++
				}
			}
		}
		return n
	}

	It("should start at most four activates per pseudo channel in any "+
		"tFAW window", func() {
		const tFAW = 40
		run(pseudoChannelBuilder(nil).WithTFAW(tFAW).WithCrossRankTRRD(true),
			32, 10e-6, consecutiveLines)

		for c, acts := range activates() {
			Expect(len(acts)).To(BeNumerically(">", 500), "PC %d", c)
			for i := 1; i < len(acts); i++ {
				Expect(acts[i].Cycle-acts[i-1].Cycle).
					To(BeNumerically(">=", tRRDS), "PC %d, ACT %d", c, i)
				if i >= 4 {
					Expect(acts[i].Cycle-acts[i-4].Cycle).
						To(BeNumerically(">=", tFAW), "PC %d, ACT %d", c, i)
				}
			}
		}
		for _, channel := range pseudoChannels(memCtrl) {
			Expect(channel.FAWDeniedActivates).To(BeNumerically(">", 0))
		}
	})

	It("should space activates to the two stack IDs only with cross-rank "+
		"tRRD", func() {
		run(pseudoChannelBuilder(nil), 32, 10e-6, consecutiveLines)
		Expect(closeCrossRankActivates()).To(BeNumerically(">", 0))
		for _, channel := range pseudoChannels(memCtrl) {
			Expect(channel.FAWDeniedActivates).To(BeZero())
		}

		run(pseudoChannelBuilder(nil).WithCrossRankTRRD(true),
			32, 10e-6, consecutiveLines)
		Expect(closeCrossRankActivates()).To(BeZero())
	})

	It("should start the activate that the window held back on an idle "+
		"controller", func() {
		// Five lines in five banks of pseudo channel 0; the fifth activate
		// waits for the window long after the first four reads are done.
		const tFAW = 500
		run(pseudoChannelBuilder(nil).WithTFAW(tFAW), 5, 20e-9,
			func(i uint64) uint64 { return i * 128 })

		acts := activates()
		Expect(acts[0]).To(HaveLen(5))
		Expect(acts[1]).To(BeEmpty())
		Expect(acts[0][4].Cycle).To(Equal(acts[0][0].Cycle + tFAW))
		Expect(agent.completed).To(Equal(5))
	})
})
