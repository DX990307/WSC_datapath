#!/usr/bin/env python3
"""Collects the per-direction upper bounds on top of Complete into ranking.csv (one row per
scenario x workload). Fractions are of Complete's network read lines ("net") and of Complete's
total mesh byte x hop ("mesh"). Compression is an ASSUMED ratio (no data content recorded)."""
import csv, os, collections
H = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
rd = lambda f: list(csv.DictReader(open(os.path.join(H, f))))
F = float
S = {(r["scenario"], r["workload"], r["config"]): r for r in rd("bytehop_summary.csv")}
A = {(r["scenario"], r["workload"], r["config"]): r for r in rd("dup_same_gpm.csv")}
P = {(r["scenario"], r["workload"], r["config"], r["window_ns"], r["K"]): r for r in rd("push_bound.csv")}
O = {(r["scenario"], r["workload"], r["config"], r["wait_ns"]): r for r in rd("owner_wait.csv")}
Hs = {(r["scenario"], r["workload"], r["config"]): r for r in rd("hotspot_whatif.csv")}
out = []
for (sc, wl, cfg), s in sorted(S.items()):
    if cfg != "Complete": continue
    a = A[(sc, wl, cfg)]; mesh = F(s["mesh_bytehop_MB"]); rshare = F(s["read_share_of_mesh"])
    net = F(a["net_lines"]); nlog = F(a["logical_reads"])
    m3 = F(a["repeat_net_fetch_frac_of_net"])
    p = P[(sc, wl, cfg, "10000", "4")]; p1 = P[(sc, wl, cfg, "1000", "4")]
    push_net = max(F(p["saved_remote_reads"]), F(p1["saved_remote_reads"])) / (net or 1)
    push_mesh = max(F(p["saved_frac_of_mesh"]), F(p1["saved_frac_of_mesh"]))
    ow = O[(sc, wl, "baseline", "200")] if (sc, wl, "baseline", "200") in O else O[(sc, wl, cfg, "200")]
    h = Hs[(sc, wl, cfg)]
    hot_mesh = (F(h["read_bytehop_MB"]) - F(h["rep4_bytehop_MB"])) / mesh if mesh else 0
    out.append(dict(scenario=sc, workload=wl,
                    m3_retain_net=m3, m3_retain_mesh=m3 * rshare,
                    push_K4_net=min(1.0, push_net), push_K4_mesh=push_mesh,
                    push_useless_opt=F(p["useless_push_frac_optimistic"]), push_useless_pess=F(p["useless_push_frac_pessimistic"]),
                    ownerwait200_extra_merge=F(ow["extra_merge_frac_of_owner_reads"]), ownerwait200_mesh=F(ow["saved_frac_of_mesh"]),
                    ownerwait200_over_rtt=F(ow["wait_over_rtt"]),
                    hot_share=F(h["hot_share"]), hot_rep4_mesh=hot_mesh, hot_rep4_maxlink_ratio=F(h["rep4_max_link_ratio"]),
                    hot_move_maxlink_ratio=F(h["move_max_link_ratio"]),
                    compress15_mesh_assumed=rshare * (112 - (16 + 64)) / 112,   # 64 B -> ~43 B (+4 B hdr): 6 -> 4 flits per response
                    compress2_mesh_assumed=rshare * (112 - (16 + 48)) / 112,    # 64 B -> 32 B: 6 -> 3 flits
                    complete_max_link_window_util=F(s["max_link_window_util"]), complete_max_link_util=F(s["max_link_util"])))
with open(os.path.join(H, "ranking.csv"), "w", newline="") as f:
    w = csv.DictWriter(f, fieldnames=list(out[0].keys())); w.writeheader()
    for r in out: w.writerow({k: (f"{v:.3g}" if isinstance(v, float) else v) for k, v in r.items()})
for r in out: print(" ".join(f"{v:.2f}" if isinstance(v, float) else str(v)[:10] for v in r.values()))
