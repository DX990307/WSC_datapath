package dram

// Golden regression for the legacy DRAM controller configurations: the
// controllers that akkalat's createDramControllerBuilder builds for the hpca
// and mi300x-eighth GPM presets at commit 756f214. Every DRAM-model feature
// added later (mapping strip, pseudo-channels, row hold, refresh, tFAW, HBM2
// turnarounds) is off by default, so these runs must reproduce the hashes
// that were recorded on a git-archive export of 756f214 bit for bit.
//
// Each run drives one controller with a fixed request stream and hashes:
//   - rsp: every response in arrival order (request index, normalized request
//     and response IDs, arrival time in ps), followed by the number of IDs
//     the run drew from the global generator;
//   - stats: the row-continuation and physical-access counters;
//   - obs (instrumented runs): every PhysicalDRAMEvent, field by field;
//   - trace (instrumented runs): every tracer callback of the controller and
//     its banks.
//
// IDs are normalized against the first ID of the run, so the hashes do not
// depend on the tests that ran before. Only APIs that exist at 756f214 are
// used, so the same file compiles on both trees. Record a new table with
//
//	AKITA_DRAM_GOLDEN_RECORD=1 go test ./mem/dram/ \
//	    -run TestDRAMGoldenRegression -v
//
// and paste the logged entries into goldenDRAMRuns.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

// goldenDRAMResult is the outcome of one run. The counts are not part of the
// proof (the hashes cover them); they make a mismatch easier to diagnose.
type goldenDRAMResult struct {
	rsp, stats, obs, trace string

	responses, drawn, events, callbacks int
}

