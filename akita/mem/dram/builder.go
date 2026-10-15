package dram

import (
	"fmt"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"

	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/tracing"

	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/cmdq"
	"github.com/sarchlab/akita/v3/mem/dram/internal/org"
	"github.com/sarchlab/akita/v3/mem/dram/internal/trans"
)

// Builder can build new memory controllers.
type Builder struct {
	engine           sim.Engine
	freq             sim.Freq
	useGlobalStorage bool
	storage          *mem.Storage
	addrConverter    mem.AddressConverter

	mapStripGranularity uint64
	mapStripUnits       int

	protocol              Protocol
	transactionQueueSize  int
	commandQueueSize      int
	rowContinuation       bool
	aggregateContinuation bool
	busWidth              int
	burstLength           int
	deviceWidth           int
	numChannel            int
	numRank               int
	numBankGroup          int
	numBank               int
	numRow                int
	numCol                int
	addressLayout         AddressLayout
	dualCommandBus        bool
	activateCommandCycles int
	transactionRowHold    bool
	pipelinedColumns      bool
	allBankRefresh        bool
	refreshSlot           int
	refreshSlots          int
	crossRankTRRD         bool
	hbm2Turnaround        bool

	burstCycle int
	tAL        int
	tCL        int
	tCWL       int
	tRL        int
	tWL        int
	readDelay  int
	writeDelay int
	tRCD       int
	tRP        int
	tRAS       int
	tCCDL      int
	tCCDS      int
	tRTRS      int
	tRTP       int
	tWTRL      int
	tWTRS      int
	tWR        int
	tPPD       int
	tRC        int
	tRRDL      int
	tRRDS      int
	tFAW       int
	tRCDRD     int
	tRCDWR     int
	tREFI      int
	tRFC       int
	tRFCb      int
	tCKESR     int
	tXS        int

	tracers []tracing.Tracer
}

// MakeBuilder creates a builder with default configuration.
func MakeBuilder() Builder {
	b := Builder{
		freq:                 1600 * sim.MHz,
		protocol:             DDR3,
		transactionQueueSize: 32,
		commandQueueSize:     8,
		busWidth:             64,
		burstLength:          8,
		deviceWidth:          16,
		numChannel:           1,
		numRank:              2,
		numBankGroup:         1,
		numBank:              8,
		numRow:               32768,
		numCol:               1024,
		burstCycle:           4,
		tAL:                  0,
		tCL:                  11,
		tCWL:                 8,
		tRCD:                 11,
		tRP:                  11,
		tRAS:                 28,
		tCCDL:                4,
		tCCDS:                4,
		tRTRS:                1,
		tRTP:                 6,
		tWTRL:                6,
		tWTRS:                6,
		tWR:                  12,
		tPPD:                 0,
		tRRDL:                5,
		tRRDS:                5,
		tRCDRD:               24,
		tRCDWR:               20,
		tREFI:                6240,
		tRFC:                 208,
		tRFCb:                1950,
		tCKESR:               5,
		tXS:                  216,
	}

	return b
}

// WithEngine sets the engine that the builder uses.
func (b Builder) WithEngine(engine sim.Engine) Builder {
	b.engine = engine
	return b
}

// WithFreq sets the frequency of the builder.
func (b Builder) WithFreq(freq sim.Freq) Builder {
	b.freq = freq
	return b
}

// WithGlobalStorage asks the DRAM to use a global storage instead of a local
// storage. Use this when you want to provide a unified storage for your whole
// simulation. The address of the storage is the global physical address.
func (b Builder) WithGlobalStorage(s *mem.Storage) Builder {
	b.storage = s
	b.useGlobalStorage = true
	return b
}

// WithInterleavingAddrConversion sets the rule to convert the global physical
// address to the internal physical address.
//
// For example, in a GPU that has 8 memory controllers. The addresses are
// interleaved across all the memory controllers at the page granularity. The
// current DRAM is the 3rd in the array of 8 memory controller. Also, there are
// 4 GPUs in total and each GPU has 4GB memory. The CPU also has 4GB memory,
// occupying the physical address from 0-4GB. The current GPU is the 2nd GPU. So
// the address range is from 8GB - 12GB. In this case, the use should call this
// function as `WithAddrConversion(4096, 8, 3, 8*mem.GB, 12*mem.GB)`.
//
// If there is only cone memory controller in your simulation, this function
// should not be called and the global physical address is equivalent to the
// DRAM controller's internal physical address.
func (b Builder) WithInterleavingAddrConversion(
	interleaveGranularity uint64,
	numTotalUnit, currentUnitIndex int,
	lowerBound, upperBound uint64,
) Builder {
	b.addrConverter = mem.InterleavingConverter{
		InterleavingSize:    interleaveGranularity,
		TotalNumOfElements:  numTotalUnit,
		CurrentElementIndex: currentUnitIndex,
		Offset:              lowerBound,
	}
	return b
}

// WithMappingInterleaveStrip removes the controller-select bits of an
// interleaved address space before DRAM address mapping. Addresses are
// interleaved across numUnits controllers every granularity bytes; the
// mapper sees addr/(granularity*numUnits)*granularity + addr%granularity.
// Only the mapping changes: storage keeps using the request address, so the
// option works with a global storage. Unset keeps mapping the raw address.
//
// The granularity must be a multiple of the physical access unit
// (BusWidth/8*BurstLength bytes), and the option cannot be combined with
// WithInterleavingAddrConversion.
func (b Builder) WithMappingInterleaveStrip(
	granularity uint64,
	numUnits int,
) Builder {
	if granularity == 0 || numUnits <= 0 {
		panic(fmt.Sprintf(
			"invalid mapping interleave strip: granularity %d, %d units",
			granularity, numUnits))
	}

	b.mapStripGranularity = granularity
	b.mapStripUnits = numUnits
	return b
}

