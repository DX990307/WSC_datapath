package runner

// ISCA'27 T18 ("new m2"): two-sided merge of remote requests. All flags are
// off by default; with every -t18-* flag at its default the simulator is
// unchanged. See mgpusim/timing/rdma/t18_*.go.

import (
	"flag"
	"fmt"

	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/timing/rdma"
)

var t18ReqMergeFlag = flag.Bool("t18-req-merge", false,
	"T18: merge remote full-line reads into open entries of the requester's "+
		"in-flight cuckoo table (requester side).")
var t18OwnerMergeFlag = flag.Bool("t18-owner-merge", false,
	"T18: merge remote reads of a line at the owner (one L2 read per line, "+
		"48-bit requester bitmap) (owner side).")
var t18OwnerMulticastFlag = flag.Bool("t18-owner-multicast", true,
	"T18: with -t18-owner-merge, send a merged response as one multicast "+
		"packet along the mesh routing tree (false: one unicast per requester).")
var t18WindowFlag = flag.Int("t18-window", 0,
	"T18: requester in-flight window W (network transactions per RDMA); "+
		"0 keeps -rdma-max-outstanding. Setting it enables the T18 requester engine.")
var t18DistCreditsFlag = flag.Bool("t18-dist-credits", false,
	"T18: split W into per-destination credits proportional to RTT "+
		"(84 + 71.4 ns/hop); idle credits may be borrowed.")
var t18CreditPolicyFlag = flag.String("t18-credit-policy", "",
	"T18 / ISCA'27 M1 allocation of the W requester slots: shared (one pool), "+
		"rtt (= -t18-dist-credits=true: per-destination credits proportional "+
		"to RTT, idle credits borrowable), near (-t18-near-reserve slots only "+
		"for destinations within -t18-near-hops hops, the rest split as rtt). "+
		"Empty: rtt if -t18-dist-credits, else shared. A policy other than "+
		"shared enables the T18 requester engine. Contradicting "+
		"-t18-dist-credits is an error.")
var t18NearReserveFlag = flag.Int("t18-near-reserve", 16,
	"T18 credit policy near: slots of W reserved for near destinations "+
		"(0 <= R < W; R = 0 is rtt). Setting it with another policy is an error.")
var t18NearHopsFlag = flag.Int("t18-near-hops", 3,
	"T18 credit policy near: a destination at most this many hops away may "+
		"use the near slots. Setting it with another policy is an error.")
var cupathV3StatsFlag = flag.Bool("report-cupath-v3-stats", false,
	"Statistics only (ISCA'27 P16): T18 requester occupancy and queueing by "+
		"distance group (near <= 3 hops, mid 4-6, far >= 7), near-zone use, "+
		"owner merge table occupancy and full time, derived merge / multicast "+
		"ratios. Does not change timing.")
var t18OwnerWindowFlag = flag.Int("t18-owner-window", 0,
	"T18: owner-direction in-flight limit; 0 = -t18-window if set, else "+
		"-rdma-max-outstanding.")
var t18ReqEntriesFlag = flag.Int("t18-req-entries", 0,
	"T18: requester in-flight table slots; 0 = 2 x window.")
var t18OwnerEntriesFlag = flag.Int("t18-owner-entries", 0,
	"T18: owner merge table slots; 0 = 2 x owner window.")
var t18WaysFlag = flag.Int("t18-ways", 4,
	"T18: slots per bucket of the cuckoo tables.")
var t18FingerprintBitsFlag = flag.Int("t18-fp-bits", 12,
	"T18: fingerprint (tag) bits compared in parallel; a hit always "+
		"checks the full line address.")
var t18MaxKicksFlag = flag.Int("t18-max-kicks", 32,
	"T18: maximum cuckoo displacements per insert before the table is full.")
var t18ReqWaitersFlag = flag.Int("t18-req-waiters", 0,
	"T18: requester waiter pool entries; 0 = 4 x table slots.")