// goldenDRAMRuns was recorded on a git-archive export of 756f214 with
// AKITA_DRAM_GOLDEN_RECORD=1.
var goldenDRAMRuns = map[string]goldenDRAMResult{
	"hpca/plain/bare": {
		rsp:       "f9c1ebefeb0c275015c2450a9a11e8fdd2b7ad19c62772bb4227f6436f1f75f5",
		stats:     "1840e63a89fa21a45f3d5b2e859a89daf660aab14d15b2abe29fadeedf0389cc",
		responses: 3295, drawn: 47537,
	},
	"hpca/plain/instrumented": {
		rsp:       "f9c1ebefeb0c275015c2450a9a11e8fdd2b7ad19c62772bb4227f6436f1f75f5",
		stats:     "1840e63a89fa21a45f3d5b2e859a89daf660aab14d15b2abe29fadeedf0389cc",
		obs:       "f5f6758c3c7b3e1abf643a8c983b0dcad10ac34c31651ccddf2b2deaadae7e72",
		trace:     "080fe90903f10c2c996fb6c3d73fa261a8effc0baec1b2cd46b73ecf7a6abfc5",
		responses: 3295, drawn: 47537,
		events: 23845, callbacks: 27140,
	},
	"hpca/row-continuation/bare": {
		rsp:       "8ffe94687e8a0c327bf85f6eaffa8e3c4900be2e8d2c9864c1b96e10d275fd69",
		stats:     "a14265e0af784627da5eb1ed9a6f96f639dbf3e13761df9d1caaf58e7965e2ef",
		responses: 3295, drawn: 105418,
	},
	"hpca/row-continuation/instrumented": {
		rsp:       "8ffe94687e8a0c327bf85f6eaffa8e3c4900be2e8d2c9864c1b96e10d275fd69",
		stats:     "a14265e0af784627da5eb1ed9a6f96f639dbf3e13761df9d1caaf58e7965e2ef",
		obs:       "fa612538699943d17ffca1449254379bd260cb2698f0df90f1ca794d633e979a",
		trace:     "39693f95fce7a4a81056cfc9109b5268eff0551a172f84fd0780e0456769cdc6",
		responses: 3295, drawn: 105418,
		events: 21891, callbacks: 25186,
	},
	"hpca/aggregate/bare": {
		rsp:       "2878fcde60e3b6056638764efd462c202b86eb51bb62703a42636415656d41c6",
		stats:     "e4152eb735571128ff930074fbe34e43baa8c99c7b86d552876b5632a66a1925",
		responses: 3295, drawn: 116828,
	},
	"hpca/aggregate/instrumented": {
		rsp:       "2878fcde60e3b6056638764efd462c202b86eb51bb62703a42636415656d41c6",
		stats:     "e4152eb735571128ff930074fbe34e43baa8c99c7b86d552876b5632a66a1925",
		obs:       "33b37c824638f70eb45753a50ec3a293f15ee29c11da878386983d4144396c75",
		trace:     "143f38c1b687e4cf828ad4241333f95c1b1e779181cf6d05e0968f1de7068e9b",
		responses: 3295, drawn: 116828,
		events: 23845, callbacks: 27140,
	},
	"mi300x-eighth/plain/bare": {
		rsp:       "21425247af0a4e03a74ac4c31587ae9c231a4f82f717a44f2e3060bef1ffd213",
		stats:     "9999f242bc70f0745c863346e130ebe0b157434d23709ffeff19d86957550fb0",
		responses: 3295, drawn: 52923,
	},
	"mi300x-eighth/plain/instrumented": {
		rsp:       "21425247af0a4e03a74ac4c31587ae9c231a4f82f717a44f2e3060bef1ffd213",
		stats:     "9999f242bc70f0745c863346e130ebe0b157434d23709ffeff19d86957550fb0",
		obs:       "74bf109af584e89a462dd0df35f0090cedfe849fa943f37c97c59b3ee8e51b70",
		trace:     "ef1fe319e1f3a87e407323570707cc362caa298a42187b47d60ea5c56a3792a9",
		responses: 3295, drawn: 52923,
		events: 23813, callbacks: 27108,
	},
	"mi300x-eighth/row-continuation/bare": {
		rsp:       "a8307548f2acb802ebfcb03a503b65cedeb2b7f7251ee3c772e8a13f6c6d21e7",
		stats:     "ec111744d974bae468e563f876fca047a680a88af717fb2a0a91760e55303d26",
		responses: 3295, drawn: 97297,
	},
	"mi300x-eighth/row-continuation/instrumented": {
		rsp:       "a8307548f2acb802ebfcb03a503b65cedeb2b7f7251ee3c772e8a13f6c6d21e7",
		stats:     "ec111744d974bae468e563f876fca047a680a88af717fb2a0a91760e55303d26",
		obs:       "1c56f6c734e38337de6a773a97a1445a83307bb1731d6380a211de225f5f08cb",
		trace:     "7dc6fed7bb62e63e8e26525cf51bba89e313595252b935e39448affeb24099e5",
		responses: 3295, drawn: 97297,
		events: 22241, callbacks: 25536,
	},
	"mi300x-eighth/aggregate/bare": {
		rsp:       "d0b9c5667e86e252da5fa156689aca374b4d30ea4f7eb649a41dfbc50aa9a793",
		stats:     "10fba332fe5aba92b26c0cc42c77042f826ddda6dea5564437700aff580378c3",
		responses: 3295, drawn: 97659,
	},
	"mi300x-eighth/aggregate/instrumented": {
		rsp:       "d0b9c5667e86e252da5fa156689aca374b4d30ea4f7eb649a41dfbc50aa9a793",
		stats:     "10fba332fe5aba92b26c0cc42c77042f826ddda6dea5564437700aff580378c3",
		obs:       "c0404fef17f11b2c488faed944f56ee86b8a646cd9a0188e0c15f102846a634f",
		trace:     "09f6c54edab40305f0dd4a6833fa2fbef58eb6f4ecc13042894ade80c8608f6c",
		responses: 3295, drawn: 97659,
		events: 23395, callbacks: 26690,
	},
}

// goldenIDNorm maps the decimal IDs of the sequential generator to offsets
// from the first ID of a run: n becomes n-base, n@domain (MsgIDAtReceiver)
// becomes (n-base)@domain and n_suffix (the "_req_out" parent of a req_in
// task) becomes (n-base)_suffix. Other strings are kept.
type goldenIDNorm struct {
	base    uint64
	foreign []string
}

func (n *goldenIDNorm) id(s string) string {
	end := strings.IndexFunc(s, func(r rune) bool {
		return r < '0' || r > '9'
	})
	if end < 0 {
		end = len(s)
	}

	if end == 0 || (end < len(s) && s[end] != '@' && s[end] != '_') {
		return s
	}

	v, err := strconv.ParseUint(s[:end], 10, 64)
	if err != nil {
		return s
	}

	if v < n.base {
		// An ID drawn before the run started: state leaked between runs.
		n.foreign = append(n.foreign, s)
		return s
	}

	return strconv.FormatUint(v-n.base, 10) + s[end:]
}