// interleaveStrip converts an address of an interleaved address space to the
// dense address of one unit by removing the unit-select digits. It accepts
// addresses of any unit; addresses of different units at the same position
// map to the same dense address.
type interleaveStrip struct {
	granularity uint64
	units       uint64
}

// ConvertExternalToInternal removes the unit-select digits.
func (s interleaveStrip) ConvertExternalToInternal(external uint64) uint64 {
	return external/(s.granularity*s.units)*s.granularity +
		external%s.granularity
}

// ConvertInternalToExternal is not supported, because the unit index is
// lost.
func (s interleaveStrip) ConvertInternalToExternal(internal uint64) uint64 {
	panic("interleave strip cannot convert an internal address back")
}

// WithProtocol sets the protocol of the memory controller.
func (b Builder) WithProtocol(protocol Protocol) Builder {
	b.protocol = protocol
	return b
}

// WithTransactionQueueSize sets the number of transactions can be buffered
// before converting them into commands. Note that accesses that touches
// multiple access units (BusWidth/8*BurstLength bytes) may need to be split
// into multiple transactions.
func (b Builder) WithTransactionQueueSize(n int) Builder {
	b.transactionQueueSize = n
	return b
}

// WithCommandQueueSize sets the number of command that each command queue
// can hold.
func (b Builder) WithCommandQueueSize(n int) Builder {
	b.commandQueueSize = n
	return b
}

// WithRowContinuation enables work-conserving row continuation. The scheduler
// preserves its ordinary command order, but suppresses auto-precharge when a
// second ready 64-byte command can immediately reuse the same bank and row.
func (b Builder) WithRowContinuation(enabled bool) Builder {
	b.rowContinuation = enabled
	return b
}

// WithAggregateContinuation keeps a row open only between physical access
// units that came from the same front-end aggregate. It does not wait for or
// reorder unrelated requests.
func (b Builder) WithAggregateContinuation(enabled bool) Builder {
	b.aggregateContinuation = enabled
	return b
}

// WithBusWidth sets the number of bits can be transferred out of the banks
// at the same time.
func (b Builder) WithBusWidth(n int) Builder {
	b.busWidth = n
	return b
}

// WithBurstLength sets the number of access (each access manipulates the amount
// of data that equals the bus width) that takes place as one group.
func (b Builder) WithBurstLength(n int) Builder {
	b.burstLength = n
	return b
}

// WithDeviceWidth sets the number of bit that a bank can deliver at the same
// time.
func (b Builder) WithDeviceWidth(n int) Builder {
	b.deviceWidth = n
	return b
}

// WithNumChannel sets the channels that the memory controller controls.
func (b Builder) WithNumChannel(n int) Builder {
	b.numChannel = n
	return b
}

// WithNumRank sets the number of ranks in each channel. Number of ranks is
// typically the last parameter to determine. Here is how you can calculate
// the number of ranks. Suppose your total memory capacity is B_{ctrl}, channel
// count N_{chn}, row count N_{row}, column count N_col, bus width W_b, device
// width W_d. You can calculate the bank size as B_b with B_b = N_{col} *
// N_{row} * W_d. The rank size can be calculated with B_r = B_b * N_b *
// N_{device_per_rank}, where N_{device_per_rank} can be calculated with
// N_{device_per_rank} = W_b/W_d. Finally, the number of ranks is N_r =
// B_{ctrl} / N_{chn} / B_r.
func (b Builder) WithNumRank(n int) Builder {
	b.numRank = n
	return b
}

// WithNumBankGroup sets the number of bank groups in each rank.
func (b Builder) WithNumBankGroup(n int) Builder {
	b.numBankGroup = n
	return b
}

// WithNumBank sets the number of banks in each bank group.
func (b Builder) WithNumBank(n int) Builder {
	b.numBank = n
	return b
}

// WithNumRow sets the number of rows in each DRAM array.
func (b Builder) WithNumRow(n int) Builder {
	b.numRow = n
	return b
}

// WithNumCol sets the number of columns in each DRAM array.
func (b Builder) WithNumCol(n int) Builder {
	b.numCol = n
	return b
}

// AddressField names a field of a DRAM location in an explicit address
// layout.
type AddressField int

// A list of address fields. The zero value marks an unused layout entry.
const (
	AddressFieldNone AddressField = iota
	AddressFieldChannel
	AddressFieldRank
	AddressFieldBankGroup
	AddressFieldBank
	AddressFieldRow
	AddressFieldColumn
)

// AddressFieldBits places Bits bits of one address field.
type AddressFieldBits struct {
	Field AddressField
	Bits  int
}

// AddressLayout lists the address fields from the bit right above the
// access-unit offset upward. A field may appear more than once, in which case
// its lower bits come first. The widths of each field must add up to the
// log2 of its count (for the column: log2(NumCol)-log2(BurstLength)). Unused
// entries are zero. The zero layout keeps the default mapping, which is, from
// low to high, column, bank group, bank, rank, channel, row. AddressLayout is
// an array, so configurations that hold one stay comparable.
type AddressLayout [8]AddressFieldBits

var addressFieldItems = map[AddressField]addressmapping.LocationItem{
	AddressFieldChannel:   addressmapping.LocationItemChannel,
	AddressFieldRank:      addressmapping.LocationItemRank,
	AddressFieldBankGroup: addressmapping.LocationItemBankGroup,
	AddressFieldBank:      addressmapping.LocationItemBank,
	AddressFieldRow:       addressmapping.LocationItemRow,
	AddressFieldColumn:    addressmapping.LocationItemColumn,
}

