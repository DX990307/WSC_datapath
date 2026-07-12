package dram

import (
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var physicalDRAMDistanceWindows = []uint64{0, 1, 2, 4, 8, 16, 32, 64}

// PhysicalDRAMCSVObserver writes lossless lifecycle events and a compact
// nearest-prior locality summary. If rawPath ends in .gz, raw events are gzip
// compressed. rawPath may be empty for summary-only collection.
//
// The summary uses actual mapped controller/bank/row/column fields and reports
// both cycle distance and intervening physical-read distance. It never infers
// DRAM geometry from addresses.
type PhysicalDRAMCSVObserver struct {
	mu sync.Mutex

	rawFile   *os.File
	rawCloser io.Closer
	rawWriter *csv.Writer

	summaryPath string
	closed      bool
	err         error

	sequenceByController map[physicalControllerKey]uint64
	cycleLocality        physicalLocalityTracker
	requestLocality      physicalLocalityTracker
	histograms           map[string]*physicalDistanceHistogram
}

type physicalDistanceKind uint8

const (
	physicalCycleDistance physicalDistanceKind = iota
	physicalRequestDistance
)

// physicalLocalityTracker keeps the candidates for one distance definition.
// The cycle and request trackers must be independent: a candidate can be old
// in controller cycles while still having few intervening reads, or vice
// versa. Expiry queues are controller-local because request sequence numbers
// are controller-local and events from shared observers can be interleaved.
type physicalLocalityTracker struct {
	distance physicalDistanceKind

	lastByUnit       map[physicalUnitKey]physicalRecentPair
	lastByRow        map[physicalRowKey]physicalRecentPair
	lastByBank       map[physicalBankKey]physicalRecentPair
	lastByController map[physicalControllerKey]physicalRecentPair

	unitExpiryByController map[physicalControllerKey]*physicalUnitExpiryQueue
	rowExpiryByController  map[physicalControllerKey]*physicalRowExpiryQueue
}

type physicalControllerKey struct {
	owner      string
	controller string
	index      int
}

type physicalBankKey struct {
	controller physicalControllerKey
	channel    uint64
	rank       uint64
	bankGroup  uint64
	bank       uint64
}

type physicalRowKey struct {
	bank physicalBankKey
	row  uint64
}

type physicalUnitKey struct {
	row    physicalRowKey
	column uint64
}

type physicalLocalitySample struct {
	cycle    uint64
	sequence uint64
	bank     physicalBankKey
	row      uint64
	column   uint64
	line64   uint64
}

type physicalRecentPair struct {
	latest          physicalLocalitySample
	latestAttribute physicalLocalityAttribute
	latestSet       bool
	other           physicalLocalitySample
	otherAttribute  physicalLocalityAttribute
	otherSet        bool
}

type physicalLocalityAttribute struct {
	kind      uint8
	value     uint64
	channel   uint64
	rank      uint64
	bankGroup uint64
	bank      uint64
}

type physicalDistanceHistogram struct {
	total      uint64
	cycleCDF   []uint64
	requestCDF []uint64
}

type physicalUnitExpiry struct {
	key    physicalUnitKey
	sample physicalLocalitySample
}

type physicalRowExpiry struct {
	key    physicalRowKey
	sample physicalLocalitySample
}

type physicalUnitExpiryQueue struct {
	items []physicalUnitExpiry
	head  int
}

type physicalRowExpiryQueue struct {
	items []physicalRowExpiry
	head  int
}

// NewPhysicalDRAMCSVObserver creates a physical DRAM observer. summaryPath may
// be empty; in that case it is derived from rawPath. At least one path must be
// non-empty. The caller owns the observer and must call Close after simulation.
func NewPhysicalDRAMCSVObserver(
	rawPath, summaryPath string,
) (*PhysicalDRAMCSVObserver, error) {
	if rawPath == "" && summaryPath == "" {
		return nil, fmt.Errorf("physical DRAM observer needs an output path")
	}
	if summaryPath == "" {
		summaryPath = physicalDRAMSummaryPath(rawPath)
	}

	o := &PhysicalDRAMCSVObserver{
		summaryPath:          summaryPath,
		sequenceByController: make(map[physicalControllerKey]uint64),
		cycleLocality:        newPhysicalLocalityTracker(physicalCycleDistance),
		requestLocality:      newPhysicalLocalityTracker(physicalRequestDistance),
		histograms:           make(map[string]*physicalDistanceHistogram),
	}
	for _, relation := range []string{
		"same_access_unit",
		"same_row_different_column",
		"same_bank_different_row",
		"different_bank_same_controller",
	} {
		o.histograms[relation] = &physicalDistanceHistogram{
			cycleCDF:   make([]uint64, len(physicalDRAMDistanceWindows)),
			requestCDF: make([]uint64, len(physicalDRAMDistanceWindows)),
		}
	}

	if rawPath != "" {
		if err := o.openRaw(rawPath); err != nil {
			return nil, err
		}
	}
	return o, nil
}

func physicalDRAMSummaryPath(rawPath string) string {
	path := strings.TrimSuffix(rawPath, ".gz")
	path = strings.TrimSuffix(path, ".csv")
	return path + ".summary.csv"
}

func (o *PhysicalDRAMCSVObserver) openRaw(path string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	o.rawFile = f

	var writer io.Writer = f
	if strings.HasSuffix(path, ".gz") {
		// Observation traces are large and generated on the simulator's hot
		// path. BestSpeed keeps instrumentation wall time bounded while CSV's
		// repeated fields still compress well.
		gzipWriter, err := gzip.NewWriterLevel(f, gzip.BestSpeed)
		if err != nil {
			_ = f.Close()
			return err
		}
		o.rawCloser = gzipWriter
		writer = gzipWriter
	}
	o.rawWriter = csv.NewWriter(writer)
	if err := o.rawWriter.Write(physicalDRAMRawHeader()); err != nil {
		_ = o.closeRaw()
		return err
	}
	return nil
}

func physicalDRAMRawHeader() []string {
	return []string{
		"event", "owner", "controller_name", "controller",
		"time_ps", "cycle", "request_id", "subtransaction_id",
		"command_id", "queued_command_id", "command_kind", "is_read",
		"external_address", "internal_address",
		"subtransaction_address", "internal_subtransaction_address",
		"request_bytes", "physical_bytes",
		"channel", "rank", "bank_group", "bank", "row", "column",
		"queue_depth_before", "queue_depth_after", "controller_queue_depth",
		"subtransaction_queue_depth",
		"open_row_valid", "open_row", "ready_same_open_row",
		"column_row_hit",
	}
}

// ObservePhysicalDRAM implements PhysicalDRAMObserver.
func (o *PhysicalDRAMCSVObserver) ObservePhysicalDRAM(e PhysicalDRAMEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.err != nil {
		return
	}

	if o.rawWriter != nil {
		if err := o.rawWriter.Write(physicalDRAMRawRow(e)); err != nil {
			o.err = err
			return
		}
	}
	if e.Kind == PhysicalDRAMSubtransactionArrive && e.IsRead {
		o.observeLocality(e)
	}
}

func physicalDRAMRawRow(e PhysicalDRAMEvent) []string {
	return []string{
		string(e.Kind), e.Owner, e.ControllerName, strconv.Itoa(e.Controller),
		strconv.FormatUint(e.TimePS, 10), strconv.FormatUint(e.Cycle, 10),
		e.RequestID, e.SubtransactionID, e.CommandID, e.QueuedCommandID,
		e.CommandKind,
		strconv.FormatBool(e.IsRead),
		fmt.Sprintf("0x%x", e.ExternalAddress),
		fmt.Sprintf("0x%x", e.InternalAddress),
		fmt.Sprintf("0x%x", e.SubtransactionAddress),
		fmt.Sprintf("0x%x", e.InternalSubtransactionAddress),
		strconv.FormatUint(e.RequestBytes, 10),
		strconv.FormatUint(e.PhysicalBytes, 10),
		strconv.FormatUint(e.Channel, 10), strconv.FormatUint(e.Rank, 10),
		strconv.FormatUint(e.BankGroup, 10), strconv.FormatUint(e.Bank, 10),
		strconv.FormatUint(e.Row, 10), strconv.FormatUint(e.Column, 10),
		strconv.Itoa(e.QueueDepthBefore), strconv.Itoa(e.QueueDepthAfter),
		strconv.Itoa(e.ControllerQueueDepth),
		strconv.Itoa(e.SubtransactionQueueDepth),
		strconv.FormatBool(e.OpenRowValid), strconv.FormatUint(e.OpenRow, 10),
		strconv.Itoa(e.ReadySameOpenRow), strconv.FormatBool(e.ColumnRowHit),
	}
}

func (o *PhysicalDRAMCSVObserver) observeLocality(e PhysicalDRAMEvent) {
	controller := physicalControllerKey{e.Owner, e.ControllerName, e.Controller}
	sequence := o.sequenceByController[controller]
	o.sequenceByController[controller] = sequence + 1
	bank := physicalBankKey{
		controller: controller,
		channel:    e.Channel,
		rank:       e.Rank,
		bankGroup:  e.BankGroup,
		bank:       e.Bank,
	}
	row := physicalRowKey{bank: bank, row: e.Row}
	unit := physicalUnitKey{row: row, column: e.Column}
	sample := physicalLocalitySample{
		cycle: e.Cycle, sequence: sequence, bank: bank,
		row: e.Row, column: e.Column, line64: e.ExternalAddress >> 6,
	}
	o.cycleLocality.expire(controller, sample)
	o.requestLocality.expire(controller, sample)

	// A same-unit opportunity must involve the other 64-B cacheline. Exact
	// duplicates are a coalescing/dedup opportunity, not adjacent-line batching.
	lineAttribute := physicalLocalityAttribute{kind: 0, value: sample.line64}
	cyclePrevious, cycleFound := o.cycleLocality.observeUnit(
		unit, sample, lineAttribute)
	requestPrevious, requestFound := o.requestLocality.observeUnit(
		unit, sample, lineAttribute)
	o.histograms["same_access_unit"].observe(
		sample, cyclePrevious, cycleFound, requestPrevious, requestFound)

	columnAttribute := physicalLocalityAttribute{kind: 1, value: sample.column}
	cyclePrevious, cycleFound = o.cycleLocality.observeRow(
		row, sample, columnAttribute)
	requestPrevious, requestFound = o.requestLocality.observeRow(
		row, sample, columnAttribute)
	o.histograms["same_row_different_column"].observe(
		sample, cyclePrevious, cycleFound, requestPrevious, requestFound)

	rowAttribute := physicalLocalityAttribute{kind: 2, value: sample.row}
	cyclePrevious, cycleFound = o.cycleLocality.observeBank(
		bank, sample, rowAttribute)
	requestPrevious, requestFound = o.requestLocality.observeBank(
		bank, sample, rowAttribute)
	o.histograms["same_bank_different_row"].observe(
		sample, cyclePrevious, cycleFound, requestPrevious, requestFound)

	bankAttribute := physicalLocalityAttribute{
		kind:      3,
		channel:   sample.bank.channel,
		rank:      sample.bank.rank,
		bankGroup: sample.bank.bankGroup,
		bank:      sample.bank.bank,
	}
	cyclePrevious, cycleFound = o.cycleLocality.observeController(
		controller, sample, bankAttribute)
	requestPrevious, requestFound = o.requestLocality.observeController(
		controller, sample, bankAttribute)
	o.histograms["different_bank_same_controller"].observe(
		sample, cyclePrevious, cycleFound, requestPrevious, requestFound)
}

func newPhysicalLocalityTracker(
	distance physicalDistanceKind,
) physicalLocalityTracker {
	return physicalLocalityTracker{
		distance:               distance,
		lastByUnit:             make(map[physicalUnitKey]physicalRecentPair),
		lastByRow:              make(map[physicalRowKey]physicalRecentPair),
		lastByBank:             make(map[physicalBankKey]physicalRecentPair),
		lastByController:       make(map[physicalControllerKey]physicalRecentPair),
		unitExpiryByController: make(map[physicalControllerKey]*physicalUnitExpiryQueue),
		rowExpiryByController:  make(map[physicalControllerKey]*physicalRowExpiryQueue),
	}
}

func (t *physicalLocalityTracker) observeUnit(
	key physicalUnitKey,
	sample physicalLocalitySample,
	attribute physicalLocalityAttribute,
) (physicalLocalitySample, bool) {
	pair := t.lastByUnit[key]
	previous, found := pair.previousDifferent(attribute)
	pair.add(sample, attribute)
	t.lastByUnit[key] = pair
	queue := t.unitExpiryQueue(key.row.bank.controller)
	queue.items = append(queue.items, physicalUnitExpiry{key, sample})
	return previous, found
}

func (t *physicalLocalityTracker) observeRow(
	key physicalRowKey,
	sample physicalLocalitySample,
	attribute physicalLocalityAttribute,
) (physicalLocalitySample, bool) {
	pair := t.lastByRow[key]
	previous, found := pair.previousDifferent(attribute)
	pair.add(sample, attribute)
	t.lastByRow[key] = pair
	queue := t.rowExpiryQueue(key.bank.controller)
	queue.items = append(queue.items, physicalRowExpiry{key, sample})
	return previous, found
}

func (t *physicalLocalityTracker) observeBank(
	key physicalBankKey,
	sample physicalLocalitySample,
	attribute physicalLocalityAttribute,
) (physicalLocalitySample, bool) {
	pair := t.lastByBank[key]
	pair.prune(sample, t.distance)
	previous, found := pair.previousDifferent(attribute)
	pair.add(sample, attribute)
	t.lastByBank[key] = pair
	return previous, found
}

func (t *physicalLocalityTracker) observeController(
	key physicalControllerKey,
	sample physicalLocalitySample,
	attribute physicalLocalityAttribute,
) (physicalLocalitySample, bool) {
	pair := t.lastByController[key]
	pair.prune(sample, t.distance)
	previous, found := pair.previousDifferent(attribute)
	pair.add(sample, attribute)
	t.lastByController[key] = pair
	return previous, found
}

func (t *physicalLocalityTracker) unitExpiryQueue(
	controller physicalControllerKey,
) *physicalUnitExpiryQueue {
	queue := t.unitExpiryByController[controller]
	if queue == nil {
		queue = &physicalUnitExpiryQueue{}
		t.unitExpiryByController[controller] = queue
	}
	return queue
}

func (t *physicalLocalityTracker) rowExpiryQueue(
	controller physicalControllerKey,
) *physicalRowExpiryQueue {
	queue := t.rowExpiryByController[controller]
	if queue == nil {
		queue = &physicalRowExpiryQueue{}
		t.rowExpiryByController[controller] = queue
	}
	return queue
}

func (t *physicalLocalityTracker) expire(
	controller physicalControllerKey,
	current physicalLocalitySample,
) {
	maxWindow := physicalDRAMDistanceWindows[len(physicalDRAMDistanceWindows)-1]
	if queue := t.unitExpiryByController[controller]; queue != nil {
		for queue.head < len(queue.items) {
			item := queue.items[queue.head]
			if !physicalSampleOlderThan(
				item.sample, current, maxWindow, t.distance) {
				break
			}
			if pair, ok := t.lastByUnit[item.key]; ok {
				pair.prune(current, t.distance)
				if pair.latestSet {
					t.lastByUnit[item.key] = pair
				} else {
					delete(t.lastByUnit, item.key)
				}
			}
			queue.head++
		}
		queue.compact()
		if queue.head == len(queue.items) {
			delete(t.unitExpiryByController, controller)
		}
	}
	if queue := t.rowExpiryByController[controller]; queue != nil {
		for queue.head < len(queue.items) {
			item := queue.items[queue.head]
			if !physicalSampleOlderThan(
				item.sample, current, maxWindow, t.distance) {
				break
			}
			if pair, ok := t.lastByRow[item.key]; ok {
				pair.prune(current, t.distance)
				if pair.latestSet {
					t.lastByRow[item.key] = pair
				} else {
					delete(t.lastByRow, item.key)
				}
			}
			queue.head++
		}
		queue.compact()
		if queue.head == len(queue.items) {
			delete(t.rowExpiryByController, controller)
		}
	}
}

func (q *physicalUnitExpiryQueue) compact() {
	if q.head > 4096 && q.head*2 >= len(q.items) {
		copy(q.items, q.items[q.head:])
		q.items = q.items[:len(q.items)-q.head]
		q.head = 0
	}
}

func (q *physicalRowExpiryQueue) compact() {
	if q.head > 4096 && q.head*2 >= len(q.items) {
		copy(q.items, q.items[q.head:])
		q.items = q.items[:len(q.items)-q.head]
		q.head = 0
	}
}

func physicalSampleOlderThan(
	sample, current physicalLocalitySample,
	window uint64,
	distance physicalDistanceKind,
) bool {
	if distance == physicalCycleDistance {
		return current.cycle > sample.cycle &&
			current.cycle-sample.cycle > window
	}
	return current.sequence > sample.sequence &&
		current.sequence-sample.sequence-1 > window
}

func (p physicalRecentPair) previousDifferent(
	attribute physicalLocalityAttribute,
) (physicalLocalitySample, bool) {
	if p.latestSet && p.latestAttribute != attribute {
		return p.latest, true
	}
	if p.otherSet && p.otherAttribute != attribute {
		return p.other, true
	}
	return physicalLocalitySample{}, false
}

func (p *physicalRecentPair) add(
	sample physicalLocalitySample,
	attribute physicalLocalityAttribute,
) {
	if !p.latestSet {
		p.latest, p.latestAttribute, p.latestSet = sample, attribute, true
		return
	}
	if p.latestAttribute == attribute {
		p.latest = sample
		return
	}
	p.other, p.otherAttribute, p.otherSet =
		p.latest, p.latestAttribute, true
	p.latest, p.latestAttribute = sample, attribute
}

func (p *physicalRecentPair) prune(
	current physicalLocalitySample,
	distance physicalDistanceKind,
) {
	maxWindow := physicalDRAMDistanceWindows[len(physicalDRAMDistanceWindows)-1]
	if p.latestSet && physicalSampleOlderThan(
		p.latest, current, maxWindow, distance) {
		p.latestSet = false
	}
	if p.otherSet && physicalSampleOlderThan(
		p.other, current, maxWindow, distance) {
		p.otherSet = false
	}
	if !p.latestSet && p.otherSet {
		p.latest, p.latestAttribute, p.latestSet =
			p.other, p.otherAttribute, true
		p.otherSet = false
	}
}

func (h *physicalDistanceHistogram) observe(
	current physicalLocalitySample,
	cyclePrevious physicalLocalitySample,
	cycleFound bool,
	requestPrevious physicalLocalitySample,
	requestFound bool,
) {
	h.total++
	if cycleFound && current.cycle >= cyclePrevious.cycle {
		cycleDistance := current.cycle - cyclePrevious.cycle
		for i, window := range physicalDRAMDistanceWindows {
			if cycleDistance <= window {
				h.cycleCDF[i]++
			}
		}
	}
	if requestFound && current.sequence > requestPrevious.sequence {
		requestDistance := current.sequence - requestPrevious.sequence - 1
		for i, window := range physicalDRAMDistanceWindows {
			if requestDistance <= window {
				h.requestCDF[i]++
			}
		}
	}
}

// Close flushes the raw trace and writes the summary. It is idempotent.
func (o *PhysicalDRAMCSVObserver) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return o.err
	}
	o.closed = true

	if err := o.closeRaw(); o.err == nil && err != nil {
		o.err = err
	}
	if err := o.writeSummary(); o.err == nil && err != nil {
		o.err = err
	}
	return o.err
}