var t18ComposeTableEntriesFlag = flag.Int("t18-compose-table-entries", 0,
	"M1 composed with M2 packing (-remote-data-path-enable with T18): "+
		"requester line table entries (the M1 in-flight table; the waiter "+
		"pool = 4 x entries unless -t18-req-waiters, open batches <= entries). "+
		"0 = old sizing max(2W, 2 x batches x batch-lines) = 1024 at W=64.")
var t18BypassWhenFullFlag = flag.Bool("t18-bypass-when-full", false,
	"T18: when the requester table is full, send the request unmerged if a "+
		"window slot is free (default: stall at the RDMA input).")

// ISCA'27 M1 (= T18) + M2 (= old m2 remote data path): which mechanism
// removes same-line duplicate reads at the requester. Empty keeps the
// per-mechanism flags (-t18-req-merge, -remote-data-path-dedup-enable), so
// existing T18 / old-m2 configurations are unchanged.
var cupathDedupOwnerFlag = flag.String("cupath-dedup-owner", "",
	"M1+M2 requester de-duplication owner: m1 (only M1's in-flight table "+
		"merge; M2 dedup off), m2 (only M2's filter dedup; M1 requester "+
		"merge off), both, none; empty keeps -t18-req-merge and "+
		"-remote-data-path-dedup-enable as given.")

// dedupOwner returns (M1 merge, M2 dedup) overrides; set is false when the
// switch is empty.
func dedupOwner() (m1, m2, set bool) {
	switch *cupathDedupOwnerFlag {
	case "":
		return false, false, false
	case "m1":
		return true, false, true
	case "m2":
		return false, true, true
	case "both":
		return true, true, true
	case "none":
		return false, false, true
	}
	panic("-cupath-dedup-owner must be m1, m2, both or none, got " +
		*cupathDedupOwnerFlag)
}

func remoteDataPathDedupFromFlags() bool {
	if _, m2, set := dedupOwner(); set {
		return m2
	}
	return *remoteDataPathDedupEnableFlag
}

// t18CreditPolicyFromFlags resolves -t18-credit-policy against the older
// -t18-dist-credits; contradictions and near-only flags given with another
// policy are errors.
func t18CreditPolicyFromFlags() rdma.T18CreditPolicy {
	set := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { set[f.Name] = true })
	policy, err := resolveT18CreditPolicy(*t18CreditPolicyFlag,
		*t18DistCreditsFlag, set["t18-dist-credits"],
		set["t18-near-reserve"] || set["t18-near-hops"])
	if err != nil {
		panic(err.Error())
	}
	if policy == rdma.T18CreditNear &&
		(*t18NearReserveFlag < 0 || *t18NearHopsFlag < 0) {
		panic("-t18-near-reserve and -t18-near-hops must be >= 0")
	}
	return policy
}

// resolveT18CreditPolicy: an empty policy follows -t18-dist-credits (true =
// rtt, false = shared). A policy given together with an explicit
// -t18-dist-credits must agree with it (true = rtt or near, false = shared).
// The near-only flags require the near policy.
func resolveT18CreditPolicy(
	name string, dist, distSet, nearFlagsSet bool,
) (rdma.T18CreditPolicy, error) {
	policy := rdma.T18CreditShared
	if dist {
		policy = rdma.T18CreditRTT
	}
	if name != "" {
		p, err := rdma.ParseT18CreditPolicy(name)
		if err != nil {
			return 0, fmt.Errorf("-t18-credit-policy: %v", err)
		}
		if distSet && dist != (p != rdma.T18CreditShared) {
			return 0, fmt.Errorf("-t18-credit-policy=%s contradicts "+
				"-t18-dist-credits=%v (dist-credits true = rtt or near, "+
				"false = shared)", p, dist)
		}
		policy = p
	}
	if policy != rdma.T18CreditNear && nearFlagsSet {
		return 0, fmt.Errorf("-t18-near-reserve / -t18-near-hops need "+
			"-t18-credit-policy=near (policy is %s)", policy)
	}
	return policy, nil
}

