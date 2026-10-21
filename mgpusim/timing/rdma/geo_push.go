package rdma

// GeoPushConfig configures geo-push (all off in the zero value).
type GeoPushConfig struct {
	Radius        int  // 0 off, 1 or 2 hops around the requester
	SharedFilter  bool // push only lines seen read by >= 2 GPMs recently
	FilterEntries int  // owner sharing table entries (shared filter)
}

// Enabled reports whether geo-push is on.
func (c GeoPushConfig) Enabled() bool { return c.Radius > 0 }

// geoPushState is the per-RDMA geo-push state (nil when geo-push is off).
type geoPushState struct {
	cfg GeoPushConfig
}

// ConfigureGeoPush turns geo-push on for this RDMA engine.
func (c *Comp) ConfigureGeoPush(cfg GeoPushConfig) {
	c.geo = &geoPushState{cfg: cfg}
}
