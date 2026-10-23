package pagemigration

import "fmt"

// Policy selects what triggers a migration.
type Policy int

// Policies.
const (
	PolicyNone Policy = iota
	// PolicyObserve installs the hooks and the statistics but never migrates.
	// It must give the same timing as no hooks at all.
	PolicyObserve
	PolicyOnFault
	PolicyAccessCounter
)

// ParsePolicy converts a flag value.
func ParsePolicy(s string) (Policy, error) {
	switch s {
	case "", "none", "off":
		return PolicyNone, nil
	case "observe":
		return PolicyObserve, nil
	case "fault", "on-fault", "onfault":
		return PolicyOnFault, nil
	case "counter", "access-counter", "ac":
		return PolicyAccessCounter, nil
	}
	return PolicyNone, fmt.Errorf("unknown page-migration policy %q", s)
}

func (p Policy) String() string {
	switch p {
	case PolicyObserve:
		return "observe"
	case PolicyOnFault:
		return "fault"
	case PolicyAccessCounter:
		return "counter"
	}
	return "none"
}

// Config holds every parameter of the migration model. Latencies are in
// nanoseconds (the GPM clock is 1 GHz).
type Config struct {
	Policy Policy

	// Log2Unit is the migration (and access-counting) granularity: 12 for
	// 4 KB, 21 for 2 MB. It must not be smaller than the page size.
	Log2Unit uint64

	// SoftwareLatencyNS is the fault / notification service time of one
	// batch (UVM-style driver work), BatchSize the most units per batch.
	SoftwareLatencyNS uint64
	BatchSize         int

	// ShootdownLatencyNS is the time from the start of the TLB shootdown
	// until all GPMs acknowledged it.
	ShootdownLatencyNS uint64

	// Threshold is the access-counter threshold (remote accesses of one GPM
	// to one unit).
	Threshold uint32

	// CooldownNS: a unit does not migrate again within this time after its
	// last migration. MaxMigrations: a unit that migrated this many times is
	// pinned (0 = unlimited).
	CooldownNS    uint64
	MaxMigrations int

	// Copy engine: chunk size of one pull over the network, pulls in flight
	// per destination engine, and L2 accesses in flight per engine.
	ChunkBytes uint64
	PullWindow int
	L2Window   int
	LineBytes  uint64

	// FrameRegionOffset is where, inside each GPM's 8-GB physical range, the
	// frames for migrated pages are taken from (above the driver's
	// allocations).
	FrameRegionOffset uint64
	FrameRegionSize   uint64

	// AllowPages (ISCA'27 P19 Q6): when non-nil, only units whose first
	// remote access hit one of these 4 KiB physical page numbers (the
	// driver's original placement) are counted and migrated, e.g. the pages
	// pgcls classifies as "private, misplaced". nil = every page.
	AllowPages map[uint64]bool

	// P20 target selection (target.go). Target = argmax keeps the original
	// behaviour; the other fields are used only by median / class.
	Target           Target
	DomShare         float64 // class: one GPM >= this share -> migrate to it
	MedianMaxReaders int     // class: more readers than this -> broadcast rule
	BroadcastK       int     // class broadcast: spread over k tiles (0 = stay)
	MinSaving        float64 // median/class: decline if weighted hops drop less
	ReaderNoise      float64 // a reader counts if it has >= this share
	LoadWindowNS     uint64  // tie-break load: halved every this many ns
	CountLocal       bool    // count the owner's own L1V misses (no trigger)
	CountNetwork     bool    // take back accesses served by an M3 replica

	// P21 adaptive placement (adapt.go); off by default.
	Adapt AdaptConfig

	// Check enables the expensive invariant checks (every L2 of every GPM is
	// searched for lines of each new frame at allocation and at remap).
	Check bool
}

// DefaultConfig returns the defaults (see README of results_index/pm for
// the sources).
func DefaultConfig() Config {
	return Config{
		Policy:             PolicyNone,
		Log2Unit:           12,
		SoftwareLatencyNS:  5000,
		BatchSize:          256,
		ShootdownLatencyNS: 1000,
		Threshold:          256,
		CooldownNS:         5000,
		MaxMigrations:      3,
		ChunkBytes:         4096,
		PullWindow:         16,
		L2Window:           128,
		LineBytes:          64,
		FrameRegionOffset:  6 << 30,
		FrameRegionSize:    2 << 30,
		Target:             TargetArgmax,
		DomShare:           0.6,
		MedianMaxReaders:   31,
		BroadcastK:         4,
		MinSaving:          0.1,
		ReaderNoise:        0.01,
		LoadWindowNS:       10000,
	}
}

// Enabled reports whether any hook is installed.
func (c Config) Enabled() bool { return c.Policy != PolicyNone }

// Migrates reports whether the policy moves pages.
func (c Config) Migrates() bool {
	return c.Policy == PolicyOnFault || c.Policy == PolicyAccessCounter
}

// Validate checks the parameters against the page size.
func (c Config) Validate(log2PageSize uint64) error {
	if !c.Enabled() {
		return nil
	}
	if c.Log2Unit < log2PageSize {
		return fmt.Errorf("migration unit 2^%d is smaller than the page 2^%d",
			c.Log2Unit, log2PageSize)
	}
	pageSize := uint64(1) << log2PageSize
	if c.ChunkBytes == 0 || pageSize%c.ChunkBytes != 0 ||
		c.ChunkBytes%c.LineBytes != 0 {
		return fmt.Errorf("chunk %d must divide the page %d and be a multiple "+
			"of the line %d", c.ChunkBytes, pageSize, c.LineBytes)
	}
	if c.BatchSize <= 0 || c.PullWindow <= 0 || c.L2Window <= 0 {
		return fmt.Errorf("batch size and windows must be positive")
	}
	if c.Adapt.Enabled && (c.Policy != PolicyAccessCounter || c.Log2Unit != 12) {
		return fmt.Errorf("adaptive placement needs the counter policy and 4 KB units")
	}
	if c.Policy == PolicyAccessCounter && c.Threshold == 0 {
		return fmt.Errorf("access-counter threshold must be positive")
	}
	return nil
}
