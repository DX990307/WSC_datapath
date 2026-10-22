package driver

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// checkNoPendingCommands warns (once per call site, on stderr) when a
// synchronous host call (MemCopyH2D/D2H/D2D, LaunchKernel) is made while
// other command queues still hold commands that were enqueued but not
// drained. The synchronous call starts the engine, so those commands (often
// kernels) run while the host thread goes on; before DrainCommandQueue waited
// for the engine to go idle, whatever the host did next was injected at a
// simulated time that depended on wall-clock thread scheduling. The
// benchmark should issue such copies before enqueueing the kernels (or drain
// first).
func (d *Driver) checkNoPendingCommands(ctx *Context, api string) {
	pending := 0

	ctx.queueMutex.Lock()
	for _, q := range ctx.queues {
		pending += q.NumCommand()
	}
	ctx.queueMutex.Unlock()

	if pending == 0 {
		return
	}

	site := callerOutsideDriver()

	d.hostSyncWarnMu.Lock()
	d.HostSyncWarnings++
	first := !d.hostSyncWarnSites[site]
	d.hostSyncWarnSites[site] = true
	d.hostSyncWarnMu.Unlock()

	if first {
		fmt.Fprintf(os.Stderr,
			"HOST-SYNC-WARNING: %s at %s while %d enqueued commands are not drained\n",
			api, site, pending)
	}
}

// callerOutsideDriver returns the first two call sites outside the driver
// package (the benchmark line and its caller, which helps when the copy is
// inside a helper).
func callerOutsideDriver() string {
	sites := []string{}
	for skip := 2; skip < 24 && len(sites) < 2; skip++ {
		_, file, line, ok := runtime.Caller(skip)
		if !ok {
			break
		}
		if strings.Contains(file, "/mgpusim/driver/") {
			continue
		}
		if i := strings.Index(file, "/mgpusim/"); i >= 0 {
			file = file[i+1:]
		} else if i := strings.Index(file, "/akkalat/"); i >= 0 {
			file = file[i+1:]
		}
		sites = append(sites, fmt.Sprintf("%s:%d", file, line))
	}

	if len(sites) == 0 {
		return "unknown"
	}

	return strings.Join(sites, " <- ")
}
