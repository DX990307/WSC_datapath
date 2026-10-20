package dispatching

import (
	"math"

	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/kernels"
	"github.com/sarchlab/mgpusim/v3/protocol"
	"github.com/sarchlab/mgpusim/v3/timing/cp/internal/resource"
	"github.com/sarchlab/mgpusim/v3/wgsteal"
)

// stealAlgorithm (T19) dispatches WGs round-robin over the CUs, like
// roundRobinAlgorithm, but takes them by ID from the launch's per-GPM
// queue (which thieves shrink from the tail and steals extend) instead of
// walking the grid with the static WG filter.
type stealAlgorithm struct {
	cuPool resource.CUResourcePool
	coord  *wgsteal.Coordinator
	gpm    int

	info   kernels.KernelLaunchInfo
	launch *wgsteal.Launch
	now    sim.VTimeInSec

	currWG           *kernels.WorkGroup
	currFlat         int
	nextCU           int
	numDispatchedWGs int
}

func (a *stealAlgorithm) RegisterCU(cu resource.DispatchableCU) {
	// The CU pool is shared with the default algorithm, which registers.
}

func (a *stealAlgorithm) StartNewKernel(info kernels.KernelLaunchInfo) {
	a.info = info
	a.numDispatchedWGs = 0
	a.currWG = nil
}

// NumWG is the number of WGs this GPM has dispatched or still holds; it
// grows when a steal succeeds.
func (a *stealAlgorithm) NumWG() int {
	n := a.numDispatchedWGs + a.launch.Remaining(a.gpm)
	if a.currWG != nil {
		n++
	}
	return n
}

func (a *stealAlgorithm) HasNext() bool {
	return a.currWG != nil || a.launch.Remaining(a.gpm) > 0
}

func (a *stealAlgorithm) Next() (location dispatchLocation) {
	if a.currWG == nil {
		flat, ok := a.launch.Pop(a.gpm, a.now)
		if !ok {
			return dispatchLocation{}
		}
		a.currWG = kernels.BuildWG(a.info, flat)
		a.currFlat = flat
	}

	for i := 0; i < a.cuPool.NumCU(); i++ {
		cuID := (a.nextCU + i) % a.cuPool.NumCU()
		cu := a.cuPool.GetCU(cuID)

		locations, ok := cu.ReserveResourceForWG(a.currWG)
		if ok {
			a.nextCU = (cuID + 1) % a.cuPool.NumCU()

			dispatch := dispatchLocation{
				valid: true,
				cu:    cu.DispatchingPort(),
				cuID:  cuID,
				wg:    a.currWG,
			}
			dispatch.locations =
				make([]protocol.WfDispatchLocation, len(locations))
			for i, l := range locations {
				dispatch.locations[i] = protocol.WfDispatchLocation(l)
			}

			a.coord.RegisterWG(a.currWG, a.launch, a.currFlat)
			a.currWG = nil
			a.numDispatchedWGs++

			return dispatch
		}
	}

	return dispatchLocation{}
}

func (a *stealAlgorithm) FreeResources(location dispatchLocation) {
	a.cuPool.GetCU(location.cuID).FreeResourcesForWG(location.wg)
}

// stealState is the dispatcher's T19 state.
type stealState struct {
	alg          *stealAlgorithm
	baseAlg      algorithm
	port         sim.Port
	send         func(msg sim.Msg, delayCycles int)
	noVictimTill sim.VTimeInSec
	noVictim     bool
	recheck      sim.VTimeInSec
}

// EnableWGSteal lets the dispatcher run T19 launches. port is the CP's
// steal port on the mesh; send queues a message on it after a delay.
func (d *DispatcherImpl) EnableWGSteal(
	coord *wgsteal.Coordinator,
	gpm int,
	port sim.Port,
	send func(msg sim.Msg, delayCycles int),
) {
	ra, ok := d.alg.(*roundRobinAlgorithm)
	if !ok {
		panic("T19 WG stealing needs the round-robin dispatcher")
	}
	d.steal = &stealState{
		alg: &stealAlgorithm{
			cuPool: ra.cuPool,
			coord:  coord,
			gpm:    gpm,
		},
		baseAlg: d.alg,
		port:    port,
		send:    send,
		recheck: sim.VTimeInSec(coord.Config().AdvPeriodNS * 1e-9),
	}
}

func (d *DispatcherImpl) stealMode() bool {
	return d.steal != nil && d.alg == algorithm(d.steal.alg)
}

// startStealKernel switches the algorithm for a launch.
func (d *DispatcherImpl) selectAlg(req *protocol.LaunchKernelReq) {
	if d.steal == nil {
		if req.WGSteal != nil {
			panic("T19 launch on a dispatcher without WG stealing")
		}
		return
	}
	if req.WGSteal == nil {
		d.alg = d.steal.baseAlg
		return
	}
	d.steal.alg.launch = req.WGSteal
	d.steal.noVictim = false
	d.steal.noVictimTill = 0
	d.alg = d.steal.alg
}

// trySteal sends a steal request when the GPM's queue is (nearly) empty.
func (d *DispatcherImpl) trySteal(now sim.VTimeInSec) bool {
	s := d.steal
	l := s.alg.launch
	s.alg.now = now
	if !l.WantsToSteal(s.alg.gpm) {
		s.noVictim = false
		return false
	}
	if s.noVictim && now < s.noVictimTill {
		return false
	}
	plan, ok := l.PlanSteal(s.alg.gpm, now)
	if !ok {
		s.noVictim = true
		s.noVictimTill = now + s.recheck
		return false
	}
	s.noVictim = false
	cfg := s.alg.coord.Config()
	l.IssueSteal(s.alg.gpm, plan)
	req := &wgsteal.StealReq{
		Launch: l,
		Thief:  s.alg.gpm,
		Victim: plan.Victim,
		Plan:   plan,
		Issue:  now,
	}
	req.ID = sim.GetIDGenerator().Generate()
	req.Src = s.port
	req.Dst = s.alg.coord.Port(plan.Victim)
	req.TrafficBytes = cfg.ReqBytes
	delay := 0
	if plan.ProbeEntries > 0 {
		delay = int(math.Ceil(float64(plan.ProbeEntries) / float64(cfg.ProbeWidth)))
	}
	s.send(req, delay)
	return true
}

// stealDone reports whether the GPM is finished with the launch: nothing
// queued or requested, and no victim in sight.
func (d *DispatcherImpl) stealDone() bool {
	s := d.steal
	l := s.alg.launch
	return !l.Pending(s.alg.gpm) && l.Remaining(s.alg.gpm) == 0 && s.noVictim
}