func t18ConfigFromFlags() rdma.T18Config {
	policy := t18CreditPolicyFromFlags()
	reqMerge := *t18ReqMergeFlag
	if m1, _, set := dedupOwner(); set {
		reqMerge = m1
	}
	c := rdma.T18Config{
		ReqMerge:        reqMerge,
		OwnerMerge:      *t18OwnerMergeFlag,
		OwnerMulticast:  *t18OwnerMulticastFlag,
		Window:          netScaled(*t18WindowFlag),
		DistCredits:     policy != rdma.T18CreditShared,
		CreditPolicy:    policy,
		ExtraStats:      *cupathV3StatsFlag,
		OwnerWindow:     netScaled(*t18OwnerWindowFlag),
		ReqEntries:      netScaled(*t18ReqEntriesFlag),
		OwnerEntries:    netScaled(*t18OwnerEntriesFlag),
		Ways:            *t18WaysFlag,
		FingerprintBits: *t18FingerprintBitsFlag,
		MaxKicks:        *t18MaxKicksFlag,
		ReqWaiters:      netScaled(*t18ReqWaitersFlag),
		BypassWhenFull:  *t18BypassWhenFullFlag,

		ComposeLineEntries: netScaled(*t18ComposeTableEntriesFlag),
	}
	if policy == rdma.T18CreditNear {
		c.NearReserve = *t18NearReserveFlag
		c.NearHops = *t18NearHopsFlag
		window := c.Window
		if window <= 0 {
			window = netScaled(*rdmaMaxOutstandingFlag)
		}
		if c.NearReserve >= window {
			panic(fmt.Sprintf("-t18-near-reserve=%d must be below the window W=%d",
				c.NearReserve, window))
		}
	}
	if !c.Enabled() {
		return rdma.T18Config{}
	}
	if *remoteDataPathEnableFlag {
		fmt.Printf("[Config] M1+M2: T18 composed with the remote data path "+
			"(dedup owner %q: M1 merge=%v, M2 dedup=%v)\n",
			*cupathDedupOwnerFlag, c.ReqMerge, remoteDataPathDedupFromFlags())
	}
	if *observationTracing {
		panic("-trace-observation does not follow T18 requests; disable T18")
	}
	fmt.Printf("[Config] T18 req-merge=%v owner-merge=%v multicast=%v "+
		"window=%d dist-credits=%v owner-window=%d req-entries=%d "+
		"owner-entries=%d ways=%d fp-bits=%d max-kicks=%d req-waiters=%d "+
		"bypass-when-full=%v\n",
		c.ReqMerge, c.OwnerMerge, c.OwnerMulticast, c.Window, c.DistCredits,
		c.OwnerWindow, c.ReqEntries, c.OwnerEntries, c.Ways,
		c.FingerprintBits, c.MaxKicks, c.ReqWaiters, c.BypassWhenFull)
	near := ""
	if policy == rdma.T18CreditNear {
		near = fmt.Sprintf(" near-reserve=%d near-hops=%d", c.NearReserve, c.NearHops)
	}
	fmt.Printf("[Config] T18 credit-policy=%s%s v3-stats=%v\n",
		policy, near, c.ExtraStats)
	if *remoteDataPathEnableFlag {
		fmt.Printf("[Config] M1+M2 compose requester line table: %s\n",
			composeTableDescription(c))
	}
	return c
}

// WithT18 configures T18 on every RDMA engine.
func (b R9NanoPlatformBuilder) WithT18(c rdma.T18Config) R9NanoPlatformBuilder {
	b.t18 = c
	return b
}

// WithT18 configures T18 on the GPM's RDMA engine.
func (b R9NanoGPUBuilder) WithT18(c rdma.T18Config) R9NanoGPUBuilder {
	b.t18 = c
	return b
}