func (l AddressLayout) fieldBits() []addressmapping.FieldBits {
	var fields []addressmapping.FieldBits

	for _, f := range l {
		if f == (AddressFieldBits{}) {
			continue
		}

		item, ok := addressFieldItems[f.Field]
		if !ok {
			panic(fmt.Sprintf("address layout: invalid field %d", f.Field))
		}

		fields = append(fields,
			addressmapping.FieldBits{Item: item, Bits: f.Bits})
	}

	return fields
}

// WithAddressLayout sets an explicit address layout. For example, an HBM2
// channel in pseudo-channel mode can place the pseudo channel (channel) right
// above the first column bit, so that the two 32-B halves of a 64-B line stay
// in one bank and row while consecutive lines alternate between the pseudo
// channels. The zero layout keeps the default mapping.
func (b Builder) WithAddressLayout(l AddressLayout) Builder {
	b.addressLayout = l
	return b
}

// WithDualCommandBus issues up to one row command (activate, precharge) and
// one column command (read, write) per cycle, as HBM's separate row and
// column command interfaces allow (JESD235A section 3.2.2). Off issues one
// command per cycle.
func (b Builder) WithDualCommandBus(enabled bool) Builder {
	b.dualCommandBus = enabled
	return b
}

// WithActivateCommandCycles sets the number of cycles an activate occupies
// the row command bus (2 for HBM2). It requires WithDualCommandBus(true).
// Unset, every command occupies its bus for one cycle.
func (b Builder) WithActivateCommandCycles(cycles int) Builder {
	b.activateCommandCycles = cycles
	return b
}

// WithTransactionRowHold keeps a bank open for all the access units of one
// transaction that use the same row, such as the two 32-B halves of a 64-B
// line: the first issues a read (write) and the last a read (write) with
// auto-precharge, so that they share one activate. The bank is reserved for
// the transaction from its activate on. With WithRowContinuation or
// WithAggregateContinuation, the transaction's last access unit can hand the
// open row to the next queued same-row command. The sub-transactions of a
// transaction are then moved to the command queue together. Off issues every
// access unit with auto-precharge.
func (b Builder) WithTransactionRowHold(enabled bool) Builder {
	b.transactionRowHold = enabled
	return b
}

// WithPipelinedColumnCommands lets the read and write commands of a bank
// overlap, as their data transfers do in a real device. A column command then
// does not occupy the bank until its data transfer ends: two reads of the
// open row are max(tCCDL, BL/2) apart, and a read or write with
// auto-precharge completes when its data transfer ends, while the timing
// tables enforce the precharge time. Off lets a bank execute one command at a
// time.
//
// On HBM, it needs WithHBM2Turnaround(true), which times write data from the
// write latency (tWL + BL/2), as pipelined writes complete at that time;
// Build panics otherwise.
func (b Builder) WithPipelinedColumnCommands(enabled bool) Builder {
	b.pipelinedColumns = enabled
	return b
}

// WithAllBankRefresh refreshes all the banks of each channel every tREFI
// cycles (WithTREFI). A refresh starts once every bank of the channel is
// precharged and idle, and blocks activates for tRFC cycles (WithRFC; the
// default is a DDR3 value). While a refresh waits, the channel issues no
// activate and the scheduler stops keeping rows open, so that the open rows
// drain. The refresh takes no command-bus slot. The channels of a controller
// refresh at evenly staggered times; see WithRefreshStagger. Off models no
// refresh.
func (b Builder) WithAllBankRefresh(enabled bool) Builder {
	b.allBankRefresh = enabled
	return b
}

// WithRefreshStagger places the controller at slot of numSlots controllers
// that share one refresh schedule, such as the memory controllers of a GPU.
// The refreshes of all their channels are then spread evenly over tREFI
// instead of blocking every channel at the same time. Unset, the controller
// takes slot 0 of 1. It requires WithAllBankRefresh(true).
func (b Builder) WithRefreshStagger(slot, numSlots int) Builder {
	if numSlots <= 0 || slot < 0 || slot >= numSlots {
		panic(fmt.Sprintf("invalid refresh stagger: slot %d of %d",
			slot, numSlots))
	}

	b.refreshSlot = slot
	b.refreshSlots = numSlots
	return b
}

// WithAdditionalTracer adds one tracer to the memory controller and all the
// banks.
func (b Builder) WithAdditionalTracer(t tracing.Tracer) Builder {
	b.tracers = append(b.tracers, t)
	return b
}

// WithTAL sets the additional latency to column access in cycles.
func (b Builder) WithTAL(cycle int) Builder {
	b.tAL = cycle
	return b
}

// WithTCL sets the column access strobe latency in cycles
func (b Builder) WithTCL(cycle int) Builder {
	b.tCL = cycle
	return b
}

// WithTCWL sets the column write strobe latency in cycles
func (b Builder) WithTCWL(cycle int) Builder {
	b.tCWL = cycle
	return b
}

// WithTRCD sets the row-to-column delay in cycles.
func (b Builder) WithTRCD(cycle int) Builder {
	b.tRCD = cycle
	return b
}

// WithTRP sets the row precharge latency in cycles.
func (b Builder) WithTRP(cycle int) Builder {
	b.tRP = cycle
	return b
}

// WithTRAS sets the row access strobe latency in cycles.
func (b Builder) WithTRAS(cycle int) Builder {
	b.tRAS = cycle
	return b
}

// WithTCCDL sets the long column-to-column delay in cycles. The long delay
// describes accesses to banks in the same bank group.
func (b Builder) WithTCCDL(cycle int) Builder {
	b.tCCDL = cycle
	return b
}

// WithTCCDS sets the short column-to-column delay in cycles. The long delay
// describes accesses to banks from different bank groups.
func (b Builder) WithTCCDS(cycle int) Builder {
	b.tCCDS = cycle
	return b
}