// goldenHash streams lines into a SHA-256 digest.
type goldenHash struct {
	h hash.Hash
	n int
}

func newGoldenHash() *goldenHash {
	return &goldenHash{h: sha256.New()}
}

func (g *goldenHash) line(format string, args ...interface{}) {
	fmt.Fprintf(g.h, format, args...)
	g.h.Write([]byte{'\n'})
	g.n++
}

func (g *goldenHash) sum() string {
	return hex.EncodeToString(g.h.Sum(nil))
}

// goldenTime writes a simulation time exactly (shortest round-trip form).
func goldenTime(t sim.VTimeInSec) string {
	return strconv.FormatFloat(float64(t), 'g', -1, 64)
}

type goldenObserver struct {
	norm *goldenIDNorm
	out  *goldenHash
}

func (o *goldenObserver) ObservePhysicalDRAM(e PhysicalDRAMEvent) {
	o.out.line("%s|%s|%s|%d|%d|%d"+
		"|%s|%s|%s|%s|%s|%t"+
		"|%d|%d|%d|%d|%d|%d"+
		"|%d|%d|%d|%d|%d|%d"+
		"|%d|%d|%d|%d|%t|%d|%d|%t",
		e.Kind, e.Owner, e.ControllerName, e.Controller, e.TimePS, e.Cycle,
		o.norm.id(e.RequestID), o.norm.id(e.SubtransactionID),
		o.norm.id(e.CommandID), o.norm.id(e.QueuedCommandID),
		e.CommandKind, e.IsRead,
		e.ExternalAddress, e.InternalAddress, e.SubtransactionAddress,
		e.InternalSubtransactionAddress, e.RequestBytes, e.PhysicalBytes,
		e.Channel, e.Rank, e.BankGroup, e.Bank, e.Row, e.Column,
		e.QueueDepthBefore, e.QueueDepthAfter, e.ControllerQueueDepth,
		e.SubtransactionQueueDepth, e.OpenRowValid, e.OpenRow,
		e.ReadySameOpenRow, e.ColumnRowHit)
}

type goldenTracer struct {
	norm *goldenIDNorm
	out  *goldenHash
}

func (t *goldenTracer) record(op string, task tracing.Task) {
	t.out.line("%s|%s|%s|%s|%s|%s|%s|%s|%d",
		op, t.norm.id(task.ID), t.norm.id(task.ParentID),
		task.Kind, task.What, task.Where,
		goldenTime(task.StartTime), goldenTime(task.EndTime),
		len(task.Steps))
}

func (t *goldenTracer) StartTask(task tracing.Task) { t.record("Start", task) }
func (t *goldenTracer) StepTask(task tracing.Task)  { t.record("Step", task) }
func (t *goldenTracer) EndTask(task tracing.Task)   { t.record("End", task) }

// goldenAgent sends a prepared request list and keeps at most
// goldenMaxOutstanding responses outstanding.
type goldenAgent struct {
	*sim.TickingComponent

	port sim.Port

	reqs        []sim.Msg
	weights     []int
	next        int
	outstanding int
	index       map[string]int

	norm      *goldenIDNorm
	out       *goldenHash
	responses int
}

const goldenMaxOutstanding = 32

func (a *goldenAgent) Tick(now sim.VTimeInSec) bool {
	madeProgress := false

	for {
		msg := a.port.Retrieve(now)
		if msg == nil {
			break
		}

		var respondTo string
		switch rsp := msg.(type) {
		case *mem.DataReadyRsp:
			respondTo = rsp.RespondTo
		case *mem.WriteDoneRsp:
			respondTo = rsp.RespondTo
		default:
			panic(fmt.Sprintf("unexpected response %T", msg))
		}

		index, ok := a.index[respondTo]
		if !ok {
			panic("response to an unknown request " + respondTo)
		}

		a.out.line("%d|%s|%s|%d", index, a.norm.id(respondTo),
			a.norm.id(msg.Meta().ID),
			uint64(math.Round(float64(now)*1e12)))
		a.outstanding--
		a.responses++
		madeProgress = true
	}

	if a.next < len(a.reqs) &&
		a.outstanding+a.weights[a.next] <= goldenMaxOutstanding {
		req := a.reqs[a.next]
		req.Meta().SendTime = now
		if a.port.Send(req) == nil {
			a.outstanding += a.weights[a.next]
			a.next++
			madeProgress = true
		}
	}

	return madeProgress
}