// configT18Topology gives every RDMA engine the mesh position of all RDMA
// engines (for hop counts). GPUs are created row by row, skipping the
// center tile (createGPUs).
func (b *R9NanoPlatformBuilder) configT18Topology() {
	if !b.t18.Enabled() {
		return
	}
	var ports []sim.Port
	var coords [][2]int
	i := 0
	for y := 0; y < b.tileHeight; y++ {
		for x := 0; x < b.tileWidth; x++ {
			if x == b.tileWidth/2 && y == b.tileHeight/2 {
				continue
			}
			ports = append(ports, b.gpus[i].RDMAEngine.ToOutside)
			coords = append(coords, [2]int{x, y})
			i++
		}
	}
	if i != len(b.gpus) {
		panic("T18 topology: GPU count does not match the mesh")
	}
	for j, gpu := range b.gpus {
		gpu.RDMAEngine.SetT18Topology(coords[j], ports, coords)
	}
}

func (r *Runner) reportT18Stats() {
	if !r.Timing {
		return
	}
	for _, gpu := range r.platform.GPUs {
		if gpu.RDMAEngine == nil {
			continue
		}
		st := gpu.RDMAEngine.GetT18Stats()
		if !st.Enabled {
			continue
		}
		where := gpu.RDMAEngine.Name()
		put := func(name string, v float64) {
			r.metricsCollector.Collect(where, "t18_"+name, v)
		}
		put("req_engine", boolMetric(st.RequesterActive))
		if st.Composed {
			put("m1m2_composed", 1)
		}
		put("req_merge_enabled", boolMetric(st.ReqMergeEnabled))
		put("owner_merge_enabled", boolMetric(st.OwnerMergeEnabled))
		put("multicast_enabled", boolMetric(st.MulticastEnabled))
		put("dist_credits", boolMetric(st.DistCredits))
		put("owner_window", float64(st.OwnerWindow))
		if st.RequesterActive {
			put("window", float64(st.Window))
			put("req_table_slots", float64(st.ReqTableSlots))
			put("req_waiter_capacity", float64(st.ReqWaiterCapacity))
			put("req_accepted", float64(st.ReqAccepted))
			put("req_mergeable_reads", float64(st.ReqMergeableReads))
			put("req_merged", float64(st.ReqMerged))
			put("req_merged_inflight", float64(st.ReqMergedInflight))
			put("req_merged_queued", float64(st.ReqMergedQueued))
			if st.ReqMergeableReads > 0 {
				put("req_merge_rate",
					float64(st.ReqMerged)/float64(st.ReqMergeableReads))
			}
			put("req_new_entries", float64(st.ReqNewEntries))
			put("req_closed_by_write", float64(st.ReqClosedByWrite))
			put("req_table_full_stalls", float64(st.ReqTableFullStalls))
			put("req_bypasses", float64(st.ReqBypasses))
			put("req_waiter_full_stalls", float64(st.ReqWaiterFullStalls))
			put("req_stall_retries", float64(st.ReqStallRetries))
			put("req_sent", float64(st.ReqSent))
			put("req_sent_borrowed", float64(st.ReqSentBorrowed))
			put("req_responses", float64(st.ReqResponses))
			put("req_fanout_responses", float64(st.ReqFanoutResponses))
			put("req_peak_waiters", float64(st.ReqPeakWaiters))
			put("req_peak_queued", float64(st.ReqPeakQueued))
			put("req_peak_inflight", float64(st.ReqPeakInflight))
			put("req_table_peak", float64(st.ReqTable.Peak))
			put("req_table_lookups", float64(st.ReqTable.Lookups))
			put("req_table_fp_matches", float64(st.ReqTable.FingerprintMatches))
			put("req_table_fp_false_positives",
				float64(st.ReqTable.FingerprintFalsePos))
			put("req_table_insert_failures", float64(st.ReqTable.InsertFailures))
			put("req_table_kicks", float64(st.ReqTable.Kicks))
			put("req_time_ns", st.ReqTimeNS)
			if st.ReqTimeNS > 0 {
				put("req_queue_avg", st.ReqQueueAreaNS/st.ReqTimeNS)
				put("req_inflight_avg", st.ReqInflightAreaNS/st.ReqTimeNS)
				put("req_window_full_frac", st.ReqWindowFullNS/st.ReqTimeNS)
			}
			if st.ReqFanoutResponses > 0 {
				put("req_latency_avg_ns",
					st.ReqLatencyTotalNS/float64(st.ReqFanoutResponses))
			}
			for h := 1; h < len(st.HopDests); h++ {
				if st.HopDests[h] == 0 {
					continue
				}
				hop := fmt.Sprintf("hop%02d_", h)
				dests := float64(st.HopDests[h])
				put(hop+"dests", dests)
				put(hop+"credit_per_dest", float64(st.HopCredits[h])/dests)
				put(hop+"sent", float64(st.HopSent[h]))
				put(hop+"merged", float64(st.HopMerged[h]))
				if st.ReqTimeNS > 0 {
					put(hop+"inflight_per_dest_avg",
						st.HopInflightAreaNS[h]/st.ReqTimeNS/dests)
				}
				if st.HopSent[h] > 0 {
					put(hop+"rtt_avg_ns", st.HopRTTTotalNS[h]/float64(st.HopSent[h]))
				}
			}
		}
		if st.ExtraStats {
			reportT18V3Stats(st, put)
		}
		if st.OwnerMergeEnabled {
			put("owner_table_slots", float64(st.OwnerTableSlots))
			put("owner_reads", float64(st.OwnerReads))
			put("owner_merged", float64(st.OwnerMerged))
			if st.OwnerReads > 0 {
				put("owner_merge_rate",
					float64(st.OwnerMerged)/float64(st.OwnerReads))
			}
			put("owner_l2_reads", float64(st.OwnerL2Reads))
			put("owner_table_full_fallbacks", float64(st.OwnerTableFullFallbacks))
			put("owner_same_requester_unicast", float64(st.OwnerSameRequester))
			put("owner_closed_by_write", float64(st.OwnerClosedByWrite))
			put("owner_single_responses", float64(st.OwnerSingleResponses))
			put("owner_multicast_packets", float64(st.OwnerMulticastPackets))
			put("owner_multicast_dests", float64(st.OwnerMulticastDests))
			if st.OwnerMulticastPackets > 0 {
				put("owner_multicast_fanout_avg", float64(st.OwnerMulticastDests)/
					float64(st.OwnerMulticastPackets))
			}
			put("owner_unicast_fanout_packets", float64(st.OwnerUnicastFanout))
			if st.OwnerBatchChildReads+st.OwnerBatchChildMerged > 0 {
				put("owner_batch_child_reads", float64(st.OwnerBatchChildReads))
				put("owner_batch_child_merged", float64(st.OwnerBatchChildMerged))
			}
			put("owner_peak_live", float64(st.OwnerPeakLive))
			put("owner_table_peak", float64(st.OwnerTable.Peak))
			put("owner_table_fp_matches", float64(st.OwnerTable.FingerprintMatches))
			put("owner_table_fp_false_positives",
				float64(st.OwnerTable.FingerprintFalsePos))
			put("owner_table_insert_failures", float64(st.OwnerTable.InsertFailures))
			for n := 2; n < len(st.OwnerFanoutHistogram); n++ {
				if v := st.OwnerFanoutHistogram[n]; v > 0 {
					put(fmt.Sprintf("owner_fanout_%02d", n), float64(v))
				}
			}
		}
	}
}

