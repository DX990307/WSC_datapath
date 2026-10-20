package runner

// Per-GPM, per-kernel statistics for T16 (position-induced load imbalance).
//
// The tracer is statistics only: it attaches passive akita tracing hooks to
// the driver, the CUs, the L1V caches and the L2 TLBs, and never sends a
// message or schedules an event, so it cannot change timing. It is off by
// default (-report-gpm-trace).
//
// Kernel and GPM identities:
//   - A kernel is one driver command that sends LaunchKernelReqs; kernels are
//     numbered 0, 1, ... in the order of their first LaunchKernelReq.
//   - The GPM index is the position in Platform.GPUs, which equals the
//     position in the unified GPU's UnifiedGPUIDs (driver IDs 1..48 in
//     order, see createUnifiedGPUs); the driver ID is index+1.
//   - The tile t comes from the component name GPU[t]; x = t % w, y = t / w (w = -mesh-size, default 7).
//
// Events are attributed to the kernel that the GPM was last launched with.

import (
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
	"github.com/sarchlab/mgpusim/v3/kernels"
	"github.com/sarchlab/mgpusim/v3/protocol"
)

// gpmTraceMeshWidth is the mesh width (-mesh-size, default 7); the center
// tile is (w/2, w/2).
func gpmTraceMeshWidth() int { return MeshSize() }

// gpmTraceGPM describes one GPM's position.
type gpmTraceGPM struct {
	Index        int // position in UnifiedGPUIDs (0-based)
	DriverID     int
	Name         string
	Tile         int
	X, Y         int
	NumCU        int
	HopsToCenter int
	MeanHops     float64 // mean Manhattan hops to every other GPM
}

// latencyCounter counts requests and the sum of their latencies.
type latencyCounter struct {
	Count uint64
	SumNS float64
}

func (c *latencyCounter) add(latNS float64) {
	c.Count++
	c.SumNS += latNS
}

func (c latencyCounter) mean() float64 {
	if c.Count == 0 {
		return math.NaN()
	}
	return c.SumNS / float64(c.Count)
}

// gpmKernelStats holds one GPM's statistics for one kernel.
type gpmKernelStats struct {
	Assigned    int
	Launched    bool
	LaunchNS    float64
	EndedKernel bool
	KernelEndNS float64

	Started      uint64
	Completed    uint64
	FirstStartNS float64
	LastEndNS    float64
	SumWGDurNS   float64
	TimedWGs     uint64 // completed WGs whose start was seen
	// ActiveNS: time this GPM had at least one WG of the kernel running
	// (P16: "every GPM in use").
	ActiveNS float64

	L1VReadLocal                            latencyCounter
	L1VReadRemote                           latencyCounter
	L1VWriteLocal                           latencyCounter
	L1VWriteRemote                          latencyCounter
	L1VReadRemoteIssued, L1VReadLocalIssued uint64

	TLBMissLocal                            latencyCounter
	TLBMissRemote                           latencyCounter
	TLBMissRemoteIssued, TLBMissLocalIssued uint64
}

type gpmTraceKernel struct {
	ID       int
	CmdID    string
	Single   bool // single-GPM launches grouped into a phase
	TotalWG  int
	LaunchNS float64
	Ended    bool
	EndNS    float64
	PerGPM   []gpmKernelStats
	// AllActiveNS: time every GPM had at least one running WG.
	AllActiveNS float64
}

type gpmTraceWG struct {
	kernel, gpm int
	startNS     float64
}

type gpmTraceMemReq struct {
	kernel, gpm   int
	remote, write bool
	startNS       float64
}

type gpmTraceLaunch struct {
	kernel, gpm int
}