// goldenRequests builds 3,000 requests in a 256-MB window: 64-B reads (70%),
// 64-B writes (20%) and paired 64-B reads (10%), with 30% of the addresses
// continuing the previous one. Paired-read IDs are fixed strings, so they draw
// no ID.
func (a *goldenAgent) goldenRequests(dst sim.Port) {
	const window = 256 * mem.MB

	rng := rand.New(rand.NewSource(1))
	addr := uint64(0)

	for i := 0; i < 3000; i++ {
		if rng.Intn(10) < 3 {
			addr = (addr + 64) % window
		} else {
			addr = uint64(rng.Intn(int(window/64))) * 64
		}

		switch r := rng.Intn(10); {
		case r < 7:
			req := mem.ReadReqBuilder{}.
				WithAddress(addr).
				WithByteSize(64).
				WithSrc(a.port).
				WithDst(dst).
				Build()
			a.add(req, 1, i, req.ID)
		case r < 9:
			data := make([]byte, 64)
			for k := range data {
				data[k] = byte(i + k)
			}

			req := mem.WriteReqBuilder{}.
				WithAddress(addr).
				WithData(data).
				WithSrc(a.port).
				WithDst(dst).
				Build()
			a.add(req, 1, i, req.ID)
		default:
			pairBase := addr &^ 127
			pairID := fmt.Sprintf("pair-%d", i)
			demand := mem.ReadReqBuilder{}.
				WithAddress(pairBase).
				WithByteSize(64).
				WithPairedRead(pairID, mem.PairedReadDemand).
				WithSrc(a.port).
				WithDst(dst).
				Build()
			sibling := mem.ReadReqBuilder{}.
				WithAddress(pairBase+64).
				WithByteSize(64).
				WithPairedRead(pairID, mem.PairedReadSibling).
				WithSrc(a.port).
				WithDst(dst).
				Build()
			pair := mem.PairedReadReqBuilder{}.
				WithReads(demand, sibling).
				WithSrc(a.port).
				WithDst(dst).
				Build()
			a.add(pair, 2, i, demand.ID, sibling.ID)
		}
	}
}

func (a *goldenAgent) add(req sim.Msg, weight, index int, ids ...string) {
	a.reqs = append(a.reqs, req)
	a.weights = append(a.weights, weight)
	for _, id := range ids {
		a.index[id] = index
	}
}

// goldenHPCABuilder is the hpca controller of createDramControllerBuilder at
// 756f214 with -num-memory-banks=4 (16 ranks from the capacity formula).
func goldenHPCABuilder() Builder {
	return MakeBuilder().
		WithFreq(500 * sim.MHz).
		WithProtocol(HBM).
		WithBurstLength(4).
		WithDeviceWidth(256).
		WithBusWidth(256).
		WithNumChannel(1).
		WithNumRank(16).
		WithNumBankGroup(4).
		WithNumBank(4).
		WithNumCol(64).
		WithNumRow(16384).
		WithCommandQueueSize(8).
		WithTransactionQueueSize(32).
		WithTCL(7).
		WithTCWL(2).
		WithTRCDRD(7).
		WithTRCDWR(7).
		WithTRP(7).
		WithTRAS(17).
		WithTREFI(1950).
		WithTRRDS(2).
		WithTRRDL(3).
		WithTWTRS(3).
		WithTWTRL(4).
		WithTWR(8).
		WithTCCDS(1).
		WithTCCDL(1).
		WithTRTRS(0).
		WithTRTP(3).
		WithTPPD(2)
}

