package dram

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/cmdq"
	"github.com/sarchlab/akita/v3/mem/dram/internal/org"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

var _ = Describe("DRAM-model stats", func() {
	It("should report nothing without a DRAM-model feature", func() {
		m := MakeBuilder().WithEngine(sim.NewSerialEngine()).Build("MemCtrl")

		Expect(m.GetDRAMModelStats()).To(Equal(DRAMModelStats{}))
	})

	DescribeTable("should be enabled by each DRAM-model feature",
		func(feature func(b Builder) Builder) {
			m := feature(MakeBuilder().WithEngine(sim.NewSerialEngine())).
				Build("MemCtrl")

			Expect(m.GetDRAMModelStats()).
				To(Equal(DRAMModelStats{Enabled: true}))
		},
		Entry("mapping interleave strip", func(b Builder) Builder {
			return b.WithMappingInterleaveStrip(4096, 8)
		}),
		Entry("address layout", func(b Builder) Builder {
			return b.WithAddressLayout(AddressLayout{
				{AddressFieldColumn, 7},
				{AddressFieldBank, 3},
				{AddressFieldRank, 1},
				{AddressFieldRow, 15},
			})
		}),
		Entry("several channels", func(b Builder) Builder {
			return b.WithNumChannel(2)
		}),
		Entry("dual command bus", func(b Builder) Builder {
			return b.WithDualCommandBus(true)
		}),
		Entry("row hold", func(b Builder) Builder {
			return b.WithTransactionRowHold(true)
		}),
		Entry("pipelined column commands", func(b Builder) Builder {
			return b.WithPipelinedColumnCommands(true)
		}),
		Entry("all-bank refresh", func(b Builder) Builder {
			return b.WithAllBankRefresh(true)
		}),
		Entry("tFAW", func(b Builder) Builder {
			return b.WithTFAW(16)
		}),
		Entry("cross-rank tRRD", func(b Builder) Builder {
			return b.WithCrossRankTRRD(true)
		}),
		Entry("HBM2 turnaround", func(b Builder) Builder {
			return b.WithHBM2Turnaround(true)
		}),
	)

	It("should sum the refresh and tFAW counters of a single channel",
		func() {
			m := MakeBuilder().
				WithEngine(sim.NewSerialEngine()).
				WithAllBankRefresh(true).
				WithTFAW(16).
				Build("MemCtrl")
			channel := m.channel.(*org.ChannelImpl)
			channel.Refresh.Stats = org.RefreshStats{
				Commands: 5, Retroactive: 2, WaitCycles: 30,
				MaxWaitCycles: 11, ForcedPrecharges: 1,
			}
			channel.FAWDeniedActivates = 7

			Expect(m.GetDRAMModelStats()).To(Equal(DRAMModelStats{
				Enabled:                 true,
				RefreshCommands:         5,
				RefreshRetroactive:      2,
				RefreshWaitCycles:       30,
				RefreshMaxWaitCycles:    11,
				RefreshForcedPrecharges: 1,
				FAWDeniedActivates:      7,
			}))
		})

	It("should sum the refresh and tFAW counters of the pseudo channels",
		func() {
			m := pseudoChannelBuilder(sim.NewSerialEngine()).
				WithAllBankRefresh(true).
				WithTFAW(16).
				Build("MemCtrl")
			channels := pseudoChannels(m)
			channels[0].Refresh.Stats = org.RefreshStats{
				Commands: 3, Retroactive: 1, WaitCycles: 10,
				MaxWaitCycles: 6,
			}
			channels[0].FAWDeniedActivates = 5
			channels[1].Refresh.Stats = org.RefreshStats{
				Commands: 4, WaitCycles: 20, MaxWaitCycles: 9,
				ForcedPrecharges: 2,
			}
			channels[1].FAWDeniedActivates = 6

			Expect(m.GetDRAMModelStats()).To(Equal(DRAMModelStats{
				Enabled:                 true,
				RefreshCommands:         7,
				RefreshRetroactive:      1,
				RefreshWaitCycles:       30,
				RefreshMaxWaitCycles:    9,
				RefreshForcedPrecharges: 2,
				FAWDeniedActivates:      11,
			}))
		})
})

