package runner

import (
	"encoding/json"
	"flag"
	"os"
	"sync"
	"time"

	"github.com/sarchlab/akita/v3/tracing"
	"github.com/tebeka/atexit"
)

var heartbeatFileFlag = flag.String("heartbeat-file", "",
	"Periodically write simulation progress (simulated time, completed "+
		"workgroups) as JSON to this file. Defaults to $EXP_HEARTBEAT; "+
		"empty disables it.")
var heartbeatIntervalFlag = flag.Duration("heartbeat-interval", 10*time.Second,
	"Wall-clock interval between heartbeat writes.")

// heartbeat is what the progress file contains. A monitor decides the
// simulation is hung when, across heartbeats, the simulated time or the
// completed-workgroup count stops advancing.
type heartbeat struct {
	PID          int      `json:"pid"`
	Args         []string `json:"args"`
	StartWall    float64  `json:"start_wall"`
	Wall         float64  `json:"wall"`
	Seq          uint64   `json:"seq"`
	SimTimeNS    float64  `json:"sim_time_ns"`
	WGsCompleted uint64   `json:"wgs_completed"`
	Finished     bool     `json:"finished"`
}

// wgCompletionCounter counts workgroups completed on every CU. It may be
// called from several engine goroutines and is read by the heartbeat writer.
type wgCompletionCounter struct {
	mu       sync.Mutex
	inflight map[string]struct{}
	count    uint64
}

func (c *wgCompletionCounter) StartTask(task tracing.Task) {
	if task.Kind != "req_in" || task.What != "*protocol.WGCompletionMsg" {
		return
	}
	c.mu.Lock()
	c.inflight[task.ID] = struct{}{}
	c.mu.Unlock()
}

func (c *wgCompletionCounter) StepTask(task tracing.Task) {}

func (c *wgCompletionCounter) EndTask(task tracing.Task) {
	c.mu.Lock()
	if _, found := c.inflight[task.ID]; found {
		delete(c.inflight, task.ID)
		c.count++
	}
	c.mu.Unlock()
}

func (c *wgCompletionCounter) value() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

func (r *Runner) startHeartbeat() {
	path := *heartbeatFileFlag
	if path == "" {
		path = os.Getenv("EXP_HEARTBEAT")
	}
	if path == "" {
		return
	}

	counter := &wgCompletionCounter{inflight: map[string]struct{}{}}
	for _, gpu := range r.platform.GPUs {
		for _, cu := range gpu.CUs {
			tracing.CollectTrace(cu.(tracing.NamedHookable), counter)
		}
	}

	hb := heartbeat{
		PID:       os.Getpid(),
		Args:      os.Args[1:],
		StartWall: wallNow(),
	}
	var mu sync.Mutex
	write := func(finished bool) {
		mu.Lock()
		defer mu.Unlock()
		hb.Wall = wallNow()
		hb.Seq++
		hb.SimTimeNS = float64(r.platform.Engine.CurrentTime()) * 1e9
		hb.WGsCompleted = counter.value()
		hb.Finished = finished
		writeJSONAtomically(path, hb)
	}

	write(false)
	go func() {
		for range time.Tick(*heartbeatIntervalFlag) {
			write(false)
		}
	}()
	atexit.Register(func() { write(true) })
}

func wallNow() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

func writeJSONAtomically(path string, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, data, 0o644) == nil {
		_ = os.Rename(tmp, path)
	}
}