// gpmTraceAgg aggregates the per-kernel per-GPM statistics. It is driven by
// explicit calls with the current time (ns), so that it can be unit tested
// without a simulation.
type gpmTraceAgg struct {
	sync.Mutex

	gpms      []gpmTraceGPM
	kernels   []*gpmTraceKernel
	curKernel []int

	kernelByCmd map[string]int
	openSingle  int
	launches    map[string]gpmTraceLaunch
	wgs         map[string]gpmTraceWG
	l1vReqs     map[string]gpmTraceMemReq
	tlbReqs     map[string]gpmTraceMemReq

	// Running WGs per GPM (tracked WGs only) and since when each GPM, and
	// all GPMs together, have been busy.
	running     []int
	activeSince []float64
	numActive   int
	allSince    float64
}

func newGPMTraceAgg(gpms []gpmTraceGPM) *gpmTraceAgg {
	a := &gpmTraceAgg{
		gpms:        gpms,
		curKernel:   make([]int, len(gpms)),
		kernelByCmd: map[string]int{},
		openSingle:  -1,
		launches:    map[string]gpmTraceLaunch{},
		wgs:         map[string]gpmTraceWG{},
		l1vReqs:     map[string]gpmTraceMemReq{},
		tlbReqs:     map[string]gpmTraceMemReq{},
		running:     make([]int, len(gpms)),
		activeSince: make([]float64, len(gpms)),
	}
	for i := range a.curKernel {
		a.curKernel[i] = -1
	}
	return a
}

// assignWGs replicates driver.distributeWGToGPUs: every GPM, in
// UnifiedGPUIDs order, gets numCU * ceil(total / totalCU) contiguous WGs
// until the WGs run out.
func assignWGs(totalWG int, numCUs []int) []int {
	out := make([]int, len(numCUs))
	totalCU := 0
	for _, n := range numCUs {
		totalCU += n
	}
	if totalWG <= 0 || totalCU == 0 {
		return out
	}
	wgPerCU := (totalWG-1)/totalCU + 1
	left := totalWG
	for i, n := range numCUs {
		k := n * wgPerCU
		if k > left {
			k = left
		}
		out[i] = k
		left -= k
	}
	return out
}

func meshHops(x0, y0, x1, y1 int) int {
	dx, dy := x0-x1, y0-y1
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}

// newGPMTraceGPMs fills the position columns from tile numbers.
func newGPMTraceGPMs(names []string, tiles []int, numCUs []int) []gpmTraceGPM {
	gpms := make([]gpmTraceGPM, len(tiles))
	for i, t := range tiles {
		gpms[i] = gpmTraceGPM{
			Index:    i,
			DriverID: i + 1,
			Name:     names[i],
			Tile:     t,
			X:        t % gpmTraceMeshWidth(),
			Y:        t / gpmTraceMeshWidth(),
			NumCU:    numCUs[i],
		}
		gpms[i].HopsToCenter = meshHops(
			gpms[i].X, gpms[i].Y, gpmTraceMeshWidth()/2, gpmTraceMeshWidth()/2)
	}
	for i := range gpms {
		sum := 0
		for j := range gpms {
			if i != j {
				sum += meshHops(gpms[i].X, gpms[i].Y, gpms[j].X, gpms[j].Y)
			}
		}
		if len(gpms) > 1 {
			gpms[i].MeanHops = float64(sum) / float64(len(gpms)-1)
		}
	}
	return gpms
}

