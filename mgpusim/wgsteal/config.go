// Package wgsteal implements T19: nearby work-group stealing between the
// GPMs of a unified (multi-GPM) kernel, optionally guided by cuckoo
// filters of the pages each GPM touched recently and of the footprints of
// WG groups recorded on earlier launches of the same kernel.
//
// Protocol (all of it is off unless the runner creates a Coordinator):
//   - The driver keeps MGPUSim's static contiguous split, but the WGs of a
//     GPM's range that have not been dispatched yet sit in a per-GPM queue
//     (Launch). The GPM's dispatcher takes WGs from the head; a thief takes
//     a chunk of ChunkWGs WGs from the tail. GPMs with an empty static
//     range also get the launch, so they can steal.
//   - A GPM whose queue is empty (<= LowWatermark) picks a victim from its
//     view of the other GPMs' remaining-WG counts and sends a StealReq to
//     the victim's command processor over the wafer mesh (a real message
//     through the switches, so it sees the same link and switch latency and
//     contention as data). The victim's CP serves it VictimCycles later,
//     cutting the chunk at that time, and answers with a StealRsp (or a
//     NACK carrying its remaining count) over the mesh.
//   - Views: every GPM advertises its remaining count to the GPMs within
//     RadiusHops every AdvPeriodNS and to farther GPMs every FarAdvPeriodNS.
//     The advertisements are not simulated as messages; a thief sees the
//     victim's count at the last advertisement boundary that is at least
//     AdvFixedNS + hops * AdvHopNS old (staleness), and the number of
//     messages the scheme would send is counted. Every GPM knows the static
//     split from the launch packet; a thief also remembers the count it got
//     in its last reply from each victim.
//   - Victim choice: the nearest eligible GPM within RadiusHops (more
//     remaining WGs breaks ties); if none, and Fallback is set, the nearest
//     eligible GPM farther away. With Filter, the near candidates are
//     ranked by the overlap of the candidate chunk's group footprint with
//     the thief's recent-page filter, then by group / index affinity, then
//     by distance.
package wgsteal

import "fmt"

// Config holds the T19 parameters.
type Config struct {
	ChunkWGs       int     // WGs per steal (k)
	RadiusHops     int     // advertisement radius / near-victim radius
	Fallback       bool    // steal beyond RadiusHops when nothing is near
	MinVictimWGs   int     // a victim must have at least this many queued WGs; 0 = 2 * ChunkWGs
	LowWatermark   int     // start stealing when the own queue has <= this many WGs
	AdvPeriodNS    float64 // near advertisement period
	FarAdvPeriodNS float64 // far advertisement period
	AdvHopNS       float64 // one-way per-hop latency of an advertisement
	AdvFixedNS     float64 // fixed latency of an advertisement
	VictimCycles   int     // victim CP cycles to serve a steal request
	ReqBytes       int     // steal request size on the mesh
	RspBytes       int     // steal reply size on the mesh
	InitialSplit   string  // "static" (MGPUSim's split) or "balanced" (floor/ceil N/GPMs)

	Filter              bool    // filter-guided victim / chunk choice
	RPFLog2Buckets      uint    // recent-page filter: log2 buckets
	FilterSlots         int     // slots per bucket (all filters)
	FPBits              uint    // fingerprint bits (all filters)
	RPFWindow           int     // distinct pages kept in the recent-page filter
	GroupLog2Buckets    uint    // group footprint filter: log2 buckets
	GroupWGs            int     // WGs per group when the benchmark gives no groups; 0 = ChunkWGs
	GroupTablePerGPM    int     // group footprint filters per home GPM (LRU)
	CandidatesPerVictim int     // chunks considered per victim (1 = tail only)
	ReplaceBelow        float64 // replace a group's history if fewer of its pages hit
	ScoreEps            float64 // overlap scores within this are a tie
	ProbeWidth          int     // filter entries probed per cycle at the thief
}

// DefaultConfig returns the defaults used by the runner flags.
func DefaultConfig() Config {
	return Config{
		ChunkWGs:            8,
		RadiusHops:          2,
		Fallback:            true,
		AdvPeriodNS:         200,
		FarAdvPeriodNS:      2000,
		AdvHopNS:            36,
		AdvFixedNS:          20,
		VictimCycles:        20,
		ReqBytes:            16,
		RspBytes:            32,
		InitialSplit:        "static",
		RPFLog2Buckets:      9,
		FilterSlots:         4,
		FPBits:              12,
		RPFWindow:           1024,
		GroupLog2Buckets:    6,
		GroupTablePerGPM:    64,
		CandidatesPerVictim: 1,
		ReplaceBelow:        0.5,
		ScoreEps:            0.05,
		ProbeWidth:          16,
	}
}

// Validate checks the parameters and fills derived defaults.
func (c *Config) Validate() error {
	if c.ChunkWGs < 1 {
		return fmt.Errorf("t19: chunk must be >= 1")
	}
	if c.MinVictimWGs == 0 {
		c.MinVictimWGs = 2 * c.ChunkWGs
	}
	if c.MinVictimWGs < c.ChunkWGs {
		return fmt.Errorf("t19: min victim WGs must be >= chunk")
	}
	if c.RadiusHops < 0 || c.LowWatermark < 0 {
		return fmt.Errorf("t19: radius and low watermark must be >= 0")
	}
	if c.AdvPeriodNS <= 0 || c.FarAdvPeriodNS <= 0 {
		return fmt.Errorf("t19: advertisement periods must be > 0")
	}
	if c.InitialSplit != "static" && c.InitialSplit != "balanced" {
		return fmt.Errorf("t19: initial split must be static or balanced")
	}
	if c.GroupWGs == 0 {
		c.GroupWGs = c.ChunkWGs
	}
	if c.CandidatesPerVictim < 1 {
		c.CandidatesPerVictim = 1
	}
	if c.ProbeWidth < 1 {
		c.ProbeWidth = 1
	}
	if c.Filter {
		if c.GroupLog2Buckets > c.RPFLog2Buckets {
			return fmt.Errorf("t19: group filter must not be larger than the recent-page filter")
		}
		if c.RPFWindow <= 0 || c.RPFWindow > (1<<c.RPFLog2Buckets)*c.FilterSlots {
			return fmt.Errorf("t19: recent-page window must be in (0, capacity]")
		}
		if c.GroupTablePerGPM < 1 {
			return fmt.Errorf("t19: group table must have >= 1 entry")
		}
	}
	return nil
}