// goldenMI300XEighthBuilder is the mi300x-eighth controller of
// createDramControllerBuilder at 756f214 with -num-memory-banks=16 (16 ranks
// from the capacity formula).
func goldenMI300XEighthBuilder() Builder {
	return MakeBuilder().
		WithFreq(1230 * sim.MHz).
		WithProtocol(HBM).
		WithBurstLength(8).
		WithDeviceWidth(64).
		WithBusWidth(64).
		WithNumChannel(1).
		WithNumRank(16).
		WithNumBankGroup(4).
		WithNumBank(4).
		WithNumCol(64).
		WithNumRow(16384).
		WithCommandQueueSize(8).
		WithTransactionQueueSize(32).
		WithTCL(17).
		WithTCWL(5).
		WithTRCDRD(17).
		WithTRCDWR(17).
		WithTRP(17).
		WithTRAS(42).
		WithTREFI(4797).
		WithTRRDS(5).
		WithTRRDL(7).
		WithTWTRS(7).
		WithTWTRL(10).
		WithTWR(20).
		WithTCCDS(2).
		WithTCCDL(2).
		WithTRTRS(0).
		WithTRTP(7).
		WithTPPD(5)
}

func goldenStatsHash(c *MemController) string {
	out := newGoldenHash()

	rc := c.GetRowContinuationStats()
	out.line("%t|%t|%d|%d|%d|%d|%d|%d|%d|%d|%d",
		rc.Enabled, rc.AggregateEnabled, rc.CommandsIssued, rc.ColumnCommands,
		rc.RowReuseHits, rc.AutoPrechargeStops,
		rc.AggregateAutoPrechargeStops, rc.AggregateImmediateContinues,
		rc.ActivateCommands, rc.PrechargeCommands, rc.MaxQueueAgeCycles)

	pa := c.GetPhysicalAccessStats()
	out.line("%d|%d|%d|%d|%d|%d|%d|%d|%d|%d|%d",
		pa.AccessBytes, pa.ReadAccesses, pa.WriteAccesses,
		pa.FrontEndReadRequests, pa.FrontEndWriteRequests,
		pa.FrontEndReadBytes, pa.FrontEndWriteBytes,
		pa.PairedReadDescriptors, pa.PairedReadMembers,
		pa.PairedDemandMembers, pa.PairedSiblingMembers)

	return out.sum()
}

func goldenNextID(t *testing.T) uint64 {
	id := sim.GetIDGenerator().Generate()
	v, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		t.Fatalf("the golden test needs the sequential ID generator, "+
			"got ID %q (parallel generator?)", id)
	}

	return v
}

func goldenRun(
	t *testing.T,
	builder Builder,
	instrumented bool,
) goldenDRAMResult {
	norm := &goldenIDNorm{base: goldenNextID(t)}
	rspOut := newGoldenHash()
	obsOut := newGoldenHash()
	traceOut := newGoldenHash()

	engine := sim.NewSerialEngine()
	builder = builder.
		WithEngine(engine).
		WithGlobalStorage(mem.NewStorage(1 * mem.GB))
	if instrumented {
		builder = builder.WithAdditionalTracer(
			&goldenTracer{norm: norm, out: traceOut})
	}

	ctrl := builder.Build("GPU[1].DRAM[3]")
	if instrumented {
		ctrl.SetPhysicalDRAMObserver(
			&goldenObserver{norm: norm, out: obsOut}, "golden", 0)
	}

	agent := &goldenAgent{
		index: make(map[string]int),
		norm:  norm,
		out:   rspOut,
	}
	agent.TickingComponent = sim.NewTickingComponent(
		"GoldenAgent", engine, 1*sim.GHz, agent)
	agent.port = sim.NewLimitNumMsgPort(agent, 64, "GoldenAgent.Port")

	conn := sim.NewDirectConnection("GoldenConn", engine, 1*sim.GHz)
	conn.PlugIn(agent.port, 64)
	conn.PlugIn(ctrl.topPort, 64)

	agent.goldenRequests(ctrl.topPort)
	agent.TickLater(0)

	if err := engine.Run(); err != nil {
		t.Fatal(err)
	}

	if agent.next != len(agent.reqs) || agent.outstanding != 0 {
		t.Fatalf("run incomplete: %d of %d requests sent, %d outstanding",
			agent.next, len(agent.reqs), agent.outstanding)
	}

	drawn := int(goldenNextID(t) - norm.base)
	rspOut.line("drawn=%d", drawn)

	if len(norm.foreign) > 0 {
		t.Fatalf("%d IDs predate the run, first %q",
			len(norm.foreign), norm.foreign[0])
	}

	r := goldenDRAMResult{
		rsp:       rspOut.sum(),
		stats:     goldenStatsHash(ctrl),
		responses: agent.responses,
		drawn:     drawn,
	}
	if instrumented {
		r.obs = obsOut.sum()
		r.trace = traceOut.sum()
		r.events = obsOut.n
		r.callbacks = traceOut.n
	}

	return r
}