// kernelLaunch records a LaunchKernelReq sent by the driver to a GPM.
//
// A unified launch (the driver splits one kernel over all GPMs with a WG
// filter) is one kernel, identified by its driver command. A single-GPM
// launch (no WG filter, e.g. platbench's SPMD phases, one queue per GPM) runs
// the whole grid on that GPM; consecutive single-GPM launches to distinct
// GPMs are grouped into one kernel ("phase"), and a second launch to a GPM
// that is already in the open phase starts a new one.
func (a *gpmTraceAgg) kernelLaunch(
	cmdID, launchTaskID string, gpm, totalWG int, unified bool, nowNS float64,
) int {
	a.Lock()
	defer a.Unlock()

	k := -1
	if unified {
		a.openSingle = -1
		if kk, ok := a.kernelByCmd[cmdID]; ok {
			k = kk
		} else {
			numCUs := make([]int, len(a.gpms))
			for i, g := range a.gpms {
				numCUs[i] = g.NumCU
			}
			kern := a.newKernel(cmdID, totalWG, nowNS)
			for i, n := range assignWGs(totalWG, numCUs) {
				kern.PerGPM[i].Assigned = n
			}
			k = kern.ID
			a.kernelByCmd[cmdID] = k
		}
	} else {
		if gpm < 0 || gpm >= len(a.gpms) {
			return -1
		}
		if a.openSingle < 0 || a.kernels[a.openSingle].PerGPM[gpm].Launched {
			a.openSingle = a.newKernel("", 0, nowNS).ID
		}
		k = a.openSingle
		a.kernels[k].Single = true
		a.kernels[k].TotalWG += totalWG
		a.kernels[k].PerGPM[gpm].Assigned = totalWG
	}
	if gpm < 0 || gpm >= len(a.gpms) {
		return k
	}
	s := &a.kernels[k].PerGPM[gpm]
	s.Launched = true
	s.LaunchNS = nowNS
	a.curKernel[gpm] = k
	a.launches[launchTaskID] = gpmTraceLaunch{kernel: k, gpm: gpm}
	return k
}

func (a *gpmTraceAgg) newKernel(cmdID string, totalWG int, nowNS float64) *gpmTraceKernel {
	kern := &gpmTraceKernel{
		ID:       len(a.kernels),
		CmdID:    cmdID,
		TotalWG:  totalWG,
		LaunchNS: nowNS,
		PerGPM:   make([]gpmKernelStats, len(a.gpms)),
	}
	a.kernels = append(a.kernels, kern)
	return kern
}

// end returns the kernel's end time: the driver command's completion for a
// unified kernel, and the last GPM end for a single-GPM phase in which every
// launched GPM has ended.
func (k *gpmTraceKernel) end() (float64, bool) {
	if !k.Single {
		return k.EndNS, k.Ended
	}
	end, any := 0.0, false
	for _, s := range k.PerGPM {
		if !s.Launched {
			continue
		}
		if !s.EndedKernel {
			return 0, false
		}
		any = true
		if s.KernelEndNS > end {
			end = s.KernelEndNS
		}
	}
	return end, any
}

// driverTaskEnd handles the end of a driver task: either a GPM's
// LaunchKernelRsp (the launch req_out task) or the whole kernel command.
func (a *gpmTraceAgg) driverTaskEnd(taskID string, nowNS float64) {
	a.Lock()
	defer a.Unlock()

	if l, ok := a.launches[taskID]; ok {
		s := &a.kernels[l.kernel].PerGPM[l.gpm]
		s.EndedKernel = true
		s.KernelEndNS = nowNS
		delete(a.launches, taskID)
		return
	}
	if k, ok := a.kernelByCmd[taskID]; ok {
		a.kernels[k].Ended = true
		a.kernels[k].EndNS = nowNS
	}
}

func (a *gpmTraceAgg) current(gpm int) *gpmKernelStats {
	if gpm < 0 || gpm >= len(a.curKernel) {
		return nil
	}
	k := a.curKernel[gpm]
	if k < 0 {
		return nil
	}
	return &a.kernels[k].PerGPM[gpm]
}

func (a *gpmTraceAgg) wgStart(wgKey string, gpm int, nowNS float64) {
	a.Lock()
	defer a.Unlock()

	s := a.current(gpm)
	if s == nil {
		return
	}
	if s.Started == 0 || nowNS < s.FirstStartNS {
		s.FirstStartNS = nowNS
	}
	s.Started++
	a.wgs[wgKey] = gpmTraceWG{kernel: a.curKernel[gpm], gpm: gpm, startNS: nowNS}
	if a.running[gpm] == 0 {
		a.activeSince[gpm] = nowNS
		a.numActive++
		if a.numActive == len(a.gpms) {
			a.allSince = nowNS
		}
	}
	a.running[gpm]++
}

