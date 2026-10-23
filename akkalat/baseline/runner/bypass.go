package runner

import (
	"flag"
	"fmt"

	"github.com/sarchlab/akita/v3/noc/networking/switching"
)

var switchBypassLatencyFlag = flag.Int("switch-bypass-latency", 0,
	"Low-load switch bypass (isca27/hop-bypass): a flit arriving at an idle "+
		"switch-to-switch input port whose output is idle takes this many "+
		"pipeline stages instead of -switch-latency. 0 = off.")
var switchBypassMaxHopsFlag = flag.Int("switch-bypass-max-hops", 0,
	"With -switch-bypass-latency, a flit bypasses at most this many "+
		"consecutive switches before taking one normal pipeline. 0 = no limit.")

var switchBypassRelaxedFlag = flag.Bool("switch-bypass-relaxed", false,
	"With -switch-bypass-latency, allow bypass while older flits are still "+
		"in the normal switch pipeline (only stalled route/forward buffers and "+
		"earlier flits of the same message block it).")

func applySwitchBypassFlags() {
	switching.BypassRelaxed = *switchBypassRelaxedFlag
	switching.BypassLatency = *switchBypassLatencyFlag
	switching.BypassMaxHops = *switchBypassMaxHopsFlag
	if switching.BypassLatency > 0 {
		fmt.Printf("[Config] switch bypass latency=%d max-hops=%d relaxed=%v\n",
			switching.BypassLatency, switching.BypassMaxHops, switching.BypassRelaxed)
	}
}

func (r *Runner) reportSwitchBypass() {
	if switching.BypassLatency <= 0 {
		return
	}
	c := switching.GetBypassCounters()
	put := func(name string, v int64) {
		r.metricsCollector.Collect("System", "bypass_"+name, float64(v))
	}
	put("latency", int64(switching.BypassLatency))
	put("max_hops", int64(switching.BypassMaxHops))
	if switching.BypassRelaxed {
		put("relaxed", 1)
	}
	put("bypassed", c.Bypassed)
	put("busy_input", c.BusyInput)
	put("busy_output", c.BusyOutput)
	put("hop_cap", c.HopCap)
	put("pipeline_full", c.PipelineFull)
	total := c.Bypassed + c.BusyInput + c.BusyOutput + c.HopCap + c.PipelineFull
	rate := 0.0
	if total > 0 {
		rate = float64(c.Bypassed) / float64(total)
	}
	r.metricsCollector.Collect("System", "bypass_hit_rate", rate)
	fmt.Printf("[Bypass] flit-hops=%d bypassed=%d (%.4f) busy_in=%d busy_out=%d hop_cap=%d pipe_full=%d\n",
		total, c.Bypassed, rate, c.BusyInput, c.BusyOutput, c.HopCap, c.PipelineFull)
}
