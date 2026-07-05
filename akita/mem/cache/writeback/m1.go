package writeback

// M1Config is intentionally narrow. M1 no longer configures L2 cache batching,
// DRAM batching, filters, early restart, or prefetching; the remaining M1
// mechanism is L1V direct local DRAM bypass plus background L2 clean fill.
type M1Config struct{}

// M1Stats reports only the L2-side background fill activity used by direct
// local DRAM bypass.
type M1Stats struct {
	DirectCleanFillReceived  uint64
	DirectCleanFillInstalled uint64
	DirectCleanFillDropped   uint64
}

func normalizeM1Config(c M1Config) M1Config {
	return c
}

// ConfigureM1 keeps the builder API stable while leaving L2-side M1 helpers
// disabled.
func (c *Cache) ConfigureM1(config M1Config) {
	c.m1Config = normalizeM1Config(config)
}

// GetM1Stats returns a copy of the M1 counters.
func (c *Cache) GetM1Stats() M1Stats {
	return c.m1Stats
}