// WithTRTRS sets the rank-to-rank switching latency.
func (b Builder) WithTRTRS(cycle int) Builder {
	b.tRTRS = cycle
	return b
}

// WithTRTP sets the row-to-precharge latency in cycles.
func (b Builder) WithTRTP(cycle int) Builder {
	b.tRTP = cycle
	return b
}

// WithTWTRL sets the long write-to-read latency in cycles. The long latency
// describes write and read to banks from the same bank group.
func (b Builder) WithTWTRL(cycle int) Builder {
	b.tWTRL = cycle
	return b
}

// WithTWTRS sets the short write-to-read latency in cycles. The short latency
// describes write and read to banks from different bank groups.
func (b Builder) WithTWTRS(cycle int) Builder {
	b.tWTRS = cycle
	return b
}

// WithTWR sets the write recovery time in cycles.
func (b Builder) WithTWR(cycle int) Builder {
	b.tWR = cycle
	return b
}

// WithTPPD sets the precharge to precharge delay in cycles.
func (b Builder) WithTPPD(cycle int) Builder {
	b.tPPD = cycle
	return b
}

// WithTRRDL sets the long activate to activate latency in cycles. The long
// latency describes activating different banks from the same bank group.
func (b Builder) WithTRRDL(cycle int) Builder {
	b.tRRDL = cycle
	return b
}

// WithTRRDS sets the short activate to activate latency in cycles. The short
// latency describes activating different banks from different bank groups.
func (b Builder) WithTRRDS(cycle int) Builder {
	b.tRRDS = cycle
	return b
}

// WithTFAW sets the four-activate window (tFAW) in cycles: a channel starts
// at most four activates in any window of that many cycles, counting the
// activates of all its ranks. With several channels (WithNumChannel), such
// as the pseudo channels of an HBM2 channel, each channel has its own
// window, which counts the activates of both stack IDs. The default, 0,
// models no window.
func (b Builder) WithTFAW(cycle int) Builder {
	if cycle < 0 {
		panic(fmt.Sprintf("invalid tFAW: %d cycles", cycle))
	}

	b.tFAW = cycle
	return b
}

// WithCrossRankTRRD keeps activates to different ranks of a channel tRRDS
// (WithTRRDS) apart. HBM2 applies tRRD per pseudo channel (JESD235A section
// 3.2.1), so activates to its two stack IDs, modeled as ranks, must be
// spaced too. Off leaves activates to different ranks unspaced.
func (b Builder) WithCrossRankTRRD(enabled bool) Builder {
	b.crossRankTRRD = enabled
	return b
}

// WithHBM2Turnaround times write data from the write latency (tWL + BL/2)
// instead of the read latency, and applies the write-to-read and
// read-to-write turnarounds at pseudo-channel scope, that is, across stack
// IDs, as JESD235 and Ramulator 2 do. The ranks of a channel are then the
// stack IDs of an HBM2 pseudo channel, which share its data bus. Off keeps
// the historical formulas, which time write data from tRL and space column
// commands to different ranks only by the data-bus burst.
func (b Builder) WithHBM2Turnaround(enabled bool) Builder {
	b.hbm2Turnaround = enabled
	return b
}

// WithTRCDRD sets the activate to read latency in cycles. It only works for
// GDDR DRAMs.
func (b Builder) WithTRCDRD(cycle int) Builder {
	b.tRCDRD = cycle
	return b
}

// WithTRCDWR sets the activate to write latency in cycles. It only works for
// GDDR DRAMs.
func (b Builder) WithTRCDWR(cycle int) Builder {
	b.tRCDWR = cycle
	return b
}

// WithTREFI sets the refresh interval in cycles.
func (b Builder) WithTREFI(cycle int) Builder {
	b.tREFI = cycle
	return b
}

// WithRFC sets the refresh cycle time in cycles.
func (b Builder) WithRFC(cycle int) Builder {
	b.tRFC = cycle
	return b
}

// WithRFCb sets the refresh to activate bank latency in cycles.
func (b Builder) WithRFCb(cycle int) Builder {
	b.tRFCb = cycle
	return b
}

// Build builds a new MemController.
func (b Builder) Build(name string) *MemController {
	m := &MemController{
		addrConverter: b.addrConverter,
		storage:       b.storage,
	}
	m.TickingComponent = sim.NewTickingComponent(name, b.engine, b.freq, m)

	b.attachTracers(m)
	b.buildChannel(name, m)

	m.addrConverter = b.addrConverter
	m.addrMapper = addressmapping.MakeBuilder().
		WithBurstLength(b.burstLength).
		WithBusWidth(b.busWidth).
		WithNumChannel(b.numChannel).
		WithNumRank(b.numRank).
		WithNumBankGroup(b.numBankGroup).
		WithNumBank(b.numBank).
		WithNumCol(b.numCol).
		WithNumRow(b.numRow).
		WithLayoutLowToHigh(b.addressLayout.fieldBits()).
		Build()

	numAccessUnitBit, _ := log2(uint64(b.busWidth / 8 * b.burstLength))
	m.physicalAccessBytes = uint64(b.busWidth / 8 * b.burstLength)
	m.subTransSplitter = trans.NewSubTransSplitter(numAccessUnitBit)
	b.buildMappingConverter(m)
	cmdQueue := &cmdq.CommandQueueImpl{
		Queues:                make([]cmdq.Queue, b.numChannel*b.numRank),
		CapacityPerQueue:      b.commandQueueSize,
		Channel:               m.channel,
		RowContinuation:       b.rowContinuation,
		AggregateContinuation: b.aggregateContinuation,
		RowHold:               b.transactionRowHold,
		Freq:                  b.freq,
	}
	if b.numChannel > 1 {
		cmdQueue.RanksPerChannel = b.numRank
	}
	m.cmdQueue = cmdQueue
	b.buildCommandBus(m)
	m.dramModelFeatures = b.usesDRAMModelFeatures()
	cmdCreator := trans.CommandCreator(&trans.ClosePageCommandCreator{
		AddrMapper: m.addrMapper,
	})
	m.subTransactionQueue = &trans.FCFSSubTransactionQueue{
		Capacity:   b.transactionQueueSize,
		CmdQueue:   m.cmdQueue,
		CmdCreator: cmdCreator,
		// The hold needs every access unit of a transaction queued.
		BatchByTransaction: b.transactionRowHold,
	}

	if b.useGlobalStorage {
		m.storage = b.storage
	} else {
		devicePerRank := b.busWidth / b.deviceWidth
		bankSize := b.numCol * b.numRow * b.deviceWidth / 8
		rankSize := bankSize * b.numBank * devicePerRank
		totalSize := rankSize * b.numRank * b.numChannel
		m.storage = mem.NewStorage(uint64(totalSize))
	}

	m.topPort = sim.NewLimitNumMsgPort(m, 1024, name+".TopPort")
	m.AddPort("Top", m.topPort)

	return m
}

