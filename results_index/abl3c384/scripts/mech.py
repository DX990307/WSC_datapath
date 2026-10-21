#!/usr/bin/env python3
"""Mechanism statistics table from .rows.json (ext.py) + obs_raw CSVs. Writes mechanism_stats.csv; prints markdown."""
import json, csv, collections, glob, os
D = "/Users/daoxuan/Desktop/CuPath/isca27/results_index/abl3c384"
rows = json.load(open(f"{D}/.rows.json"))
R = collections.defaultdict(dict)
nm = {"mi200": "base", "mi200+v3complete": "C", "mi200+v3m1": "M1", "mi200+v3m1m2": "M1M2", "mi200+v3m1m3": "M1M3", "mi200+m3": "M3",
      "mi200+v3c-nomerge": "nomerge", "mi200+v3c-nopack": "nopack", "mi200+v3c-allocshared": "shared", "mi200+v3c-allocnear": "near"}
for o in rows:
    p = o["exp"].split("_"); R[p[2].replace("v3c-", "")][nm.get(p[3], p[3])] = o
def load(name):
    out = collections.defaultdict(list)
    for h in ("yss2", "yss3"):
        for r in csv.DictReader(open(f"{D}/obs_raw/{h}/{name}.csv")) if os.path.exists(f"{D}/obs_raw/{h}/{name}.csv") else []:
            out[(r["workload"].replace("v3c-", ""), r["config"])].append(r)
    return out
F = lambda x: float(x) if x not in (None, "") else 0.0
lat, occ, links, share, agg, reuse, own = map(load, ["latency_segments", "occupancy", "links", "sharing", "requester_agg", "remote_reuse", "owner_shift"])
def seg(w, c):
    rs = lat.get((w, c), [])
    out = {}
    for s in ("wait_rrt", "fwd", "owner_queue", "owner_service_hit", "owner_service_miss", "owner_rsp_wait", "ret", "to_l1v", "merge_wait", "rtt", "total"):
        n = sum(F(r[f"{s}_n"]) for r in rs); out[s] = sum(F(r[f"{s}_n"]) * F(r[f"{s}_mean"]) for r in rs) / n if n else None
    n = sum(F(r["n"]) for r in rs); out["n"] = n
    out["local_frac"] = sum(F(r["n_local"]) for r in rs) / n if n else None
    out["multicast_leaf_frac"] = sum(F(r["n_multicast_leaf"]) for r in rs) / n if n else None
    # near vs far wait
    for g, hs in (("near", range(1, 4)), ("far", range(7, 13))):
        rr = [r for r in rs if r["hops"].isdigit() and int(r["hops"]) in hs]
        nn = sum(F(r["wait_rrt_n"]) for r in rr)
        out[f"wait_rrt_{g}"] = sum(F(r["wait_rrt_n"]) * F(r["wait_rrt_mean"]) for r in rr) / nn if nn else None
        nn = sum(F(r["total_n"]) for r in rr)
        out[f"total_{g}"] = sum(F(r["total_n"]) * F(r["total_mean"]) for r in rr) / nn if nn else None
    return out
def occs(w, c):
    rs = occ.get((w, c), [])
    if not rs: return {}
    k = len(rs)
    return dict(rrt_full=sum(F(r["frac_time_full"]) for r in rs) / k, rrt_used=sum(F(r["mean_used"]) for r in rs) / k,
                rrt_wait=sum(F(r["mean_waiting"]) for r in rs) / k, owner_inflight=sum(F(r["owner_in_flight_mean"]) for r in rs) / k,
                top_dest_share=sum(F(r["top_dest_share"]) for r in rs) / k,
                top_dest_mode=collections.Counter(r["top_dest"] for r in rs).most_common(1)[0])
