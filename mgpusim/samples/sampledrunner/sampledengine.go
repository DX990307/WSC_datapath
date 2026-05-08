package sampledrunner

import (
	"container/heap"
	"github.com/sarchlab/mgpusim/v3/timing/wavefront"
	//	"github.com/sarchlab/mgpusim/v3/kernels"
	"github.com/sarchlab/akita/v3/sim"
	"log"
	"reflect"
)

type Wavefront struct {
	*wavefront.Wavefront
	//    bbls []profiler.BBL
	//    bbltimes []sim.VTimeInSec
	starttime sim.VTimeInSec
	endtime   sim.VTimeInSec
	submitted bool
	cu        sim.Handler
}

type wfHeap []*Wavefront

// Len returns the length of the event queue
func (h wfHeap) Len() int {
	return len(h)
}

func (h wfHeap) Less(i, j int) bool {
	return h[i].endtime < h[j].endtime
}

// Swap changes the position of two events in the event queue
func (h wfHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

// Push adds an event into the event queue
func (h *wfHeap) Push(x interface{}) {
	wf := x.(*Wavefront)
	*h = append(*h, wf)
}

// Pop removes and returns the next event to happen
func (h *wfHeap) Pop() interface{} {
	old := *h
	n := len(old)
	wf := old[n-1]
	*h = old[0 : n-1]
	return wf
}

type SampledTimeEngine struct {
	*sim.TickingComponent
	debugLabel          string
	wfs                 wfHeap
	targetcompltedwfs   uint64
	submitedwfs         uint64
	finishedbytimemodel uint64
	activate_wfs        uint64
	finetuned           bool
}

var Sampledtimeengine *SampledTimeEngine

func (cu *SampledTimeEngine) SetDebugLabel(label string) {
	if cu == nil {
		return
	}
	cu.debugLabel = label
}

func NewSampledTimeEngine(
	name string,
	engine sim.Engine,
	freq sim.Freq,
) *SampledTimeEngine {
	sampledTimeEngine := &SampledTimeEngine{}
	sampledTimeEngine.TickingComponent = sim.NewTickingComponent(
		name, engine, freq, sampledTimeEngine)

	sampledTimeEngine.wfs = make([]*Wavefront, 0)
	sampledTimeEngine.submitedwfs = 0
	heap.Init(&sampledTimeEngine.wfs)
	PhotonDebugf(name, "sampled time engine created")
	return sampledTimeEngine
}

func (cu *SampledTimeEngine) SetTargetCompletedWfs(target uint64) {
	cu.targetcompltedwfs = target
	cu.submitedwfs = 0
	cu.finishedbytimemodel = 0
	cu.activate_wfs = 0
	cu.finetuned = false
	cu.wfs = make([]*Wavefront, 0)
	heap.Init(&cu.wfs)
	PhotonDebugf(cu.debugLabel, "sampled time engine reset target=%d", target)
}

func (cu *SampledTimeEngine) Tick(now sim.VTimeInSec) bool {
	return false
}
func InitSampledTimeEngine(engine sim.Engine, freq sim.Freq) {
	Sampledtimeengine = NewSampledTimeEngine("SampledTimeEngine", engine, freq)
}

// /two types of events
// /the time of the sampled wf completion comes; call corresponding cu that the wf completed
// /the time of the
type SampledWfCompletedEvent struct {
	*sim.EventBase
}

func (c *SampledTimeEngine) Handle(e sim.Event) error {
	switch e := e.(type) {
	case *SampledWfCompletedEvent:
		c.handleSampledWfCompletedEvent(e)
	default:
		log.Panicf("cannot handle event of %s", reflect.TypeOf(e))
	}
	return nil
}

func (engine *SampledTimeEngine) handleSampledWfCompletedEvent(e sim.Event) {
	now := e.Time()
	complete_time := engine.Freq.NextTick(now)
	for engine.wfs.Len() > 0 {
		new_wf := engine.wfs[0]
		if new_wf.endtime > now {
			break
		}

		evt := wavefront.NewWfCompletionEvent(complete_time, new_wf.cu, new_wf.Wavefront)

		engine.Engine.Schedule(evt)
		PhotonDebugf(engine.debugLabel,
			"sampled wf completion fired wfid=%s sampledLevel=%d scheduledComplete=%.3fns now=%.3fns",
			new_wf.UID,
			new_wf.Sampled_level,
			complete_time*1e9,
			now*1e9)
		heap.Pop(&engine.wfs)
	}
	if engine.wfs.Len() > 0 {
		new_wf := engine.wfs[0]
		if !new_wf.submitted {
			engine.NewSampledWfCompleteEvent(new_wf)
		}
	}
}
func (engine *SampledTimeEngine) NewSampledWfCompleteEvent(new_wf *Wavefront) {
	evt := new(SampledWfCompletedEvent)
	new_wf.submitted = true //marked as submitted
	time := new_wf.endtime
	//new event
	//log.Printf("new event %.2f\n",time*1e9)
	evt.EventBase = sim.NewSampledEventBase(time, engine)
	engine.Engine.Schedule(evt)
	PhotonDebugf(engine.debugLabel,
		"sampled completion timer scheduled wfid=%s end=%.3fns queue=%d",
		new_wf.UID,
		time*1e9,
		engine.wfs.Len())
}

func (engine *SampledTimeEngine) IncreaseIdx(now sim.VTimeInSec) {
	engine.finishedbytimemodel++
	if !engine.finetuned &&
		engine.targetcompltedwfs > 0 &&
		engine.observedWfs() >= engine.targetcompltedwfs &&
		engine.wfs.Len() > 0 {
		engine.finetuneTime(now)
	}
	// log.Printf("start %d\n",engine.finishedbytimemodel )
}

func (engine *SampledTimeEngine) observedWfs() uint64 {
	return engine.submitedwfs + engine.finishedbytimemodel
}

func (engine *SampledTimeEngine) UpdateMaxWFS(wfnum uint64) {
	if engine.activate_wfs < wfnum {
		engine.activate_wfs = wfnum
	}
}
func (engine *SampledTimeEngine) finetuneTime(now sim.VTimeInSec) {
	if engine.finetuned {
		return
	}

	wfnums := engine.wfs.Len()
	if wfnums == 0 {
		return
	}
	engine.finetuned = true

	wfs := make([]*Wavefront, wfnums)
	i := 0
	for engine.wfs.Len() != 0 {
		wfs[i] = engine.wfs[0]
		heap.Pop(&engine.wfs)
		i++
	}
	activate_wfs := engine.activate_wfs
	//    endtime := wfs[0].endtime
	//    for _,wf := range wfs{
	//        if wf.starttime < endtime {
	//            activate_wfs++
	//        }
	//    }
	// log.Printf("%d %d\n", activate_wfs, wfnums)
	// log.Printf("submitted wf complete %d ", engine.submitedwfs)
	if activate_wfs == 0 {
		activate_wfs = uint64(wfnums)
		PhotonDebugf(engine.debugLabel,
			"sampled finetune active fallback active=%d queued=%d",
			activate_wfs,
			wfnums)
	}
	nexttick := engine.Freq.NextTick(now)
	if activate_wfs <= 1 {
		for _, wf := range wfs {
			if wf.endtime < nexttick {
				wf.endtime = nexttick
			}
			evt := wavefront.NewWfCompletionEvent(wf.endtime, wf.cu, wf.Wavefront)
			engine.Engine.Schedule(evt)
		}
		return
	}
	idx := 1
	endidx := len(wfs)
	left_times := make([]sim.VTimeInSec, wfnums-1)
	for idx < endidx {
		left_time := wfs[idx].endtime - wfs[idx-1].endtime
		current_activate_wfs := wfnums - idx
		left_times[idx-1] = left_time * sim.VTimeInSec(current_activate_wfs) / sim.VTimeInSec(activate_wfs)

		//current_activate_wfs--
		idx++
	}
	idx = 1
	for idx < endidx {
		wfs[idx].endtime = wfs[idx-1].endtime + left_times[idx-1]
		idx++

	}
	for _, wf := range wfs {
		if wf.endtime < nexttick {
			wf.endtime = nexttick
		}
		evt := wavefront.NewWfCompletionEvent(wf.endtime, wf.cu, wf.Wavefront)
		engine.Engine.Schedule(evt)
	}
}

func (engine *SampledTimeEngine) NewRawSampledWfCompletionEvent(now sim.VTimeInSec, time sim.VTimeInSec, handler sim.Handler, wf *wavefront.Wavefront) {
	nexttick := engine.Freq.NextTick(now)
	if time <= now {
		time = nexttick
	}

	new_wf := &Wavefront{
		Wavefront: wf,
		submitted: false,
		endtime:   time,
		starttime: now,
		cu:        handler,
	}
	heap.Push(&engine.wfs, new_wf)
	engine.submitedwfs++
	PhotonDebugf(engine.debugLabel,
		"sampled wf queued wfid=%s start=%.3fns end=%.3fns sampledQueued=%d realFinished=%d target=%d queue=%d",
		wf.UID,
		now*1e9,
		time*1e9,
		engine.submitedwfs,
		engine.finishedbytimemodel,
		engine.targetcompltedwfs,
		engine.wfs.Len())
	//log.Printf("start %.2f %.2f %d target %d\n",now*1e9, time*1e9, engine.submitedwfs,engine.targetcompltedwfs )
	if !engine.finetuned &&
		engine.targetcompltedwfs > 0 &&
		engine.observedWfs() >= engine.targetcompltedwfs {
		engine.finetuneTime(now)
	} else {
		if new_wf == engine.wfs[0] {
			engine.NewSampledWfCompleteEvent(new_wf)
		}
	}
}
