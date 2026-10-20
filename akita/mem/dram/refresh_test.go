package dram

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/org"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// pseudoChannels returns the channels of a pseudo-channel controller.
func pseudoChannels(m *MemController) []*org.ChannelImpl {
	return m.channel.(*org.MultiChannel).Channels
}

var _ = Describe("Builder with all-bank refresh", func() {
	It("should build no refresh by default", func() {
		channel := MakeBuilder().Build("X").channel.(*org.ChannelImpl)
		Expect(channel.Refresh).To(BeNil())
		Expect(channel.Freq).To(BeZero())

		for _, channel := range pseudoChannels(
			pseudoChannelBuilder(nil).Build("X")) {
			Expect(channel.Refresh).To(BeNil())
			Expect(channel.Freq).To(BeZero())
		}
	})

	It("should refresh a single channel", func() {
		channel := MakeBuilder().
			WithAllBankRefresh(true).
			Build("X").channel.(*org.ChannelImpl)

		// DDR3 defaults: tREFI 6240, tRFC 208.
		Expect(channel.Freq).To(Equal(1600 * sim.MHz))
		Expect(channel.Refresh).To(Equal(&org.AllBankRefresh{
			Interval: 6240, Duration: 208,
		}))
	})

	DescribeTable("should stagger the pseudo channels",
		func(b Builder, phases []uint64) {
			channels := pseudoChannels(b.
				WithFreq(941 * sim.MHz).
				WithTREFI(3669).
				WithRFC(423).
				WithAllBankRefresh(true).
				Build("X"))

			Expect(channels).To(HaveLen(2))
			for c, channel := range channels {
				Expect(channel.Freq).To(Equal(941 * sim.MHz))
				Expect(channel.Refresh).To(Equal(&org.AllBankRefresh{
					Interval: 3669, Duration: 423, Phase: phases[c],
				}))
			}
		},
		Entry("in one controller", pseudoChannelBuilder(nil),
			[]uint64{0, 1834}),
		// (3*2+c)*3669/16
		Entry("across 8 controllers",
			pseudoChannelBuilder(nil).WithRefreshStagger(3, 8),
			[]uint64{1375, 1605}),
	)

	DescribeTable("should reject an invalid refresh",
		func(b func() Builder, message string) {
			Expect(func() { b().Build("X") }).
				To(PanicWith(ContainSubstring(message)))
		},
		Entry("tRFC as long as tREFI", func() Builder {
			return MakeBuilder().WithAllBankRefresh(true).
				WithTREFI(100).WithRFC(100)
		}, "tRFC"),
		Entry("no tRFC", func() Builder {
			return MakeBuilder().WithAllBankRefresh(true).WithRFC(0)
		}, "tRFC"),
		Entry("a stagger without refresh", func() Builder {
			return MakeBuilder().WithRefreshStagger(1, 4)
		}, "WithAllBankRefresh"),
		Entry("a slot out of range", func() Builder {
			return MakeBuilder().WithAllBankRefresh(true).
				WithRefreshStagger(4, 4)
		}, "slot 4 of 4"),
		Entry("no slots", func() Builder {
			return MakeBuilder().WithAllBankRefresh(true).
				WithRefreshStagger(0, 0)
		}, "slot 0 of 0"),
	)
})

// streamAgent keeps up to window 64-B reads in flight until stopAt. The
// address of the i-th read is address(i).
type streamAgent struct {
	*sim.TickingComponent
	port      sim.Port
	dst       sim.Port
	window    int
	stopAt    sim.VTimeInSec
	address   func(i uint64) uint64
	next      uint64
	inflight  int
	completed int
}

func (a *streamAgent) Tick(now sim.VTimeInSec) (madeProgress bool) {
	for a.port.Retrieve(now) != nil {
		a.inflight--
		a.completed++
		madeProgress = true
	}

	if now < a.stopAt && a.inflight < a.window {
		req := mem.ReadReqBuilder{}.
			WithSendTime(now).
			WithSrc(a.port).
			WithDst(a.dst).
			WithAddress(a.address(a.next)).
			WithByteSize(64).
			Build()
		if a.port.Send(req) == nil {
			a.next++
			a.inflight++
			madeProgress = true
		}
	}

	return madeProgress
}

// consecutiveLines addresses consecutive 64-B lines. With the MI200 layout,
// they alternate between the pseudo channels and then spread over the bank
// groups, banks, and stack IDs, so that every line needs an activate.
func consecutiveLines(i uint64) uint64 {
	return i * 64
}

// rowWalk addresses, with the MI200 layout, all 16 lines of one bank row one
// after another before it moves on to the next bank, so that the row
// continuation hands each open row over to the next line.
func rowWalk(i uint64) uint64 {
	return (i&15)<<12 | (i>>4&63)<<6 | (i>>10)<<16
}

