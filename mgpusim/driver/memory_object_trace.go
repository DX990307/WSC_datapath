package driver

import memtrace "github.com/sarchlab/akita/v3/mem/trace"

// RegisterMemoryObject labels a virtual allocation for an enabled diagnostic
// provenance trace. It is a no-op in normal and formal runs.
func (d *Driver) RegisterMemoryObject(
	ctx *Context,
	ptr Ptr,
	size uint64,
	label string,
) {
	if ctx == nil || !memtrace.RemoteOriginTraceEnabled() {
		return
	}
	memtrace.RegisterRemoteOriginObject(
		uint64(ctx.pid), uint64(ptr), size, label)
}
