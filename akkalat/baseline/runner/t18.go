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
var t18BypassWhenFullFlag = flag.Bool("t18-bypass-when-full", false,
	"T18: when the requester table is full, send the request unmerged if a "+
		"window slot is free (default: stall at the RDMA input).")

func t18ConfigFromFlags() rdma.T18Config {
	c := rdma.T18Config{
		ReqMerge:        *t18ReqMergeFlag,
		OwnerMerge:      *t18OwnerMergeFlag,
		OwnerMulticast:  *t18OwnerMulticastFlag,
		Window:          *t18WindowFlag,
		DistCredits:     *t18DistCreditsFlag,
		OwnerWindow:     *t18OwnerWindowFlag,
		ReqEntries:      *t18ReqEntriesFlag,
		OwnerEntries:    *t18OwnerEntriesFlag,
		Ways:            *t18WaysFlag,
		FingerprintBits: *t18FingerprintBitsFlag,
		MaxKicks:        *t18MaxKicksFlag,
		ReqWaiters:      *t18ReqWaitersFlag,
		BypassWhenFull:  *t18BypassWhenFullFlag,
	}
	if !c.Enabled() {
		return rdma.T18Config{}
	}
	if *remoteDataPathEnableFlag {
		panic("T18 (-t18-*) and -remote-data-path-enable are mutually exclusive")
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