func (o *PhysicalDRAMCSVObserver) closeRaw() error {
	var firstErr error
	if o.rawWriter != nil {
		o.rawWriter.Flush()
		firstErr = o.rawWriter.Error()
	}
	if o.rawCloser != nil {
		if err := o.rawCloser.Close(); firstErr == nil {
			firstErr = err
		}
	}
	if o.rawFile != nil {
		if err := o.rawFile.Close(); firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (o *PhysicalDRAMCSVObserver) writeSummary() error {
	if dir := filepath.Dir(o.summaryPath); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(o.summaryPath)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	if err := w.Write([]string{
		"relation", "distance", "direction", "window", "count", "total",
		"fraction", "matched_within_max_window",
	}); err != nil {
		_ = f.Close()
		return err
	}

	for _, relation := range []string{
		"same_access_unit",
		"same_row_different_column",
		"same_bank_different_row",
		"different_bank_same_controller",
	} {
		h := o.histograms[relation]
		for i, window := range physicalDRAMDistanceWindows {
			for _, item := range []struct {
				name  string
				count uint64
			}{
				{"cycles", h.cycleCDF[i]},
				{"intervening_requests", h.requestCDF[i]},
			} {
				fraction := 0.0
				if h.total > 0 {
					fraction = float64(item.count) / float64(h.total)
				}
				if err := w.Write([]string{
					relation, item.name, "nearest_prior",
					strconv.FormatUint(window, 10),
					strconv.FormatUint(item.count, 10),
					strconv.FormatUint(h.total, 10),
					strconv.FormatFloat(fraction, 'g', -1, 64),
					strconv.FormatUint(func() uint64 {
						if item.name == "cycles" {
							return h.cycleCDF[len(h.cycleCDF)-1]
						}
						return h.requestCDF[len(h.requestCDF)-1]
					}(), 10),
				}); err != nil {
					_ = f.Close()
					return err
				}
			}
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