one = lambda t, w, c, key: (t.get((w, c)) or [{}])[0].get(key)
cols = []
W = sorted(R)
for w in W:
    b, c = R[w].get("base", {}), R[w].get("C", {})
    if not c: continue
    sb, sc = seg(w, "mi200"), seg(w, "mi200+v3complete"); ob, oc = occs(w, "mi200"), occs(w, "mi200+v3complete")
    sh10 = [r for r in share.get((w, "mi200"), []) if r["window_ns"] == "1000"]
    row = dict(workload=w,
        speedup=b.get("total_time", 0) / c["total_time"] if b.get("total_time") else None,
        remote_reads=c.get("remote_reads"),
        base_wait_rrt=sb["wait_rrt"], base_total=sb["total"], base_rtt=sb["rtt"], base_wait_rrt_near=sb["wait_rrt_near"], base_wait_rrt_far=sb["wait_rrt_far"],
        C_wait_rrt=sc["wait_rrt"], C_total=sc["total"], C_rtt=sc["rtt"], C_owner_queue=sc["owner_queue"], C_merge_wait=sc["merge_wait"],
        C_local_frac=sc["local_frac"], C_wait_rrt_near=sc["wait_rrt_near"], C_wait_rrt_far=sc["wait_rrt_far"], C_total_near=sc["total_near"], C_total_far=sc["total_far"],
        base_rrt_full=ob.get("rrt_full"), base_rrt_wait=ob.get("rrt_wait"), C_rrt_full=oc.get("rrt_full"), base_top_dest_share=ob.get("top_dest_share"), base_top_dest=ob.get("top_dest_mode"),
        base_link_mean=F(one(links, w, "mi200", "link_mean")), base_link_p99=F(one(links, w, "mi200", "link_p99")), base_link_win_max=F(one(links, w, "mi200", "link_window_max")),
        C_link_mean=F(one(links, w, "mi200+v3complete", "link_mean")), C_link_win_max=F(one(links, w, "mi200+v3complete", "link_window_max")),
        base_mesh_bytes=F(one(links, w, "mi200", "mesh_bytes")), C_mesh_bytes=F(one(links, w, "mi200+v3complete", "mesh_bytes")),
        shared1us=F(sh10[0]["shared_read_frac"]) if sh10 else None, sharers1us=F(sh10[0]["mean_sharers"]) if sh10 else None,
        owner_dup_base=F(sh10[0]["owner_dup_frac"]) if sh10 else None, req_dup_same_gpm=F(sh10[0]["requester_dup_same_gpm_frac"]) if sh10 else None,
        mc_links_saved_all=F(sh10[0]["multicast_links_saved_all"]) if sh10 else None,
        agg_same_line=F(one(agg, w, "mi200", "same_line_dup_frac")), agg_same_page=F(one(agg, w, "mi200", "same_page_frac")),
        agg_same_page_16cyc=F(one(agg, w, "mi200", "same_page_le_16cyc_frac")),
        reuse_lines=F(one(reuse, w, "mi200", "frac_lines_reused")), reuse_reads=F(one(reuse, w, "mi200", "frac_reads_retouch")),
        reuse_after_complete=F(one(reuse, w, "mi200", "frac_reads_retouch_after_complete")), l2_free=F(one(reuse, w, "mi200", "l2_free_frac")),
        busy48_base=b.get("busy48_frac"), busy48_C=c.get("busy48_frac"))
    for k in ("req_merge_frac", "req_merged_into_sent", "req_merged_into_queued", "req_table_full_stalls", "req_bypasses", "req_window_full_frac",
              "req_inflight_avg", "near_inflight_avg", "mid_inflight_avg", "far_inflight_avg", "near_wait_full_frac", "far_wait_full_frac",
              "owner_reads", "owner_merged", "owner_merge_frac", "owner_merged_entries", "mc_packets", "mc_dests_avg", "mc_responses_saved",
              "owner_same_requester_unicast", "owner_table_avg", "owner_table_full_frac", "lines_per_packet", "multi_line_packet_frac",
              "pf_piggyback", "pf_useful_frac", "pf_avoided_frac", "m3_hit_frac", "m3_installed", "m3_fill_attempts", "m3_useful_fills",
              "m3_local_clean_protect_drops", "m3_probe_lat_ns", "remote_read_lat_ns", "net_req_bytes", "net_rsp_bytes", "wire_lines"):
        row["C_" + k] = c.get(k)
    m3 = R[w].get("M3", {}); row["M3only_hit_frac"] = m3.get("m3_hit_frac")
    for k in ("nomerge", "nopack", "M1M2"):
        o = R[w].get(k, {}); row[f"{k}_wire_lines"] = o.get("wire_lines"); row[f"{k}_req_packets"] = o.get("req_packets")
    cols.append(row)
keys = list(cols[0].keys())
with open(f"{D}/mechanism_stats.csv", "w", newline="") as f:
    wr = csv.DictWriter(f, keys); wr.writeheader(); [wr.writerow(r) for r in cols]
def p(x, d=2):
    if x is None: return "–"
    if isinstance(x, (tuple, list)): return str(x[0])
    return f"{x:.{d}f}" if abs(x) < 1e5 else f"{x:.3g}"
import sys
sel = sys.argv[1:] or keys
for r in cols: print(" | ".join([r["workload"]] + [f"{k}={p(r.get(k))}" for k in sel if k != "workload"]))