// buildMappingConverter installs the mapping-only interleave strip. Without
// WithMappingInterleaveStrip the converter stays nil and sub-transactions are
// mapped at their request address.
func (b Builder) buildMappingConverter(m *MemController) {
	if b.mapStripUnits == 0 {
		return
	}

	if b.addrConverter != nil {
		// The physical observer would convert the stripped address a second
		// time.
		panic("WithMappingInterleaveStrip cannot be combined with " +
			"WithInterleavingAddrConversion")
	}

	if b.mapStripGranularity%m.physicalAccessBytes != 0 {
		panic(fmt.Sprintf(
			"mapping interleave granularity %d is not a multiple of "+
				"the %d-byte access unit",
			b.mapStripGranularity, m.physicalAccessBytes))
	}

	m.mappingConverter = interleaveStrip{
		granularity: b.mapStripGranularity,
		units:       uint64(b.mapStripUnits),
	}
}

// usesDRAMModelFeatures reports whether any optional DRAM-model feature is
// on. Only then does the controller report DRAMModelStats, so that the
// reports of the historical configurations stay unchanged.
func (b Builder) usesDRAMModelFeatures() bool {
	return b.mapStripUnits != 0 ||
		b.addressLayout != (AddressLayout{}) ||
		b.numChannel > 1 ||
		b.dualCommandBus ||
		b.transactionRowHold ||
		b.pipelinedColumns ||
		b.allBankRefresh ||
		b.tFAW > 0 ||
		b.crossRankTRRD ||
		b.hbm2Turnaround
}

// buildCommandBus configures the dual row/column command bus. Without
// WithDualCommandBus the controller issues one command per cycle.
func (b Builder) buildCommandBus(m *MemController) {
	if b.activateCommandCycles < 0 ||
		(b.activateCommandCycles > 1 && !b.dualCommandBus) {
		panic(fmt.Sprintf(
			"%d activate command cycles need WithDualCommandBus(true)",
			b.activateCommandCycles))
	}

	if !b.dualCommandBus {
		return
	}

	m.dualCommandBus = true
	m.activateCommandCycles = max(1, b.activateCommandCycles)
}

func (b Builder) attachTracers(hookable tracing.NamedHookable) {
	for _, tracer := range b.tracers {
		tracing.CollectTrace(hookable, tracer)
	}
}

// buildChannel builds the banks. A controller with several channels, such as
// the pseudo channels of an HBM2 channel, gets one ChannelImpl per channel,
// with bank names X.Channel[c].Bank[r][g][b]; the channels share the timing
// tables but no bank state.
func (b Builder) buildChannel(name string, m *MemController) {
	timing := b.generateTiming()
	b.checkColumnPipelining()
	b.checkRefresh()

	if b.numChannel <= 1 {
		channel := &org.ChannelImpl{
			Banks:  b.buildBanks(name),
			Timing: timing,
		}
		b.buildRefresh(channel, 0)
		b.buildFAW(channel)
		m.channel = channel
		return
	}

	channel := &org.MultiChannel{}
	for c := 0; c < b.numChannel; c++ {
		pseudoChannel := &org.ChannelImpl{
			Banks:  b.buildBanks(fmt.Sprintf("%s.Channel[%d]", name, c)),
			Timing: timing,
		}
		b.buildRefresh(pseudoChannel, c)
		b.buildFAW(pseudoChannel)
		channel.Channels = append(channel.Channels, pseudoChannel)
	}
	m.channel = channel
}

// buildFAW gives a channel its tFAW window and the clock that times it.
// Without WithTFAW the channel has no window.
func (b Builder) buildFAW(channel *org.ChannelImpl) {
	if b.tFAW == 0 {
		return
	}

	channel.Freq = b.freq
	channel.FAW = b.tFAW
}

// checkRefresh rejects refresh settings that cannot work: tRFC must be
// positive and shorter than tREFI, and a stagger needs refresh.
func (b Builder) checkRefresh() {
	if !b.allBankRefresh {
		if b.refreshSlots != 0 {
			panic("WithRefreshStagger needs WithAllBankRefresh(true)")
		}
		return
	}

	if b.tRFC <= 0 || b.tRFC >= b.tREFI {
		panic(fmt.Sprintf(
			"all-bank refresh needs 0 < tRFC (%d) < tREFI (%d)",
			b.tRFC, b.tREFI))
	}
}

