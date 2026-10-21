#!/usr/bin/env python3
"""Per-run aggregates (v3c384 ext.py + window/table checks for v3m2w). usage: ext.py RESULT_DIR... -> JSON lines"""
import csv, json, os, re, sys, collections
def f(x):
    try: return float(x)
    except Exception: return None
for R in sys.argv[1:]:
    e = os.path.basename(R.rstrip("/"))
    o = dict(exp=e)
    try: o["exit"] = json.load(open(R + "/meta.json"))["exit_code"]
    except Exception: o["exit"] = None
    S = collections.defaultdict(float); N = collections.Counter()
    try:
        for r in csv.reader(open(R + "/stats/metrics.csv")):
            if len(r) < 4: continue
            k = r[2].strip(); v = f(r[3])
            if v is None: continue
            if r[1].strip() == "Driver" and k in ("total_time", "kernel_time"): o[k] = v; continue
            S[k] += v; N[k] += 1
            wh = r[1].strip()
            if ".L2[" in wh and k in ("read-hit", "read-miss", "read-mshr-hit"): S["L2:" + k] += v
            if ".DRAM[" in wh and k == "read_trans_count": S["DRAM:reads"] += v
    except Exception as ex:
        o["err"] = str(ex); print(json.dumps(o)); continue
    g = lambda k: S.get(k, 0.0)
    avg = lambda k: S[k] / N[k] if N[k] else None
    div = lambda a, b: a / b if b else None
    o["inst"] = g("cu_inst_count"); o["wgs"] = g("cu_wg_count")
    lr = g("remote_logical_reads") or g("rdma_observed_remote_reads")
    o["remote_reads"] = lr
    o["remote_read_lat_ns"] = div(g("remote_logical_read_latency_total_ns"), g("remote_logical_read_latency_samples"))
    o["rdma_observed_remote_reads"] = g("rdma_observed_remote_reads")
    o["rdma_req_outstanding_full_stalls"] = g("rdma_requester_outstanding_full_stalls")
    # requester merge
    o["req_merged"] = g("t18_req_merged"); o["req_merge_frac"] = div(g("t18_req_merged"), lr)
    o["req_merged_into_sent"] = g("t18_v3_req_merged_into_sent"); o["req_merged_into_queued"] = g("t18_v3_req_merged_into_queued")
    o["req_new_entries"] = g("t18_req_new_entries"); o["req_bypasses"] = g("t18_req_bypasses")
    o["req_table_full_stalls"] = g("t18_req_table_full_stalls"); o["line_entry_full_stall_cycles"] = g("remote_line_entry_full_stalls")
    o["req_table_peak_max"] = None
    o["req_window_full_frac"] = avg("t18_req_window_full_frac"); o["req_inflight_avg"] = avg("t18_req_inflight_avg"); o["req_queue_avg"] = avg("t18_req_queue_avg")
    o["req_sent"] = g("t18_req_sent"); o["req_sent_borrowed"] = g("t18_req_sent_borrowed")
    for gname in ("near", "mid", "far"):
        o[f"{gname}_inflight_avg"] = avg(f"t18_v3_{gname}_inflight_avg")
        o[f"{gname}_queued_avg"] = avg(f"t18_v3_{gname}_queued_avg")
        o[f"{gname}_wait_full_frac"] = avg(f"t18_v3_{gname}_wait_while_full_frac")
    for k in ("near_zone_inflight_avg", "near_zone_full_frac"): o[k] = avg("t18_v3_" + k)
    for k in ("near_zone_sends", "near_overflow_sends", "near_overflow_borrowed"): o[k] = g("t18_v3_" + k)
    # per-hop rtt (sent-weighted over GPMs)
    hop = {}
    for k in list(S):
        m = re.match(r"t18_hop(\d+)_sent$", k)
        if m: hop[int(m[1])] = S[k]
    o["hop_sent"] = {h: hop[h] for h in sorted(hop)}
    # owner / multicast
    o["owner_reads"] = g("t18_owner_reads"); o["owner_merged"] = g("t18_owner_merged")
    o["owner_merge_frac"] = div(g("t18_owner_merged"), g("t18_owner_reads"))
    o["owner_merged_entries"] = g("t18_v3_owner_merged_entries")
    o["owner_same_requester_unicast"] = g("t18_owner_same_requester_unicast"); o["owner_table_full_fallbacks"] = g("t18_owner_table_full_fallbacks")
    o["owner_table_avg"] = avg("t18_v3_owner_table_avg"); o["owner_table_full_frac"] = avg("t18_v3_owner_table_full_frac")
    o["owner_batch_child_reads"] = g("t18_owner_batch_child_reads"); o["owner_batch_child_merged"] = g("t18_owner_batch_child_merged")
    o["mc_packets"] = g("t18_v3_mc_packets"); o["mc_dests_avg"] = div(g("t18_owner_multicast_dests"), g("t18_owner_multicast_packets"))
    o["mc_responses_saved"] = g("t18_v3_mc_responses_saved"); o["mc_unicast_fanout_packets"] = g("t18_v3_mc_unicast_fanout_packets")
    # v3m2w: effective window / table (mean over GPMs that report it)
    o["win_stat"] = avg("t18_window") if N["t18_window"] else avg("rdma_max_outstanding")
    o["table_stat"] = avg("t18_req_table_slots")
    o["rdma_max_outstanding_stat"] = avg("rdma_max_outstanding")
    o["req_peak_inflight_max"] = None
    o["rdma_req_peak_outstanding_sum"] = g("rdma_requester_peak_outstanding")
    o["owner_multicast_packets"] = g("t18_owner_multicast_packets")
    o["owner_single_responses"] = g("t18_owner_single_responses")
    o["req_latency_avg_ns"] = avg("t18_req_latency_avg_ns")
    # packing / prefetch
    pk = g("remote_bitmap_packets") + g("remote_single_packets")
    o["req_packets"] = pk; o["wire_lines"] = g("remote_wire_lines"); o["lines_per_packet"] = div(g("remote_wire_lines"), pk)
    o["multi_line_packet_frac"] = div(pk - g("remote_batch_size_01_packets"), pk)
    o["net_req_bytes"] = g("remote_network_request_bytes"); o["net_rsp_bytes"] = g("remote_network_response_bytes")
    o["pf_piggyback"] = g("remote_prefetch_piggyback_lines"); o["pf_useful"] = g("remote_prefetch_useful"); o["pf_unused"] = g("remote_prefetch_unused")
    o["pf_useful_frac"] = div(g("remote_prefetch_useful"), g("remote_prefetch_useful") + g("remote_prefetch_unused"))
    o["pf_avoided_frac"] = div(g("remote_prefetch_avoided_remote_requests"), lr)
    o["pre_network_wait_ns"] = div(g("remote_pre_network_wait_total_ns"), g("remote_pre_network_wait_samples"))
    o["batch_queue_wait_ns"] = div(g("remote_batch_queue_wait_total_ns"), g("remote_batch_queue_wait_samples"))
    # M3
    o["m3_l2_hits"] = g("remote_requester_l2_hits"); o["m3_hit_frac"] = div(g("remote_requester_l2_hits"), lr)
    o["m3_fill_attempts"] = g("remote_l2_two_touch_fill_attempts"); o["m3_installed"] = g("remote_l2_two_touch_installed_fills")
    o["m3_useful_fills"] = g("remote_useful_two_touch_fills"); o["m3_unused_retire"] = g("remote_unused_two_touch_retirements")
    o["m3_local_clean_protect_drops"] = g("remote_local_clean_protection_drops"); o["m3_tracked_evictions"] = g("remote_tracked_evictions")
    o["m3_boundary_lines_invalidated"] = g("remote_reuse_boundary_lines_invalidated"); o["m3_probe_lat_ns"] = div(g("remote_probe_latency_total_ns"), g("remote_probe_latency_samples"))
    o["m3_one_touch_bypasses"] = g("remote_l2_one_touch_probe_bypasses")
    # L2 miss totals for pollution check
    o["l2_read_hit"] = g("L2:read-hit"); o["l2_read_miss"] = g("L2:read-miss"); o["l2_read_mshr_hit"] = g("L2:read-mshr-hit")
    o["l2_read_miss_rate"] = div(g("L2:read-miss"), g("L2:read-hit") + g("L2:read-miss") + g("L2:read-mshr-hit"))
    o["dram_reads"] = g("DRAM:reads")
    # gpm trace busy48
    gt = R + "/stats/metrics_gpm_trace.csv"
    if os.path.exists(gt):
        K = {}
        for r in csv.DictReader(open(gt)):
            k = r["kernel"]
            d = K.setdefault(k, dict(all=0, dur=0, gpms=0))
            try:
                d["all"] = f(r["kernel_all_gpm_active_ns"]) or 0
                st = f(r["kernel_launch_ns"]); en = f(r["kernel_end_ns"]) or f(r["gpm_kernel_end_ns"])
                if en is None: en = max(f(r["last_wg_end_ns"]) or 0, d.get("end", 0))
                d["start"] = st; d["end"] = max(d.get("end", 0), en or 0)
                if (f(r["wg_assigned"]) or 0) > 0: d["gpms"] += 1
            except Exception: pass
        tot = sum(max(0, d["end"] - d["start"]) for d in K.values() if "start" in d)
        o["busy48_frac"] = div(sum(d["all"] for d in K.values()), tot)
        o["kernels"] = len(K)
    print(json.dumps(o))