func goldenCompare(t *testing.T, name string, got goldenDRAMResult) {
	want, ok := goldenDRAMRuns[name]
	if !ok {
		t.Errorf("%s: no golden entry", name)
		return
	}

	if got.responses != want.responses || got.drawn != want.drawn ||
		got.events != want.events || got.callbacks != want.callbacks {
		t.Errorf("%s: counts (responses, drawn, events, callbacks) = "+
			"(%d, %d, %d, %d), want (%d, %d, %d, %d)", name,
			got.responses, got.drawn, got.events, got.callbacks,
			want.responses, want.drawn, want.events, want.callbacks)
	}

	for _, h := range []struct{ field, got, want string }{
		{"rsp", got.rsp, want.rsp},
		{"stats", got.stats, want.stats},
		{"obs", got.obs, want.obs},
		{"trace", got.trace, want.trace},
	} {
		if h.got != h.want {
			t.Errorf("%s: %s hash %s, want %s", name, h.field, h.got, h.want)
		}
	}
}

// goldenEntry formats one run as a goldenDRAMRuns entry.
func goldenEntry(name string, r goldenDRAMResult) string {
	s := fmt.Sprintf("\t%q: {\n\t\trsp:   %q,\n\t\tstats: %q,\n",
		name, r.rsp, r.stats)
	if r.obs != "" || r.trace != "" {
		s += fmt.Sprintf("\t\tobs:   %q,\n\t\ttrace: %q,\n", r.obs, r.trace)
	}
	s += fmt.Sprintf("\t\tresponses: %d, drawn: %d,\n", r.responses, r.drawn)
	if r.events != 0 || r.callbacks != 0 {
		s += fmt.Sprintf("\t\tevents: %d, callbacks: %d,\n",
			r.events, r.callbacks)
	}

	return s + "\t},"
}

// TestDRAMGoldenRegression runs 2 presets x 3 scheduler modes x {bare,
// instrumented}. It must not run in parallel: the ID generator is global.
func TestDRAMGoldenRegression(t *testing.T) {
	record := os.Getenv("AKITA_DRAM_GOLDEN_RECORD") == "1"

	presets := []struct {
		name    string
		builder func() Builder
	}{
		{"hpca", goldenHPCABuilder},
		{"mi300x-eighth", goldenMI300XEighthBuilder},
	}
	modes := []struct {
		name                       string
		rowContinuation, aggregate bool
	}{
		{"plain", false, false},
		{"row-continuation", true, false},
		{"aggregate", false, true},
	}

	var entries []string
	for _, p := range presets {
		for _, m := range modes {
			// Same order as createDramControllerBuilder.
			b := p.builder().
				WithAggregateContinuation(m.aggregate).
				WithRowContinuation(m.rowContinuation)
			prefix := p.name + "/" + m.name

			bare := goldenRun(t, b, false)
			instrumented := goldenRun(t, b, true)

			if instrumented.rsp != bare.rsp || instrumented.stats != bare.stats {
				t.Errorf("%s: the observer or the tracer changed the run: "+
					"rsp %s / %s, stats %s / %s", prefix,
					bare.rsp, instrumented.rsp, bare.stats, instrumented.stats)
			}

			if instrumented.events == 0 || instrumented.callbacks == 0 {
				t.Errorf("%s: instrumented run saw %d events and %d tracer "+
					"callbacks", prefix, instrumented.events,
					instrumented.callbacks)
			}

			if record {
				entries = append(entries,
					goldenEntry(prefix+"/bare", bare),
					goldenEntry(prefix+"/instrumented", instrumented))
				continue
			}

			goldenCompare(t, prefix+"/bare", bare)
			goldenCompare(t, prefix+"/instrumented", instrumented)
		}
	}

	if record {
		t.Logf("goldenDRAMRuns entries:\n%s", strings.Join(entries, "\n"))
	}
}