// buildRefresh gives channel c of the controller its all-bank refresh, with
// the phase of its stagger slot. Without WithAllBankRefresh the channel has
// neither a refresh nor a clock.
func (b Builder) buildRefresh(channel *org.ChannelImpl, c int) {
	if !b.allBankRefresh {
		return
	}

	slot, numSlots := b.refreshSlot, max(1, b.refreshSlots)
	channel.Freq = b.freq
	channel.Refresh = &org.AllBankRefresh{
		Interval: b.tREFI,
		Duration: b.tRFC,
		Phase: org.StaggerRefreshPhase(
			slot, numSlots, c, max(1, b.numChannel), b.tREFI),
	}
}

// checkColumnPipelining rejects pipelined column commands on HBM without
// WithHBM2Turnaround, because a pipelined write completes at writeDelay,
// which only that option times from the write latency.
func (b Builder) checkColumnPipelining() {
	if !b.pipelinedColumns || !b.protocol.isHBM() || b.hbm2Turnaround {
		return
	}

	panic("pipelined column commands on HBM need WithHBM2Turnaround(true), " +
		"which times write data from the write latency (tWL + BL/2)")
}

func (b Builder) buildBanks(prefix string) org.Banks {
	banks := make(org.Banks, b.numRank)
	for i := 0; i < b.numRank; i++ {
		banks[i] = make([][]org.Bank, b.numBankGroup)

		for j := 0; j < b.numBankGroup; j++ {
			banks[i][j] = make([]org.Bank, b.numBank)

			for k := 0; k < b.numBank; k++ {
				bankName := fmt.Sprintf("%s.Bank[%d][%d][%d]",
					prefix, i, j, k)
				bank := org.NewBankImpl(bankName)
				bank.CmdCycles = [signal.NumCmdKind]int{
					signal.CmdKindRead:           b.readDelay,
					signal.CmdKindReadPrecharge:  b.tRP,
					signal.CmdKindWrite:          b.writeDelay,
					signal.CmdKindWritePrecharge: b.tRP,
					signal.CmdKindActivate:       b.tRCD - b.tAL,
					signal.CmdKindPrecharge:      b.tRP,
					signal.CmdKindRefreshBank:    1,
					signal.CmdKindRefresh:        1,
					signal.CmdKindSRefEnter:      1,
					signal.CmdKindSRefExit:       1,
				}

				if b.protocol.isGDDR() || b.protocol.isHBM() {
					bank.CmdCycles[signal.CmdKindActivate] = b.tRCDRD - b.tAL
				}

				if b.pipelinedColumns {
					// The command ends with its data transfer; the
					// precharge time is in the timing tables.
					bank.PipelineColumnCommands = true
					bank.CmdCycles[signal.CmdKindReadPrecharge] = b.readDelay
					bank.CmdCycles[signal.CmdKindWritePrecharge] =
						b.writeDelay
				}

				banks[i][j][k] = bank

				b.attachTracers(bank)
			}
		}
	}

	return banks
}

