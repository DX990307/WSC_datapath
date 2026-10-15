package dram

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/cmdq"
	"github.com/sarchlab/akita/v3/mem/dram/internal/org"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

// mi200Layout places, above the 32-B offset, column bit 0, the pseudo
// channel, 2 bank-group bits, 2 bank bits, the stack ID, 4 more column bits
// and the row.
var mi200Layout = AddressLayout{
	{AddressFieldColumn, 1},
	{AddressFieldChannel, 1},
	{AddressFieldBankGroup, 2},
	{AddressFieldBank, 2},
	{AddressFieldRank, 1},
	{AddressFieldColumn, 4},
	{AddressFieldRow, 15},
}

// pseudoChannelBuilder builds one HBM2 channel in pseudo-channel mode: 2
// pseudo channels with 2 stack IDs of 4x4 banks and 32-B access units.
func pseudoChannelBuilder(engine sim.Engine) Builder {
	return MakeBuilder().
		WithEngine(engine).
		WithProtocol(HBM).
		WithBusWidth(64).
		WithDeviceWidth(64).
		WithBurstLength(4).
		WithNumChannel(2).
		WithNumRank(2).
		WithNumBankGroup(4).
		WithNumBank(4).
		WithNumCol(128).
		WithNumRow(32768).
		WithAddressLayout(mi200Layout)
}

type countingTracer struct {
	starts, steps, ends []tracing.Task
}

func (t *countingTracer) StartTask(task tracing.Task) {
	t.starts = append(t.starts, task)
}

func (t *countingTracer) StepTask(task tracing.Task) {
	t.steps = append(t.steps, task)
}

func (t *countingTracer) EndTask(task tracing.Task) {
	t.ends = append(t.ends, task)
}