var _ = Describe("MemController with all-bank refresh", func() {
	const (
		tREFI = 3669
		tRFC  = 423
		tRAS  = 32
		tRP   = 13
		tRC   = tRAS + tRP
	)

	var (
		observer *collectingPhysicalDRAMObserver
		memCtrl  *MemController
		agent    *streamAgent
	)

	// stream builds an MI200-like HBM2e channel (941 MHz, two pseudo
	// channels, row hold, pipelined column commands, dual command bus), as
	// controller 3 of 8 of a GPM, and streams reads into it for 40 us.
	stream := func(
		refresh, rowContinuation bool,
		address func(i uint64) uint64,
	) {
		engine := sim.NewSerialEngine()
		b := pseudoChannelBuilder(engine).
			WithFreq(941 * sim.MHz).
			// The stream has no writes, so tCWL does not matter.
			WithTCL(13).WithTCWL(13).
			WithTRCDRD(13).WithTRCDWR(13).WithTRP(tRP).WithTRAS(tRAS).
			WithTRRDS(7).WithTRRDL(10).WithTCCDS(2).WithTCCDL(5).
			WithTRTP(6).WithTWR(15).WithTWTRS(10).WithTWTRL(13).
			WithTRTRS(0).WithTREFI(tREFI).WithRFC(tRFC).
			WithCommandQueueSize(16).
			WithTransactionQueueSize(64).
			WithDualCommandBus(true).
			WithActivateCommandCycles(2).
			WithHBM2Turnaround(true).
			WithTransactionRowHold(true).
			WithPipelinedColumnCommands(true).
			WithRowContinuation(rowContinuation).
			WithGlobalStorage(mem.NewStorage(1 * mem.GB))
		if refresh {
			b = b.WithAllBankRefresh(true).WithRefreshStagger(3, 8)
		}
		memCtrl = b.Build("MemCtrl")
		observer = &collectingPhysicalDRAMObserver{}
		memCtrl.SetPhysicalDRAMObserver(observer, "GPU[0]", 3)

		agent = &streamAgent{window: 32, stopAt: 40e-6, address: address}
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

	// activateGaps counts, per pseudo channel, the gaps of at least tRFC
	// cycles between two consecutive activates.
	activateGaps := func() []int {
		gaps := make([]int, 2)
		last := make([]uint64, 2)
		for _, e := range observer.events {
			if e.Kind != PhysicalDRAMCommandIssue ||
				e.CommandKind != "Activate" {
				continue
			}
			if last[e.Channel] != 0 && e.Cycle-last[e.Channel] >= tRFC {
				gaps[e.Channel]++
			}
			last[e.Channel] = e.Cycle
		}
		return gaps
	}

	DescribeTable("should refresh every pseudo channel each tREFI while "+
		"the rows drain on their own",
		func(rowContinuation bool, address func(i uint64) uint64) {
			stream(true, rowContinuation, address)

			end := (941 * sim.MHz).Cycle(40e-6)
			gaps := activateGaps()
			for c, channel := range pseudoChannels(memCtrl) {
				phase := org.StaggerRefreshPhase(3, 8, c, 2, tREFI)
				due := (end - phase) / tREFI
				stats := channel.Refresh.Stats

				Expect(stats.Commands).To(BeNumerically(">=", due))
				Expect(stats.Commands).To(BeNumerically("<=", due+1))
				Expect(stats.Retroactive).To(BeZero())
				Expect(stats.ForcedPrecharges).To(BeZero())
				// The rows that are open when a refresh falls due close
				// within one row cycle of their activate, because the
				// scheduler no longer hands them over.
				Expect(stats.WaitCycles).To(BeNumerically(">", 0))
				Expect(stats.MaxWaitCycles).To(BeNumerically("<", tRC))
				// Each refresh in the stream stops the activates of its
				// pseudo channel for at least tRFC.
				Expect(gaps[c]).To(Equal(int(due)))
			}

			if rowContinuation {
				Expect(memCtrl.GetRowContinuationStats().AutoPrechargeStops).
					To(BeNumerically(">", 1000))
			}
		},
		Entry("for consecutive lines", false, consecutiveLines),
		Entry("for lines that hand their rows over", true, rowWalk),
	)

	It("should cost bandwidth only while a pseudo channel refreshes",
		func() {
			stream(false, false, consecutiveLines)
			without := agent.completed
			Expect(activateGaps()).To(Equal([]int{0, 0}))

			stream(true, false, consecutiveLines)
			with := agent.completed

			// A refresh blocks one pseudo channel for tRFC plus its
			// drain, about 12% of tREFI.
			Expect(with).To(BeNumerically("<", without))
			Expect(float64(with)).
				To(BeNumerically(">", 0.85*float64(without)))
		})
})