//nolint:gocyclo,funlen,govet
func (b *Builder) generateTiming() org.Timing {
	t := org.Timing{
		SameBank:              org.MakeTimeTable(),
		OtherBanksInBankGroup: org.MakeTimeTable(),
		SameRank:              org.MakeTimeTable(),
		OtherRanks:            org.MakeTimeTable(),
	}

	b.calculateBurstCycle()

	b.tRL = b.tAL + b.tCL
	b.tWL = b.tAL + b.tCWL
	b.readDelay = b.tRL + b.burstCycle
	b.writeDelay = b.tRL + b.burstCycle
	if b.hbm2Turnaround {
		// Write data starts tWL after the write command (JESD235).
		b.writeDelay = b.tWL + b.burstCycle
	}
	b.tRC = b.tRAS + b.tRP

	readToReadL := max(b.burstCycle, b.tCCDL)
	readToReadS := max(b.burstCycle, b.tCCDS)
	readToReadO := b.burstCycle + b.tRTRS
	readToWrite := b.tRL + b.burstCycle - b.tWL + b.tRTRS
	readToWriteO := b.readDelay + b.burstCycle +
		b.tRTRS - b.writeDelay
	readToPrecharge := b.tAL + b.tRTP
	readpToAct := b.tAL + b.burstCycle + b.tRTP + b.tRP

	writeToReadL := b.writeDelay + b.tWTRL
	writeToReadS := b.writeDelay + b.tWTRS
	writeToReadO := b.writeDelay + b.burstCycle +
		b.tRTRS - b.readDelay
	if b.hbm2Turnaround {
		// The stack IDs of a pseudo channel share its data bus, so the
		// turnarounds hold across them as within one stack ID (Ramulator 2
		// HBM2: nRTW and nCWL + nBL + nWTRS at pseudo-channel scope). As
		// there, read to write has one value for both stack IDs.
		readToWriteO = readToWrite
		writeToReadO = b.tWL + b.burstCycle + b.tWTRS
	}
	writeToWriteL := max(b.burstCycle, b.tCCDL)
	writeToWriteS := max(b.burstCycle, b.tCCDS)
	writeToWriteO := b.burstCycle
	writeToPrecharge := b.tWL + b.burstCycle + b.tWR

	prechargeToActivate := b.tRP
	prechargeToPrecharge := b.tPPD
	readToActivate := readToPrecharge + prechargeToActivate
	writeToActivate := writeToPrecharge + prechargeToActivate

	activateToActivate := b.tRC
	activateToActivateL := b.tRRDL
	activateToActivateS := b.tRRDS
	activateToPrecharge := b.tRAS
	activateToRead := b.tRCD - b.tAL
	activateToWrite := b.tRCD - b.tAL
	if b.protocol.isGDDR() || b.protocol.isHBM() {
		activateToRead = b.tRCDRD
		activateToWrite = b.tRCDWR
	}
	activateToRefresh := b.tRC // need to precharge before ref, so it's tRC

	refreshToRefresh := b.tREFI
	refreshToActivate := b.tRFC
	refreshToActivateBank := b.tRFCb

	selfRefreshEntryToExit := b.tCKESR
	selfRefreshExit := b.tXS

	if b.numBankGroup == 1 {
		// Bank-group can be disabled. In that case
		// the value of tXXX_S should be used instead of tXXX_L
		// (because now the device is running at a lower freq)
		// we overwrite the following values so that we don't have
		// to change the assignment of the vectors
		readToReadL = max(b.burstCycle, b.tCCDS)
		writeToReadL = b.writeDelay + b.tWTRS
		writeToWriteL = max(b.burstCycle, b.tCCDS)
		activateToActivateL = b.tRRDS
	}

	t.SameBank[signal.CmdKindRead] = []org.TimeTableEntry{
		{signal.CmdKindRead, readToReadL},
		{signal.CmdKindWrite, readToWrite},
		{signal.CmdKindReadPrecharge, readToReadL},
		{signal.CmdKindWritePrecharge, readToWrite},
		{signal.CmdKindPrecharge, readToPrecharge},
	}

	t.OtherBanksInBankGroup[signal.CmdKindRead] = []org.TimeTableEntry{
		{signal.CmdKindRead, readToReadL},
		{signal.CmdKindWrite, readToWrite},
		{signal.CmdKindReadPrecharge, readToReadL},
		{signal.CmdKindWritePrecharge, readToWrite},
	}
	t.SameRank[signal.CmdKindRead] = []org.TimeTableEntry{
		{signal.CmdKindRead, readToReadS},
		{signal.CmdKindWrite, readToWrite},
		{signal.CmdKindReadPrecharge, readToReadS},
		{signal.CmdKindWritePrecharge, readToWrite},
	}
	t.OtherRanks[signal.CmdKindRead] = []org.TimeTableEntry{
		{signal.CmdKindRead, readToReadO},
		{signal.CmdKindWrite, readToWriteO},
		{signal.CmdKindReadPrecharge, readToReadO},
		{signal.CmdKindWritePrecharge, readToWriteO},
	}

	t.SameBank[signal.CmdKindWrite] = []org.TimeTableEntry{
		{signal.CmdKindRead, writeToReadL},
		{signal.CmdKindWrite, writeToWriteL},
		{signal.CmdKindReadPrecharge, writeToReadL},
		{signal.CmdKindWritePrecharge, writeToWriteL},
		{signal.CmdKindPrecharge, writeToPrecharge}}
	t.OtherBanksInBankGroup[signal.CmdKindWrite] = []org.TimeTableEntry{
		{signal.CmdKindRead, writeToReadL},
		{signal.CmdKindWrite, writeToWriteL},
		{signal.CmdKindReadPrecharge, writeToReadL},
		{signal.CmdKindWritePrecharge, writeToWriteL}}
	t.SameRank[signal.CmdKindWrite] =
		[]org.TimeTableEntry{
			{signal.CmdKindRead, writeToReadS},
			{signal.CmdKindWrite, writeToWriteS},
			{signal.CmdKindReadPrecharge, writeToReadS},
			{signal.CmdKindWritePrecharge, writeToWriteS}}
	t.OtherRanks[signal.CmdKindWrite] =
		[]org.TimeTableEntry{
			{signal.CmdKindRead, writeToReadO},
			{signal.CmdKindWrite, writeToWriteO},
			{signal.CmdKindReadPrecharge, writeToReadO},
			{signal.CmdKindWritePrecharge, writeToWriteO}}

	// command READ_PRECHARGE
	t.SameBank[signal.CmdKindReadPrecharge] =
		[]org.TimeTableEntry{
			{signal.CmdKindActivate, readpToAct},
			{signal.CmdKindRefresh, readToActivate},
			{signal.CmdKindRefreshBank, readToActivate},
			{signal.CmdKindSRefEnter, readToActivate}}
	t.OtherBanksInBankGroup[signal.CmdKindReadPrecharge] =
		[]org.TimeTableEntry{
			{signal.CmdKindRead, readToReadL},
			{signal.CmdKindWrite, readToWrite},
			{signal.CmdKindReadPrecharge, readToReadL},
			{signal.CmdKindWritePrecharge, readToWrite}}
	t.SameRank[signal.CmdKindReadPrecharge] =
		[]org.TimeTableEntry{
			{signal.CmdKindRead, readToReadS},
			{signal.CmdKindWrite, readToWrite},
			{signal.CmdKindReadPrecharge, readToReadS},
			{signal.CmdKindWritePrecharge, readToWrite}}
	t.OtherRanks[signal.CmdKindReadPrecharge] =
		[]org.TimeTableEntry{
			{signal.CmdKindRead, readToReadO},
			{signal.CmdKindWrite, readToWriteO},
			{signal.CmdKindReadPrecharge, readToReadO},
			{signal.CmdKindWritePrecharge, readToWriteO}}

	// command WRITE_PRECHARGE
	t.SameBank[signal.CmdKindWritePrecharge] =
		[]org.TimeTableEntry{
			{signal.CmdKindActivate, writeToActivate},
			{signal.CmdKindRefresh, writeToActivate},
			{signal.CmdKindRefreshBank, writeToActivate},
			{signal.CmdKindSRefEnter, writeToActivate}}
	t.OtherBanksInBankGroup[signal.CmdKindWritePrecharge] =
		[]org.TimeTableEntry{
			{signal.CmdKindRead, writeToReadL},
			{signal.CmdKindWrite, writeToWriteL},
			{signal.CmdKindReadPrecharge, writeToReadL},
			{signal.CmdKindWritePrecharge, writeToWriteL}}
	t.SameRank[signal.CmdKindWritePrecharge] =
		[]org.TimeTableEntry{
			{signal.CmdKindRead, writeToReadS},
			{signal.CmdKindWrite, writeToWriteS},
			{signal.CmdKindReadPrecharge, writeToReadS},
			{signal.CmdKindWritePrecharge, writeToWriteS}}
	t.OtherRanks[signal.CmdKindWritePrecharge] =
		[]org.TimeTableEntry{
			{signal.CmdKindRead, writeToReadO},
			{signal.CmdKindWrite, writeToWriteO},
			{signal.CmdKindReadPrecharge, writeToReadO},
			{signal.CmdKindWritePrecharge, writeToWriteO}}

	// command ACTIVATE
	t.SameBank[signal.CmdKindActivate] =
		[]org.TimeTableEntry{
			{signal.CmdKindActivate, activateToActivate},
			{signal.CmdKindRead, activateToRead},
			{signal.CmdKindWrite, activateToWrite},
			{signal.CmdKindReadPrecharge, activateToRead},
			{signal.CmdKindWritePrecharge, activateToWrite},
			{signal.CmdKindPrecharge, activateToPrecharge},
		}

	t.OtherBanksInBankGroup[signal.CmdKindActivate] =
		[]org.TimeTableEntry{
			{signal.CmdKindActivate, activateToActivateL},
			{signal.CmdKindRefreshBank, activateToRefresh}}

	t.SameRank[signal.CmdKindActivate] =
		[]org.TimeTableEntry{
			{signal.CmdKindActivate, activateToActivateS},
			{signal.CmdKindRefreshBank, activateToRefresh}}

	if b.crossRankTRRD {
		// The ranks are the stack IDs of an HBM2 pseudo channel, which
		// applies tRRD across them.
		t.OtherRanks[signal.CmdKindActivate] =
			[]org.TimeTableEntry{
				{signal.CmdKindActivate, activateToActivateS}}
	}

	// command PRECHARGE
	t.SameBank[signal.CmdKindPrecharge] =
		[]org.TimeTableEntry{
			{signal.CmdKindActivate, prechargeToActivate},
			{signal.CmdKindRefresh, prechargeToActivate},
			{signal.CmdKindRefreshBank, prechargeToActivate},
			{signal.CmdKindSRefEnter, prechargeToActivate}}

	// for those who need tPPD
	if b.protocol.isGDDR() || b.protocol == LPDDR4 {
		t.OtherBanksInBankGroup[signal.CmdKindPrecharge] =
			[]org.TimeTableEntry{
				{signal.CmdKindPrecharge, prechargeToPrecharge},
			}

		t.SameRank[signal.CmdKindPrecharge] =
			[]org.TimeTableEntry{
				{signal.CmdKindPrecharge, prechargeToPrecharge},
			}
	}

	// command REFRESH_BANK
	t.SameRank[signal.CmdKindRefreshBank] =
		[]org.TimeTableEntry{
			{signal.CmdKindActivate, refreshToActivateBank},
			{signal.CmdKindRefresh, refreshToActivateBank},
			{signal.CmdKindRefreshBank, refreshToActivateBank},
			{signal.CmdKindSRefEnter, refreshToActivateBank}}

	t.OtherBanksInBankGroup[signal.CmdKindRefreshBank] =
		[]org.TimeTableEntry{
			{signal.CmdKindActivate, refreshToActivate},
			{signal.CmdKindRefreshBank, refreshToRefresh},
		}

	t.SameRank[signal.CmdKindRefreshBank] =
		[]org.TimeTableEntry{
			{signal.CmdKindActivate, refreshToActivate},
			{signal.CmdKindRefreshBank, refreshToRefresh},
		}

	// REFRESH, SREF_ENTER and SREF_EXIT are isued to the entire
	// rank  command REFRESH
	t.SameRank[signal.CmdKindRefresh] =
		[]org.TimeTableEntry{
			{signal.CmdKindActivate, refreshToActivate},
			{signal.CmdKindRefresh, refreshToActivate},
			{signal.CmdKindSRefEnter, refreshToActivate}}

	// command SREF_ENTER
	// TODO: add power down commands
	t.SameRank[signal.CmdKindSRefEnter] =
		[]org.TimeTableEntry{
			{signal.CmdKindSRefExit, selfRefreshEntryToExit}}

	// command SREF_EXIT
	t.SameRank[signal.CmdKindSRefExit] =
		[]org.TimeTableEntry{
			{signal.CmdKindActivate, selfRefreshExit},
			{signal.CmdKindRefresh, selfRefreshExit},
			{signal.CmdKindRefreshBank, selfRefreshExit},
			{signal.CmdKindSRefEnter, selfRefreshExit}}

	return t
}

func (b *Builder) calculateBurstCycle() {
	b.burstLengthMustNotBeZero()

	switch b.protocol {
	case GDDR5:
		b.burstCycle = b.burstLength / 4
	case GDDR5X:
		b.burstCycle = b.burstLength / 8
	case GDDR6:
		b.burstCycle = b.burstLength / 16
	default:
		b.burstCycle = b.burstLength / 2
	}
}

func (b *Builder) burstLengthMustNotBeZero() {
	if b.burstLength == 0 {
		panic("burst length cannot be 0")
	}
}

// log2 returns the log2 of a number. It also returns false if it is not a log2
// number.
func log2(n uint64) (uint64, bool) {
	oneCount := 0
	onePos := uint64(0)
	for i := uint64(0); i < 64; i++ {
		if n&(1<<i) > 0 {
			onePos = i
			oneCount++
		}
	}

	return onePos, oneCount == 1
}

func max(a, b int) int {
	if a > b {
		return a
	}

	return b
}
