#!/usr/bin/env python3
"""Item 2: repeated fetches and cross-GPM sharing bounds (offline, raw/*.jsonl.gz).
Outputs: dup_same_gpm.csv (a), sharing_windows.csv (b), push_bound.csv (c), owner_wait.csv (d).
All counts at the L1V->RDMA boundary (logical remote reads) unless noted."""
import sys, os, csv, math, collections
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from load import *
READ_B, RSP_B, REQ_B = 112, 96, 16
R = load()
def allrow(o): return next(r for r in o["latency"] if r["hops"] == "all")
def lat(o): return [r for r in o["latency"] if r["hops"] != "all"]
def gsum(o, k): return sum(v.get(k, 0) for v in o["gpm"].values())
def wrows(o, T): return [w for w in (o["m1_windows"] or []) if w["window_ns"] == str(T)]
A, B, C, D = [], [], [], []
for k in sorted(R):
    sc, wl, cfg = k; o = R[k]; s = o["summary"]; mi = o["m1_inflight"]; ar = allrow(o)
    n_log = sum(F(r["n"]) for r in lat(o))
    net_lines = sum(F(r["n_net"]) - F(r["n_req_merged_queued"]) for r in lat(o))
    bh_read = sum(int(r["hops"]) * F(r["n"]) for r in lat(o)) * READ_B   # if every logical read went to the network
    mean_h = sum(int(r["hops"]) * F(r["n"]) for r in lat(o)) / (n_log or 1)
    # ---------- (a) same GPM
    uniq = s.get("reuse_unique_lines", 0); rr = s.get("reuse_remote_reads", 0) or 1
    bins = [(s[f"reuse_interval_bin_{i:03d}_ns_ge"], s[f"reuse_interval_bin_{i:03d}_count"]) for i in range(200) if f"reuse_interval_bin_{i:03d}_count" in s]
    tot = sum(c for _, c in bins) or 1
    le = lambda x: sum(c for v, c in bins if v < x) / tot
    pf = gsum(o, "remote_prefetch_wire_lines")
    pfu = gsum(o, "remote_prefetch_useful")
    rep_net = max(0.0, net_lines - uniq)   # lower-bound style: unique (GPM,line,epoch) need >= 1 fetch each (useful prefetches may already be inside net_lines)
    A.append(dict(scenario=sc, workload=wl, config=cfg, logical_reads=n_log, unique_gpm_lines=uniq,
                  retouch_frac=s.get("reuse_retouch_reads", 0) / rr,
                  retouch_after_complete_frac=s.get("reuse_retouch_reads_after_complete", 0) / rr,
                  inflight_same_gpm_frac=mi.get("requester_dup_same_gpm", 0) / (mi.get("requester_remote_reads") or 1),
                  retouch_interval_p50_ns=s.get("reuse_interval_p50_ns", 0), retouch_lt_1us=le(1000), retouch_lt_10us=le(10000),
                  net_lines=net_lines, prefetch_lines=pf,
                  repeat_net_fetch_frac_of_reads=rep_net / (n_log or 1), repeat_net_fetch_frac_of_net=rep_net / (net_lines or 1),
                  m3_hits=gsum(o, "L2:remote_requester_l2_hits"), m3_installed=gsum(o, "L2:remote_l2_two_touch_installed_fills"),
                  m3_useful_fills=gsum(o, "L2:remote_useful_two_touch_fills"), m3_unused_retired=gsum(o, "L2:remote_unused_two_touch_retirements"),
                  l2_free_frac=None))
    # ---------- (b) cross-GPM sharing
    for T in (1000, 10000):
        rows = wrows(o, T)
        L = sum(F(w["lines"]) for w in rows); Q = sum(F(w["requests"]) for w in rows)
        sh = [w for w in rows if int(w["sharers"]) >= 2]
        Ls = sum(F(w["lines"]) for w in sh); Qs = sum(F(w["requests"]) for w in sh)
        dupx = sum(F(w["lines"]) * (int(w["sharers"]) - 1) for w in sh)
        inc = sum(F(w["lines"]) * int(w["sharers"]) for w in rows)
        B.append(dict(scenario=sc, workload=wl, config=cfg, window_ns=T, l1v_remote_reads=Q, window_lines=L,
                      shared_line_frac=Ls / (L or 1), reads_to_shared_lines_frac=Qs / (Q or 1),
                      mean_sharers_shared=sum(F(w["lines"]) * int(w["sharers"]) for w in sh) / (Ls or 1),
                      cross_gpm_first_touch_dups=dupx, cross_gpm_dup_frac_of_reads=dupx / (Q or 1),
                      first_touch_incidences=inc, same_gpm_repeat_in_window_frac=(Q - inc) / (Q or 1),
                      mean_pair_hops=sum(F(w["sum_pair_hops"]) for w in sh) / (sum(F(w["pairs"]) for w in sh) or 1),
                      mean_owner_hops_shared=sum(F(w["sum_owner_hops"]) for w in sh) / (inc - (L - Ls) or 1),
                      tree_over_unicast=sum(F(w["sum_tree_links"]) for w in sh) / (sum(F(w["sum_owner_hops"]) for w in sh) or 1)))
    # time scale points (same definition: read has another GPM's read of the line outstanding)
    oq = F(ar["owner_queue_mean"]); nh, nm = F(ar["owner_service_hit_n"]), F(ar["owner_service_miss_n"])
    svc = (nh * F(ar["owner_service_hit_mean"]) + nm * F(ar["owner_service_miss_mean"])) / ((nh + nm) or 1)
    tau_o = oq + svc + F(ar["owner_rsp_wait_mean"])
    f_o = s.get("owner_same_line_concurrent_other_gpm_frac", 0)
    tau_r = F(ar["total_mean"])
    f_r = (mi.get("requester_dup_other_gpm_only", 0) + mi.get("requester_dup_same_and_other_gpm", 0)) / (mi.get("requester_remote_reads") or 1)
    # nearest other-GPM outstanding reader distance
    mh = {int(kk.rsplit("_", 1)[1]): v for kk, v in mi.items() if kk.startswith("requester_dup_other_gpm_only_min_hops_")}
    mht = sum(mh.values()) or 1
    # ---------- (c) push bound
    for T in (1000, 10000):
        rows = wrows(o, T)
        Q = sum(F(w["requests"]) for w in rows)
        L = sum(F(w["lines"]) for w in rows)
        Ls = sum(F(w["lines"]) for w in rows if int(w["sharers"]) >= 2)
        for K in (1, 2, 4, 8, 47):
            saved = 0.0; sbh = 0.0; pushes_hist = 0.0; useful = 0.0
            for w in rows:
                sh = int(w["sharers"]); ln = F(w["lines"])
                if sh < 2: continue
                u = min(K, sh - 1); useful += ln * u
                soh, stl = F(w["sum_owner_hops"]), F(w["sum_tree_links"])
                full = READ_B * soh - REQ_B * soh / sh - RSP_B * stl   # s unicast fetches -> 1 request + 1 multicast tree
                sbh += full * u / (sh - 1)
            cap = net_lines if cfg == "Complete" else n_log   # cannot save more network fetches than the run made
            saved = min(useful, cap); sbh *= saved / useful if useful else 0
            useless_opt = (Ls * K - useful) / (Ls * K) if Ls else 0          # only lines shared in this window have history
            useless_pess = (L * K - useful) / (L * K) if L else 0             # every line has K remembered readers
            C.append(dict(scenario=sc, workload=wl, config=cfg, window_ns=T, K=K if K < 47 else "inf",
                          saved_remote_reads=saved, saved_frac_of_reads=saved / (n_log or 1),
                          saved_bytehop_MB=sbh / 1e6, saved_frac_of_mesh=sbh / (s.get("mesh_bytes") or 1),
                          useless_push_frac_optimistic=useless_opt, useless_push_frac_pessimistic=useless_pess))
    # ---------- (d) owner wait
    def f(t):
        if t <= tau_o or tau_r <= tau_o: return f_o
        if t >= tau_r: return f_r
        return f_o + (f_r - f_o) * math.log(t / tau_o) / math.log(tau_r / tau_o)
    r2 = [w for w in wrows(o, 1000) if int(w["sharers"]) == 2]
    per_merge_saved_hops = (F(r2[0]["sum_owner_hops"]) - F(r2[0]["sum_tree_links"])) / F(r2[0]["lines"]) if r2 and F(r2[0]["lines"]) else 0
    own_reads = s.get("owner_reads", 0)
    for t in (100, 200, 500):
        extra = max(0.0, f(t) - f_o) * own_reads
        D.append(dict(scenario=sc, workload=wl, config=cfg, wait_ns=t, tau_owner_ns=tau_o, f_owner=f_o, tau_l1v_ns=tau_r, f_l1v=f_r,
                      f_at_wait=f(t), extra_owner_merges=extra, extra_merge_frac_of_owner_reads=extra / (own_reads or 1),
                      saved_bytehop_MB=extra * per_merge_saved_hops * RSP_B / 1e6,
                      saved_frac_of_mesh=extra * per_merge_saved_hops * RSP_B / (s.get("mesh_bytes") or 1),
                      rtt_mean_ns=F(ar["rtt_mean"]), wait_over_rtt=t / (F(ar["rtt_mean"]) or 1),
                      nearest_sharer_1hop_frac=mh.get(1, 0) / mht, nearest_sharer_le2hop_frac=(mh.get(1, 0) + mh.get(2, 0)) / mht))
def wcsv(name, rows):
    with open(os.path.join(HERE, name), "w", newline="") as fh:
        w = csv.DictWriter(fh, fieldnames=list(rows[0].keys())); w.writeheader()
        for r in rows: w.writerow({k: (f"{v:.4g}" if isinstance(v, float) else v) for k, v in r.items()})
wcsv("dup_same_gpm.csv", A); wcsv("sharing_windows.csv", B); wcsv("push_bound.csv", C); wcsv("owner_wait.csv", D)
print("ok")