func (a *gpmTraceAgg) wgEnd(wgKey string, gpm int, nowNS float64) {
	a.Lock()
	defer a.Unlock()

	var s *gpmKernelStats
	wg, ok := a.wgs[wgKey]
	if ok {
		s = &a.kernels[wg.kernel].PerGPM[wg.gpm]
		delete(a.wgs, wgKey)
	} else {
		s = a.current(gpm)
	}
	if s == nil {
		return
	}
	s.Completed++
	if nowNS > s.LastEndNS {
		s.LastEndNS = nowNS
	}
	if ok {
		s.SumWGDurNS += nowNS - wg.startNS
		s.TimedWGs++
		a.running[wg.gpm]--
		if a.running[wg.gpm] == 0 {
			s.ActiveNS += nowNS - a.activeSince[wg.gpm]
			if a.numActive == len(a.gpms) {
				a.kernels[wg.kernel].AllActiveNS += nowNS - a.allSince
			}
			a.numActive--
		}
	}
}

func (a *gpmTraceAgg) l1vIssue(id string, gpm int, remote, write bool, nowNS float64) {
	a.Lock()
	defer a.Unlock()

	s := a.current(gpm)
	if s == nil {
		return
	}
	if !write {
		if remote {
			s.L1VReadRemoteIssued++
		} else {
			s.L1VReadLocalIssued++
		}
	}
	a.l1vReqs[id] = gpmTraceMemReq{
		kernel: a.curKernel[gpm], gpm: gpm,
		remote: remote, write: write, startNS: nowNS,
	}
}

func (a *gpmTraceAgg) l1vDone(id string, nowNS float64) {
	a.Lock()
	defer a.Unlock()

	r, ok := a.l1vReqs[id]
	if !ok {
		return
	}
	delete(a.l1vReqs, id)
	s := &a.kernels[r.kernel].PerGPM[r.gpm]
	lat := nowNS - r.startNS
	switch {
	case r.write && r.remote:
		s.L1VWriteRemote.add(lat)
	case r.write:
		s.L1VWriteLocal.add(lat)
	case r.remote:
		s.L1VReadRemote.add(lat)
	default:
		s.L1VReadLocal.add(lat)
	}
}

func (a *gpmTraceAgg) tlbMiss(id string, gpm int, remote bool, nowNS float64) {
	a.Lock()
	defer a.Unlock()

	s := a.current(gpm)
	if s == nil {
		return
	}
	if remote {
		s.TLBMissRemoteIssued++
	} else {
		s.TLBMissLocalIssued++
	}
	a.tlbReqs[id] = gpmTraceMemReq{
		kernel: a.curKernel[gpm], gpm: gpm, remote: remote, startNS: nowNS,
	}
}

func (a *gpmTraceAgg) tlbDone(id string, nowNS float64) {
	a.Lock()
	defer a.Unlock()

	r, ok := a.tlbReqs[id]
	if !ok {
		return
	}
	delete(a.tlbReqs, id)
	s := &a.kernels[r.kernel].PerGPM[r.gpm]
	if r.remote {
		s.TLBMissRemote.add(nowNS - r.startNS)
	} else {
		s.TLBMissLocal.add(nowNS - r.startNS)
	}
}

// gpmTraceCSVHeader lists the columns of the per-kernel per-GPM CSV. Times
// are in ns of simulated time; empty cells mean "did not happen".
var gpmTraceCSVHeader = []string{
	"kernel", "kernel_kind", "kernel_total_wg", "kernel_launch_ns", "kernel_end_ns",
	"gpm_index", "driver_id", "gpm_name", "tile", "x", "y",
	"hops_to_center", "mean_hops_to_others",
	"wg_assigned", "wg_started", "wg_completed",
	"gpm_launch_ns", "first_wg_start_ns", "last_wg_end_ns",
	"gpm_kernel_end_ns", "mean_wg_duration_ns",
	"l1v_read_local", "l1v_read_remote", "l1v_read_remote_frac",
	"l1v_read_local_mean_lat_ns", "l1v_read_remote_mean_lat_ns",
	"l1v_write_local", "l1v_write_remote",
	"l1v_write_remote_mean_lat_ns",
	"l2tlb_miss_local", "l2tlb_miss_remote",
	"l2tlb_miss_remote_lat_sum_ns", "l2tlb_miss_remote_mean_lat_ns",
	"gpm_active_ns", "kernel_all_gpm_active_ns",
}