var _ = Describe("Builder", func() {
	It("should build one channel with legacy bank names", func() {
		tracer := &countingTracer{}
		m := MakeBuilder().
			WithEngine(sim.NewSerialEngine()).
			WithNumRank(2).
			WithNumBankGroup(4).
			WithNumBank(4).
			WithAdditionalTracer(tracer).
			Build("X")

		channel, ok := m.channel.(*org.ChannelImpl)
		Expect(ok).To(BeTrue())
		Expect(channel.Banks.GetBank(0, 0, 0).Name()).
			To(Equal("X.Bank[0][0][0]"))
		Expect(channel.Banks.GetBank(1, 3, 3).Name()).
			To(Equal("X.Bank[1][3][3]"))
		numBanks := 0
		channel.ForEachBank(func(b org.Bank) {
			numBanks++
			Expect(b.(*org.BankImpl).NumHooks()).To(Equal(1))
		})
		Expect(numBanks).To(Equal(32))

		queue := m.cmdQueue.(*cmdq.CommandQueueImpl)
		Expect(queue.Queues).To(HaveLen(2))
		Expect(queue.RanksPerChannel).To(BeZero())
		Expect(m.addrMapper).To(BeAssignableToTypeOf(
			addressmapping.DefaultMapper{}))
		Expect(m.dualCommandBus).To(BeFalse())
	})

	It("should build independent pseudo channels", func() {
		tracer := &countingTracer{}
		m := pseudoChannelBuilder(sim.NewSerialEngine()).
			WithAdditionalTracer(tracer).
			Build("X")

		channel, ok := m.channel.(*org.MultiChannel)
		Expect(ok).To(BeTrue())
		Expect(channel.Channels).To(HaveLen(2))
		for c, ch := range channel.Channels {
			ranks, bankGroups, banks := ch.Banks.GetSize()
			Expect([]uint64{ranks, bankGroups, banks}).
				To(Equal([]uint64{2, 4, 4}))
			Expect(ch.Banks.GetBank(1, 2, 3).Name()).To(Equal(
				fmt.Sprintf("X.Channel[%d].Bank[1][2][3]", c)))
			Expect(ch.Timing).To(Equal(channel.Channels[0].Timing))
		}
		Expect(channel.Channels[0].Banks.GetBank(0, 0, 0)).
			NotTo(BeIdenticalTo(channel.Channels[1].Banks.GetBank(0, 0, 0)))

		names := map[string]bool{}
		channel.ForEachBank(func(b org.Bank) {
			names[b.Name()] = true
			Expect(b.(*org.BankImpl).NumHooks()).To(Equal(1), b.Name())
		})
		Expect(names).To(HaveLen(64))

		queue := m.cmdQueue.(*cmdq.CommandQueueImpl)
		Expect(queue.Queues).To(HaveLen(4))
		Expect(queue.RanksPerChannel).To(Equal(2))
		Expect(queue.Channel).To(BeIdenticalTo(m.channel))
		Expect(m.physicalAccessBytes).To(Equal(uint64(32)))
	})

	It("should map with the address layout", func() {
		m := pseudoChannelBuilder(sim.NewSerialEngine()).Build("X")

		Expect(m.addrMapper.Map(0x20)).To(Equal(
			addressmapping.Location{Column: 1}))
		Expect(m.addrMapper.Map(0x40)).To(Equal(
			addressmapping.Location{Channel: 1}))
		Expect(m.addrMapper.Map(0x800)).To(Equal(
			addressmapping.Location{Rank: 1}))
		Expect(m.addrMapper.Map(0x10000)).To(Equal(
			addressmapping.Location{Row: 1}))
	})

	It("should panic on an invalid address field", func() {
		none := AddressLayout{{AddressFieldNone, 1}}
		unknown := mi200Layout
		unknown[7] = AddressFieldBits{AddressField(99), 1}
		empty := mi200Layout
		empty[7] = AddressFieldBits{AddressFieldRank, 0}

		Expect(func() {
			pseudoChannelBuilder(nil).WithAddressLayout(none).Build("X")
		}).To(PanicWith(ContainSubstring("invalid field 0")))
		Expect(func() {
			pseudoChannelBuilder(nil).WithAddressLayout(unknown).Build("X")
		}).To(PanicWith(ContainSubstring("invalid field 99")))
		Expect(func() {
			pseudoChannelBuilder(nil).WithAddressLayout(empty).Build("X")
		}).To(PanicWith(ContainSubstring("rank segment has 0 bits")))
	})

	It("should configure the dual command bus", func() {
		m := MakeBuilder().
			WithDualCommandBus(true).
			WithActivateCommandCycles(2).
			Build("X")
		Expect(m.dualCommandBus).To(BeTrue())
		Expect(m.activateCommandCycles).To(Equal(2))

		m = MakeBuilder().WithDualCommandBus(true).Build("X")
		Expect(m.activateCommandCycles).To(Equal(1))
	})

	It("should panic on activate command cycles without the dual bus",
		func() {
			Expect(func() {
				MakeBuilder().WithActivateCommandCycles(2).Build("X")
			}).To(PanicWith(ContainSubstring("WithDualCommandBus")))
		})

	It("should space activates across ranks only with cross-rank tRRD",
		func() {
			channel := MakeBuilder().WithTRRDS(7).
				Build("X").channel.(*org.ChannelImpl)
			Expect(channel.Timing.OtherRanks[signal.CmdKindActivate]).
				To(BeEmpty())

			channel = MakeBuilder().WithTRRDS(7).WithTRRDL(10).
				WithNumBankGroup(4).WithCrossRankTRRD(true).
				Build("X").channel.(*org.ChannelImpl)
			Expect(channel.Timing.OtherRanks[signal.CmdKindActivate]).
				To(Equal([]org.TimeTableEntry{
					{NextCmdKind: signal.CmdKindActivate,
						MinCycleInBetween: 7},
				}))
			Expect(channel.Timing.SameRank[signal.CmdKindActivate][0]).
				To(Equal(org.TimeTableEntry{
					NextCmdKind:       signal.CmdKindActivate,
					MinCycleInBetween: 7,
				}))

			for _, pc := range pseudoChannels(pseudoChannelBuilder(nil).
				WithTRRDS(7).WithCrossRankTRRD(true).Build("X")) {
				Expect(pc.Timing.OtherRanks[signal.CmdKindActivate]).
					To(ConsistOf(org.TimeTableEntry{
						NextCmdKind:       signal.CmdKindActivate,
						MinCycleInBetween: 7,
					}))
			}
		})

	It("should give every channel its own tFAW window", func() {
		channel := MakeBuilder().Build("X").channel.(*org.ChannelImpl)
		Expect(channel.FAW).To(BeZero())
		Expect(channel.Freq).To(BeZero())

		channel = MakeBuilder().WithTFAW(26).
			Build("X").channel.(*org.ChannelImpl)
		Expect(channel.FAW).To(Equal(26))
		Expect(channel.Freq).To(Equal(1600 * sim.MHz))

		pcs := pseudoChannels(pseudoChannelBuilder(nil).
			WithFreq(941 * sim.MHz).WithTFAW(26).Build("X"))
		Expect(pcs).To(HaveLen(2))
		for _, pc := range pcs {
			Expect(pc.FAW).To(Equal(26))
			Expect(pc.Freq).To(Equal(941 * sim.MHz))
			Expect(pc.Refresh).To(BeNil())
		}
	})

	It("should panic on a negative tFAW", func() {
		Expect(func() { MakeBuilder().WithTFAW(-1) }).
			To(PanicWith(ContainSubstring("invalid tFAW")))
	})
})