// reportT18V3Stats writes the ISCA'27 P16 statistics (-report-cupath-v3-stats)
// as t18_v3_* metrics. Groups: near <= 3 hops, mid 4-6, far >= 7.
func reportT18V3Stats(st rdma.T18Stats, put func(string, float64)) {
	v3 := func(name string, v float64) { put("v3_"+name, v) }
	div := func(a, b float64) float64 {
		if b == 0 {
			return 0
		}
		return a / b
	}
	v3("credit_policy_"+st.CreditPolicy.String(), 1)
	if st.CreditPolicy == rdma.T18CreditNear {
		v3("near_reserve", float64(st.NearReserve))
		v3("near_hops", float64(st.NearHops))
	}
	if st.RequesterActive {
		groups := [3]string{"near", "mid", "far"}
		var dests [3]float64
		for h, n := range st.HopDests {
			if h > 0 {
				dests[t18GroupOfHop(h)] += float64(n)
			}
		}
		for g, name := range groups {
			v3(name+"_dests", dests[g])
			v3(name+"_inflight_avg", div(st.GroupInflightAreaNS[g], st.ReqTimeNS))
			v3(name+"_queued_avg", div(st.GroupQueuedAreaNS[g], st.ReqTimeNS))
			v3(name+"_queued_while_full_avg", div(st.GroupQueuedFullNS[g], st.ReqTimeNS))
			v3(name+"_wait_while_full_ns", st.GroupWaitFullTimeNS[g])
			v3(name+"_wait_while_full_frac", div(st.GroupWaitFullTimeNS[g], st.ReqTimeNS))
		}
		v3("window_full_ns", st.ReqWindowFullNS)
		if st.CreditPolicy == rdma.T18CreditNear {
			v3("near_zone_inflight_avg", div(st.NearZoneAreaNS, st.ReqTimeNS))
			v3("near_zone_full_frac", div(st.NearZoneFullNS, st.ReqTimeNS))
			v3("near_zone_sends", float64(st.NearZoneSends))
			v3("near_overflow_sends", float64(st.NearOverflowSends))
			v3("near_overflow_borrowed", float64(st.NearOverflowBorrowed))
		}
		v3("req_merged_into_sent", float64(st.ReqMergedInflight))
		v3("req_merged_into_queued", float64(st.ReqMergedQueued))
	}
	if st.OwnerMergeEnabled {
		v3("owner_full_line_reads", float64(st.OwnerReads))
		v3("owner_merged", float64(st.OwnerMerged))
		v3("owner_table_full_fallbacks", float64(st.OwnerTableFullFallbacks))
		v3("owner_table_avg", div(st.OwnerTableAreaNS, st.OwnerTimeNS))
		v3("owner_table_full_ns", st.OwnerTableFullNS)
		v3("owner_table_full_frac", div(st.OwnerTableFullNS, st.OwnerTimeNS))
		fallbacks := float64(st.OwnerTableFullFallbacks + st.OwnerSameRequester)
		v3("owner_unicast_fallback_frac", div(fallbacks, float64(st.OwnerReads)))
		// Merged entries (>= 2 requesters): responses that would have been
		// sent as unicasts versus packets actually sent.
		merged := 0.0
		for n := 2; n < len(st.OwnerFanoutHistogram); n++ {
			merged += float64(st.OwnerFanoutHistogram[n])
		}
		v3("owner_merged_entries", merged)
		v3("mc_packets", float64(st.OwnerMulticastPackets))
		v3("mc_dests_avg", div(float64(st.OwnerMulticastDests),
			float64(st.OwnerMulticastPackets)))
		v3("mc_responses_saved",
			float64(st.OwnerMulticastDests)-float64(st.OwnerMulticastPackets))
		v3("mc_unicast_fanout_packets", float64(st.OwnerUnicastFanout))
	}
}

// t18GroupOfHop mirrors rdma's distance groups: near <= 3, mid 4-6, far >= 7.
func t18GroupOfHop(h int) int {
	switch {
	case h <= 3:
		return 0
	case h <= 6:
		return 1
	}
	return 2
}

// composeTableDescription states the effective requester line table size of
// the M1+M2 composed path (it mirrors rdma.Comp.m1ComposeInit).
func composeTableDescription(c rdma.T18Config) string {
	window := c.Window
	if window <= 0 {
		window = netScaled(*rdmaMaxOutstandingFlag)
	}
	if c.ComposeLineEntries > 0 {
		waiters := 4 * c.ComposeLineEntries
		if c.ReqWaiters > 0 {
			waiters = c.ReqWaiters
		}
		return fmt.Sprintf("entries=%d waiters=%d (-t18-compose-table-entries)",
			c.ComposeLineEntries, waiters)
	}
	entries := c.ReqEntries
	if entries <= 0 {
		entries = 2 * window
	}
	m2 := *remoteDataPathBatchesFlag * *remoteDataPathBatchLinesFlag * 2
	if entries < m2 {
		entries = m2
	}
	return fmt.Sprintf("entries=%d (default max(2W, M2 lines))", entries)
}