func fmtNS(v float64, valid bool) string {
	if !valid || math.IsNaN(v) {
		return ""
	}
	return strconv.FormatFloat(v, 'f', 3, 64)
}

func fmtFloat(v float64) string {
	if math.IsNaN(v) {
		return ""
	}
	return strconv.FormatFloat(v, 'f', 4, 64)
}

// writeCSV writes one row per kernel x GPM. Read and TLB counts are of
// requests issued; latencies are over the requests that completed.
func (a *gpmTraceAgg) writeCSV(w io.Writer) error {
	a.Lock()
	defer a.Unlock()

	if _, err := fmt.Fprintln(w, strings.Join(gpmTraceCSVHeader, ",")); err != nil {
		return err
	}
	u := func(v uint64) string { return strconv.FormatUint(v, 10) }
	for _, k := range a.kernels {
		for i, g := range a.gpms {
			s := k.PerGPM[i]
			meanWG := math.NaN()
			if s.TimedWGs > 0 {
				meanWG = s.SumWGDurNS / float64(s.TimedWGs)
			}
			reads := s.L1VReadLocalIssued + s.L1VReadRemoteIssued
			remoteFrac := math.NaN()
			if reads > 0 {
				remoteFrac = float64(s.L1VReadRemoteIssued) / float64(reads)
			}
			kind := "unified"
			if k.Single {
				kind = "single_gpm_phase"
			}
			endNS, ended := k.end()
			row := []string{
				strconv.Itoa(k.ID), kind, strconv.Itoa(k.TotalWG),
				fmtNS(k.LaunchNS, true), fmtNS(endNS, ended),
				strconv.Itoa(g.Index), strconv.Itoa(g.DriverID), g.Name,
				strconv.Itoa(g.Tile), strconv.Itoa(g.X), strconv.Itoa(g.Y),
				strconv.Itoa(g.HopsToCenter), fmtFloat(g.MeanHops),
				strconv.Itoa(s.Assigned), u(s.Started), u(s.Completed),
				fmtNS(s.LaunchNS, s.Launched),
				fmtNS(s.FirstStartNS, s.Started > 0),
				fmtNS(s.LastEndNS, s.Completed > 0),
				fmtNS(s.KernelEndNS, s.EndedKernel),
				fmtNS(meanWG, true),
				u(s.L1VReadLocalIssued), u(s.L1VReadRemoteIssued),
				fmtFloat(remoteFrac),
				fmtNS(s.L1VReadLocal.mean(), true),
				fmtNS(s.L1VReadRemote.mean(), true),
				u(s.L1VWriteLocal.Count), u(s.L1VWriteRemote.Count),
				fmtNS(s.L1VWriteRemote.mean(), true),
				u(s.TLBMissLocalIssued), u(s.TLBMissRemoteIssued),
				fmtNS(s.TLBMissRemote.SumNS, true),
				fmtNS(s.TLBMissRemote.mean(), true),
				fmtNS(s.ActiveNS, true), fmtNS(k.AllActiveNS, true),
			}
			if _, err := fmt.Fprintln(w, strings.Join(row, ",")); err != nil {
				return err
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Hook adapters

type gpmTraceHook struct {
	agg    *gpmTraceAgg
	engine sim.TimeTeller
	start  func(h *gpmTraceHook, task tracing.Task)
	end    func(h *gpmTraceHook, task tracing.Task)
	gpm    int
}

func (h *gpmTraceHook) nowNS() float64 {
	return float64(h.engine.CurrentTime()) * 1e9
}

func (h *gpmTraceHook) StartTask(task tracing.Task) {
	if h.start != nil {
		h.start(h, task)
	}
}

func (h *gpmTraceHook) StepTask(task tracing.Task) {}

func (h *gpmTraceHook) EndTask(task tracing.Task) {
	if h.end != nil {
		h.end(h, task)
	}
}

// gpmTracer owns the aggregator and the hooks.
type gpmTracer struct {
	agg      *gpmTraceAgg
	portToID map[sim.Port]int
	path     string
	checked  bool
}

var gpmTileRe = regexp.MustCompile(`GPU\[(\d+)\]`)

func (r *Runner) addGPMTracer() {
	if !*gpmTraceFlag {
		return
	}

	names := make([]string, len(r.platform.GPUs))
	tiles := make([]int, len(r.platform.GPUs))
	numCUs := make([]int, len(r.platform.GPUs))
	t := &gpmTracer{portToID: map[sim.Port]int{}}
	for i, gpu := range r.platform.GPUs {
		names[i] = gpu.Domain.Name()
		m := gpmTileRe.FindStringSubmatch(names[i])
		if m == nil {
			panic(fmt.Sprintf("gpm trace: cannot parse tile from %q", names[i]))
		}
		tiles[i], _ = strconv.Atoi(m[1])
		numCUs[i] = len(gpu.CUs)
		if int(gpu.GPUID) != i+1 {
			panic(fmt.Sprintf("gpm trace: GPU %s has driver ID %d at index %d",
				names[i], gpu.GPUID, i))
		}
		t.portToID[gpu.Domain.GetPortByName("CommandProcessor")] = i
	}
	t.agg = newGPMTraceAgg(newGPMTraceGPMs(names, tiles, numCUs))
	t.path = *gpmTraceFileFlag
	if t.path == "" {
		t.path = *filenameFlag + "_gpm_trace.csv"
	}
	r.gpmTracer = t
	engine := r.platform.Engine

	tracing.CollectTrace(r.platform.Driver, &gpmTraceHook{
		agg: t.agg, engine: engine, gpm: -1,
		start: func(h *gpmTraceHook, task tracing.Task) {
			if task.Kind != "req_out" || task.What != "*protocol.LaunchKernelReq" {
				return
			}
			req, ok := task.Detail.(*protocol.LaunchKernelReq)
			if !ok {
				return
			}
			gpm, ok := t.portToID[req.Dst]
			if !ok {
				gpm = -1
			}
			total := totalWGs(req.Packet)
			unified := req.WGFilter != nil
			k := h.agg.kernelLaunch(task.ParentID, task.ID, gpm, total, unified, h.nowNS())
			if gpm >= 0 && k >= 0 && unified {
				t.checkFilter(req, k, gpm)
			}
		},
		end: func(h *gpmTraceHook, task tracing.Task) {
			h.agg.driverTaskEnd(task.ID, h.nowNS())
		},
	})

	for i, gpu := range r.platform.GPUs {
		gpm := i
		for _, cu := range gpu.CUs {
			tracing.CollectTrace(cu.(tracing.NamedHookable), &gpmTraceHook{
				agg: t.agg, engine: engine, gpm: gpm,
				start: func(h *gpmTraceHook, task tracing.Task) {
					if task.Kind != "req_in" {
						return
					}
					switch msg := task.Detail.(type) {
					case *protocol.MapWGReq:
						h.agg.wgStart(msg.ID, h.gpm, h.nowNS())
					case *protocol.WGCompletionMsg:
						key := ""
						if len(msg.RspTo) > 0 {
							key = msg.RspTo[0]
						}
						h.agg.wgEnd(key, h.gpm, h.nowNS())
					}
				},
			})
		}

		seen := map[string]bool{}
		for _, l1v := range gpu.L1VCaches {
			if !firstTraceForComponent(seen, l1v) {
				continue
			}
			tracing.CollectTrace(l1v, &gpmTraceHook{
				agg: t.agg, engine: engine, gpm: gpm,
				start: func(h *gpmTraceHook, task tracing.Task) {
					if task.Kind != "req_out" {
						return
					}
					msg, ok := task.Detail.(sim.Msg)
					if !ok {
						return
					}
					write := false
					switch msg.(type) {
					case *mem.ReadReq:
					case *mem.WriteReq:
						write = true
					default:
						return
					}
					remote := msg.Meta().Dst != nil &&
						strings.Contains(msg.Meta().Dst.Name(), ".RDMA.")
					h.agg.l1vIssue(task.ID, h.gpm, remote, write, h.nowNS())
				},
				end: func(h *gpmTraceHook, task tracing.Task) {
					h.agg.l1vDone(task.ID, h.nowNS())
				},
			})
		}

		if gpu.L2TLB != nil {
			tracing.CollectTrace(gpu.L2TLB, &gpmTraceHook{
				agg: t.agg, engine: engine, gpm: gpm,
				start: func(h *gpmTraceHook, task tracing.Task) {
					if task.Kind != "req_out" {
						return
					}
					msg, ok := task.Detail.(sim.Msg)
					if !ok || msg.Meta().Dst == nil {
						return
					}
					remote := strings.Contains(msg.Meta().Dst.Name(), "IOMMU")
					// The miss task (ID = parent) ends when the L2 TLB
					// answers the first request of the MSHR entry.
					h.agg.tlbMiss(task.ParentID, h.gpm, remote, h.nowNS())
				},
				end: func(h *gpmTraceHook, task tracing.Task) {
					h.agg.tlbDone(task.ID, h.nowNS())
				},
			})
		}
	}
}

func totalWGs(pkt *kernels.HsaKernelDispatchPacket) int {
	if pkt == nil || pkt.WorkgroupSizeX == 0 || pkt.WorkgroupSizeY == 0 ||
		pkt.WorkgroupSizeZ == 0 {
		return 0
	}
	nx := (pkt.GridSizeX-1)/uint32(pkt.WorkgroupSizeX) + 1
	ny := (pkt.GridSizeY-1)/uint32(pkt.WorkgroupSizeY) + 1
	nz := (pkt.GridSizeZ-1)/uint32(pkt.WorkgroupSizeZ) + 1
	return int(nx * ny * nz)
}

// checkFilter verifies, once per run, that the driver's WG filter of this
// launch agrees with assignWGs at the range boundaries.
func (t *gpmTracer) checkFilter(req *protocol.LaunchKernelReq, k, gpm int) {
	if t.checked || req.WGFilter == nil || req.Packet == nil {
		return
	}
	t.checked = true
	pkt := req.Packet
	nx := int((pkt.GridSizeX-1)/uint32(pkt.WorkgroupSizeX) + 1)
	ny := int((pkt.GridSizeY-1)/uint32(pkt.WorkgroupSizeY) + 1)
	t.agg.Lock()
	kern := t.agg.kernels[k]
	lo := 0
	for i := 0; i < gpm; i++ {
		lo += kern.PerGPM[i].Assigned
	}
	hi := lo + kern.PerGPM[gpm].Assigned
	total := kern.TotalWG
	t.agg.Unlock()

	in := func(flat int) bool {
		wg := &kernels.WorkGroup{
			IDX: flat % nx,
			IDY: (flat / nx) % ny,
			IDZ: flat / (nx * ny),
		}
		return req.WGFilter(pkt, wg)
	}
	ok := hi > lo && in(lo) && in(hi-1)
	if lo > 0 {
		ok = ok && !in(lo-1)
	}
	if hi < total {
		ok = ok && !in(hi)
	}
	if !ok {
		fmt.Fprintf(os.Stderr,
			"[GPMTrace] warning: WG filter of GPM %d does not match [%d,%d)\n",
			gpm, lo, hi)
	}
}

func (r *Runner) writeGPMTrace() {
	if r.gpmTracer == nil {
		return
	}
	f, err := os.Create(r.gpmTracer.path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[GPMTrace] cannot write %s: %v\n",
			r.gpmTracer.path, err)
		return
	}
	defer f.Close()
	if err := r.gpmTracer.agg.writeCSV(f); err != nil {
		fmt.Fprintf(os.Stderr, "[GPMTrace] write %s: %v\n", r.gpmTracer.path, err)
	}
}