// hbm2TurnaroundBuilder builds one HBM2 pseudo channel with 2 stack IDs of
// 4x4 banks: tCL 13, tCWL 4, BL4 (2 cycles), tWTRS 6, tWTRL 8, tRTRS 0.
func hbm2TurnaroundBuilder(enabled bool) Builder {
	return MakeBuilder().
		WithProtocol(HBM).
		WithBusWidth(64).
		WithDeviceWidth(64).
		WithBurstLength(4).
		WithNumRank(2).
		WithNumBankGroup(4).
		WithNumBank(4).
		WithNumCol(128).
		WithTCL(13).
		WithTCWL(4).
		WithTWTRS(6).
		WithTWTRL(8).
		WithTRTRS(0).
		WithHBM2Turnaround(enabled)
}

// minCycles returns the cycles that table puts between a from command and a
// to command, or -1 if it has no such entry.
func minCycles(table org.TimeTable, from, to signal.CommandKind) int {
	for _, e := range table[from] {
		if e.NextCmdKind == to {
			return e.MinCycleInBetween
		}
	}

	return -1
}

var _ = Describe("Builder with HBM2 turnaround", func() {
	reads := []signal.CommandKind{
		signal.CmdKindRead, signal.CmdKindReadPrecharge}
	writes := []signal.CommandKind{
		signal.CmdKindWrite, signal.CmdKindWritePrecharge}

	type turnaround struct {
		writeToReadS, writeToReadL, writeToReadO int
		readToWriteS, readToWriteO               int
		writeCycles                              int
	}

	DescribeTable("should time write data and the turnarounds",
		func(enabled bool, want turnaround) {
			m := hbm2TurnaroundBuilder(enabled).Build("X")
			t := m.channel.(*org.ChannelImpl).Timing

			got := turnaround{
				writeToReadS: minCycles(t.SameRank,
					signal.CmdKindWrite, signal.CmdKindRead),
				writeToReadL: minCycles(t.OtherBanksInBankGroup,
					signal.CmdKindWrite, signal.CmdKindRead),
				writeToReadO: minCycles(t.OtherRanks,
					signal.CmdKindWrite, signal.CmdKindRead),
				readToWriteS: minCycles(t.SameRank,
					signal.CmdKindRead, signal.CmdKindWrite),
				readToWriteO: minCycles(t.OtherRanks,
					signal.CmdKindRead, signal.CmdKindWrite),
			}
			forEachBankImpl(m, func(b *org.BankImpl) {
				got.writeCycles = b.CmdCycles[signal.CmdKindWrite]
			})
			Expect(got).To(Equal(want))

			// The auto-precharge variants share the cross-SID values, and
			// the data-bus bursts stay BL/2 apart.
			for _, w := range writes {
				for _, r := range reads {
					Expect(minCycles(t.OtherRanks, w, r)).
						To(Equal(want.writeToReadO))
					Expect(minCycles(t.OtherRanks, r, w)).
						To(Equal(want.readToWriteO))
					Expect(minCycles(t.OtherRanks, r, r)).To(Equal(2))
					Expect(minCycles(t.OtherRanks, w, w)).To(Equal(2))
				}
			}
		},
		// WR->RD = tWL + BL/2 + tWTR, also across SIDs; RD->WR = tRL +
		// BL/2 - tWL + tRTRS for both SIDs; write data ends at tWL + BL/2.
		Entry("on", true, turnaround{
			writeToReadS: 12, writeToReadL: 14, writeToReadO: 12,
			readToWriteS: 11, readToWriteO: 11,
			writeCycles: 6,
		}),
		// Write data timed from tRL, and cross-SID turnarounds of only
		// BL/2 + tRTRS.
		Entry("off", false, turnaround{
			writeToReadS: 21, writeToReadL: 23, writeToReadO: 2,
			readToWriteS: 11, readToWriteO: 2,
			writeCycles: 15,
		}),
	)

	It("should complete pipelined writes when their data ends", func() {
		m := hbm2TurnaroundBuilder(true).
			WithPipelinedColumnCommands(true).
			Build("X")

		forEachBankImpl(m, func(b *org.BankImpl) {
			Expect(b.CmdCycles[signal.CmdKindWrite]).To(Equal(6))
			Expect(b.CmdCycles[signal.CmdKindWritePrecharge]).To(Equal(6))
			Expect(b.CmdCycles[signal.CmdKindReadPrecharge]).To(Equal(15))
		})
	})

	It("should reject HBM pipelining without the HBM2 turnaround", func() {
		Expect(func() {
			hbm2TurnaroundBuilder(false).
				WithPipelinedColumnCommands(true).
				Build("X")
		}).To(PanicWith(ContainSubstring("WithHBM2Turnaround(true)")))

		// The option decides, not the latencies.
		Expect(func() {
			hbm2TurnaroundBuilder(false).
				WithTCWL(13).
				WithPipelinedColumnCommands(true).
				Build("X")
		}).To(PanicWith(ContainSubstring("WithHBM2Turnaround(true)")))

		Expect(func() {
			MakeBuilder().WithPipelinedColumnCommands(true).Build("X")
		}).NotTo(Panic())
	})

	Context("in a channel", func() {
		var channel *org.ChannelImpl

		command := func(
			kind signal.CommandKind,
			rank, bankGroup, bank uint64,
		) *signal.Command {
			cmd := &signal.Command{
				ID: fmt.Sprintf("%s@%d.%d.%d",
					kind, rank, bankGroup, bank),
				Kind:     kind,
				SubTrans: &signal.SubTransaction{ID: "SubTrans"},
			}
			cmd.Rank, cmd.BankGroup, cmd.Bank = rank, bankGroup, bank

			return cmd
		}

		start := func(ready *signal.Command) {
			channel.StartCommand(0, ready)
			channel.UpdateTiming(0, ready)
		}

		issue := func(cmd *signal.Command) {
			ready := channel.GetReadyCommand(0, cmd)
			Expect(ready).NotTo(BeNil(), cmd.ID)
			Expect(ready.Kind).To(Equal(cmd.Kind))
			start(ready)
		}

		// build opens row 0 of banks 0 and 1 of bank groups 0 and 1 of both
		// stack IDs and lets every timing constraint expire.
		build := func(enabled bool) {
			channel = hbm2TurnaroundBuilder(enabled).
				Build("X").channel.(*org.ChannelImpl)

			for rank := uint64(0); rank < 2; rank++ {
				for bankGroup := uint64(0); bankGroup < 2; bankGroup++ {
					for bank := uint64(0); bank < 2; bank++ {
						read := command(signal.CmdKindRead,
							rank, bankGroup, bank)
						act := channel.GetReadyCommand(0, read)
						for act == nil {
							channel.Tick(0)
							act = channel.GetReadyCommand(0, read)
						}
						Expect(act.Kind).To(Equal(signal.CmdKindActivate))
						start(act)
					}
				}
			}

			for c := 0; c < 200; c++ {
				channel.Tick(0)
			}
		}

		type probe struct {
			cmd     *signal.Command
			readyAt int
		}

		// expectReady ticks the channel and checks that each probe becomes
		// ready exactly readyAt cycles after the command just issued.
		expectReady := func(probes ...probe) {
			for c := 0; c <= 30; c++ {
				for _, p := range probes {
					ready := channel.GetReadyCommand(0, p.cmd)
					Expect(ready != nil).To(Equal(c >= p.readyAt),
						"%s at cycle %d", p.cmd.ID, c)
					if ready != nil {
						Expect(ready.Kind).To(Equal(p.cmd.Kind))
					}
				}
				channel.Tick(0)
			}
		}

		// A read on SID 0 at t drives DQ during [t+13, t+15). With the
		// option, a write on SID 1 issues at t+11 at the earliest and drives
		// its data during [t+15, t+17). Without it, a write on SID 1 may
		// issue at t+9, whose data collides with the read data.
		DescribeTable("should turn the bus around from read to write",
			func(enabled bool, otherSID, otherBankGroup int) {
				build(enabled)
				issue(command(signal.CmdKindRead, 0, 0, 0))

				expectReady(
					probe{command(signal.CmdKindWrite, 1, 0, 0), otherSID},
					probe{command(signal.CmdKindWrite, 0, 1, 0),
						otherBankGroup},
				)
			},
			Entry("on", true, 11, 11),
			Entry("off", false, 2, 11),
		)

		DescribeTable("should turn the bus around from write to read",
			func(enabled bool, otherSID, otherBankGroup, sameBankGroup int) {
				build(enabled)
				issue(command(signal.CmdKindWrite, 0, 0, 0))

				expectReady(
					probe{command(signal.CmdKindRead, 1, 0, 0), otherSID},
					probe{command(signal.CmdKindRead, 0, 1, 0),
						otherBankGroup},
					probe{command(signal.CmdKindRead, 0, 0, 1),
						sameBankGroup},
				)
			},
			Entry("on", true, 12, 12, 14),
			Entry("off", false, 2, 21, 23),
		)
	})
})