var _ = Describe("MemController DRAM-model stats", func() {
	var (
		observer *collectingPhysicalDRAMObserver
		memCtrl  *MemController
		agent    *streamAgent
	)

	// stream builds b with the observer of the refresh tests and keeps 32
	// consecutive-line reads in flight for 20 us.
	stream := func(b Builder) {
		engine := sim.NewSerialEngine()
		memCtrl = b.
			WithEngine(engine).
			WithGlobalStorage(mem.NewStorage(1 * mem.GB)).
			Build("MemCtrl")
		observer = &collectingPhysicalDRAMObserver{}
		memCtrl.SetPhysicalDRAMObserver(observer, "GPU[0]", 3)

		agent = &streamAgent{
			window: 32, stopAt: 20e-6, address: consecutiveLines}
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
		Expect(agent.completed).To(BeNumerically(">", 500))
	}

	// issues returns the number of activates and the number of cycles in
	// which two commands issued.
	issues := func() (activates, dualCycles uint64) {
		perCycle := make(map[uint64]int)
		for _, e := range observer.events {
			if e.Kind != PhysicalDRAMCommandIssue {
				continue
			}
			perCycle[e.Cycle]++
			if e.CommandKind == "Activate" {
				activates++
			}
		}
		for _, n := range perCycle {
			Expect(n).To(BeNumerically("<=", 2))
			if n == 2 {
				dualCycles++
			}
		}
		return activates, dualCycles
	}

	It("should count the work of the MI200 DRAM model", func() {
		stream(pseudoChannelBuilder(nil).
			WithFreq(941*sim.MHz).
			WithTCL(13).WithTCWL(13).
			WithTRCDRD(13).WithTRCDWR(13).WithTRP(13).WithTRAS(32).
			WithTRRDS(7).WithTRRDL(10).WithTCCDS(2).WithTCCDL(5).
			WithTRTP(6).WithTWR(15).WithTWTRS(10).WithTWTRL(13).
			WithTRTRS(0).WithTREFI(3669).WithRFC(423).
			WithCommandQueueSize(16).
			WithTransactionQueueSize(64).
			WithDualCommandBus(true).
			WithActivateCommandCycles(2).
			WithHBM2Turnaround(true).
			WithTransactionRowHold(true).
			WithPipelinedColumnCommands(true).
			WithAllBankRefresh(true).
			WithRefreshStagger(3, 8).
			WithTFAW(40).
			WithCrossRankTRRD(true))

		stats := memCtrl.GetDRAMModelStats()
		Expect(stats.Enabled).To(BeTrue())

		// The first 32-B half of every 64-B line keeps the row open for
		// the second.
		Expect(stats.RowHoldAutoPrechargeStops).
			To(Equal(uint64(agent.completed)))
		Expect(stats.RowHoldAutoPrechargeStops).To(Equal(
			memCtrl.cmdQueue.(*cmdq.CommandQueueImpl).
				GetRowContinuationStats().RowHoldAutoPrechargeStops))

		// A two-cycle activate holds the row bus for the next cycle.
		activates, dualCycles := issues()
		Expect(stats.RowBusBusyTicks).To(Equal(activates))
		Expect(stats.DualIssueTicks).To(Equal(dualCycles))
		Expect(stats.DualIssueTicks).To(BeNumerically(">", 0))

		var want DRAMModelStats
		for _, channel := range pseudoChannels(memCtrl) {
			r := channel.Refresh.Stats
			Expect(r.Commands).To(BeNumerically(">", 0))
			want.RefreshCommands += r.Commands
			want.RefreshRetroactive += r.Retroactive
			want.RefreshWaitCycles += r.WaitCycles
			if r.MaxWaitCycles > want.RefreshMaxWaitCycles {
				want.RefreshMaxWaitCycles = r.MaxWaitCycles
			}
			want.RefreshForcedPrecharges += r.ForcedPrecharges
			want.FAWDeniedActivates += channel.FAWDeniedActivates
		}
		Expect(stats.RefreshCommands).To(Equal(want.RefreshCommands))
		Expect(stats.RefreshRetroactive).To(Equal(want.RefreshRetroactive))
		Expect(stats.RefreshWaitCycles).To(Equal(want.RefreshWaitCycles))
		Expect(stats.RefreshMaxWaitCycles).
			To(Equal(want.RefreshMaxWaitCycles))
		Expect(stats.RefreshForcedPrecharges).
			To(Equal(want.RefreshForcedPrecharges))
		Expect(stats.FAWDeniedActivates).To(Equal(want.FAWDeniedActivates))
		Expect(stats.FAWDeniedActivates).To(BeNumerically(">", 0))
	})

	It("should count nothing in a legacy controller under load", func() {
		stream(MakeBuilder().
			WithProtocol(HBM).
			WithFreq(500 * sim.MHz).
			WithBusWidth(256).
			WithBurstLength(4).
			WithNumRank(16).
			WithRowContinuation(true))

		Expect(memCtrl.GetDRAMModelStats()).To(Equal(DRAMModelStats{}))
		Expect(memCtrl.dualIssueTicks).To(BeZero())
		Expect(memCtrl.rowBusBusyTicks).To(BeZero())
	})
})
