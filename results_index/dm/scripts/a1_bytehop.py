#!/usr/bin/env python3
"""Item 1: byte x hop decomposition (offline, from raw/*.jsonl.gz).
Outputs: bytehop_summary.csv, bytehop_by_hop.csv, bytehop_by_ownerpos.csv,
links_class.csv, bytehop_savings.csv.
Byte model (akita switching/endpoint.go msgToFlits, encoding overhead 0.25, 16 B flit):
  read request 12 B -> 1 flit; data response 64+4 B -> 6 flits; write 64+12 B -> 6 flits; write ack -> 1 flit;
  bitmap request 20 B -> 2 flits; bitmap response 4+64n B -> ceil(1.25(4+64n)/16) flits; multicast rsp 64+10 B -> 6 flits.
"""
import sys, os, csv, math, collections
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from load import *
FL = 16
def flits(b): return (int(math.ceil(b * 1.25)) - 1) // FL + 1
READ_B = (flits(12) + flits(68)) * FL       # 112
RSP_B = flits(68) * FL                     # 96
REQ_B = flits(12) * FL                     # 16
def bitmap_rsp_flits(n): return flits(4 + 64 * n)
R = load()
OUT = HERE
def lat_rows(o): return [r for r in o["latency"] if r["hops"] != "all"]
def pair_matrix(o):
    """Estimated owner->requester read matrix (requester g, owner d): Little's law on the per-pair
    in-flight samples (mean / rtt(hop)), normalised so each requester's row sums to its completed
    remote reads (occupancy.remote_reads)."""
    rt = {int(r["hops"]): (F(r["rtt_mean"]) + F(r["wait_rrt_mean"])) or 1 for r in lat_rows(o)}
    occ = {int(r["gpm"]): F(r["remote_reads"]) for r in o["occupancy"]}
    P = collections.defaultdict(dict)
    for r in o["pairs"]:
        g, d = int(r["gpm"]), int(r["dst"])
        P[g][d] = F(r["mean"]) / rt.get(hops(g, d), 1)
    M = {}
    for g, dd in P.items():
        s = sum(dd.values())
        for d, v in dd.items():
            if s: M[(g, d)] = occ.get(g, 0) * v / s
    return M
def link_class(a, b):
    c = []
    if a in CORNERS or b in CORNERS: c.append("corner")
    if a == CENTER or b == CENTER: c.append("center")
    xa, ya, xb, yb = a % W, a // W, b % W, b // W
    if {xa, xb} in ({2, 3}, {3, 4}) or {ya, yb} in ({2, 3}, {3, 4}): c.append("mid_cut")  # links adjacent to the centre row/col
    return c
summ, byhop, bypos, lk, sav = [], [], [], [], []
for k in sorted(R):
    sc, wl, cfg = k
    o = R[k]; s = o["summary"]; lat = lat_rows(o)
    T = o.get("total_time") or 0
    mesh = s.get("mesh_bytes", 0)
    # --- per hop
    nnet = {int(r["hops"]): F(r["n_net"]) - F(r["n_req_merged_queued"]) for r in lat}
    for r in lat:
        h = int(r["hops"])
        byhop.append(dict(scenario=sc, workload=wl, config=cfg, hops=h, logical_reads=F(r["n"]), net_lines=nnet[h],
                          req_merged=F(r["n_req_merged_queued"]) + F(r["n_req_merged_inflight"]), local_hits=F(r["n_local"]),
                          multicast_leaf=F(r["n_multicast_leaf"]), read_bytehop_est=h * nnet[h] * READ_B,
                          rtt_mean_ns=F(r["rtt_mean"]), total_mean_ns=F(r["total_mean"])))
    n_log = sum(F(r["n"]) for r in lat); n_net = sum(nnet.values())
    bh_lines = sum(h * v for h, v in nnet.items())
    # read bytes x hop. baseline: exact per-hop counts x 112 B. Complete: per-hop line counts x measured bytes/line
    G = o["gpm"]
    g = lambda key: sum(v.get(key, 0) for v in G.values())
    if cfg == "baseline":
        rd_bh = bh_lines * READ_B
    else:
        # flits per line from packet mix
        bs = {n: g(f"remote_batch_size_{n:02d}_packets") for n in range(1, 9)}
        single_rsp = g("t18_owner_single_responses")
        bm_rsp_p, bm_rsp_l = g("remote_bitmap_response_packets"), g("remote_bitmap_response_lines")
        req_fl = sum(v * (flits(12) if n == 1 else flits(20)) for n, v in bs.items())
        n_bm = bm_rsp_l / bm_rsp_p if bm_rsp_p else 1
        rsp_fl = single_rsp * flits(68) + bm_rsp_p * bitmap_rsp_flits(n_bm) + g("t18_owner_multicast_packets") * flits(74)
        wire = g("remote_wire_lines") or n_net
        per_line = (req_fl + rsp_fl) * FL / wire if wire else READ_B
        rd_bh = bh_lines * per_line
    wr = s.get("remote_writes_seen", 0)
    # --- owner position (estimated matrix; Complete scaled per owner to exact t18_owner_reads)
    M = pair_matrix(o)
    # column scaling: Complete -> exact owner wire lines (t18_owner_reads); baseline -> owner-side RDMA
    # incoming transactions (reads + writes) x reads/(reads+writes), i.e. writes assumed spread like reads.
    wr0 = s.get("remote_writes_seen", 0)
    method = "exact_owner_lines" if cfg == "Complete" else ("incoming_scaled" if wr0 <= 0.2 * n_log else "inflight_only")
    if cfg == "Complete":
        own_exact = {t: G[t].get("t18_owner_reads", 0) for t in G}
    elif method == "inflight_only":
        own_exact = None
    else:
        inc = {t: G[t].get("incoming_trans_count", 0) for t in G}
        ti = sum(inc.values()) or 1
        own_exact = {t: v * n_log / ti for t, v in inc.items()}
    if own_exact is not None:
        est_own = collections.Counter()
        for (gg, d), v in M.items(): est_own[d] += v
        M = {(gg, d): v * own_exact.get(d, 0) / est_own[d] for (gg, d), v in M.items() if est_own[d]}
    pos_reads = collections.Counter(); pos_bh = collections.Counter(); own_reads = collections.Counter()
    for (gg, d), v in M.items():
        pos_reads[pos(d)] += v; pos_bh[pos(d)] += v * hops(gg, d); own_reads[d] += v
    tot_r = sum(pos_reads.values()) or 1; tot_bh = sum(pos_bh.values()) or 1
    est_mean_hop = tot_bh / tot_r
    for p in ("corner", "edge", "interior"):
        bypos.append(dict(scenario=sc, workload=wl, config=cfg, owner_pos=p, n_owners={"corner": 4, "edge": 20, "interior": 24}[p],
                          read_share=pos_reads[p] / tot_r, bytehop_share=pos_bh[p] / tot_bh,
                          mean_hops=pos_bh[p] / pos_reads[p] if pos_reads[p] else 0))
    top = own_reads.most_common(2)
    # --- links
    L = {}
    for r in o["links"] or []:
        a, b = link_tiles(r); L[(a, b)] = (F(r["bytes"]), F(r["util"]), F(r["max_window_util"]))
    cls_b = collections.Counter(); cls_n = collections.Counter(); cls_max = collections.defaultdict(float); cls_wmax = collections.defaultdict(float)
    for (a, b), (by, u, wu) in L.items():
        for c in link_class(a, b) + ["all"]:
            cls_b[c] += by; cls_max[c] = max(cls_max[c], u); cls_wmax[c] = max(cls_wmax[c], wu)
    ncls = collections.Counter()
    for a in range(49):
        for b in range(49):
            if hops(a, b) == 1:
                for c in link_class(a, b) + ["all"]: ncls[c] += 1
    for c in ("all", "corner", "center", "mid_cut"):
        lk.append(dict(scenario=sc, workload=wl, config=cfg, link_class=c, links=ncls[c], bytes_share=cls_b[c] / (cls_b["all"] or 1),
                       mean_util=(cls_b[c] / ncls[c]) / ((48 if "b48" in sc else 384) * T * 1e9) if T else 0,
                       max_util=cls_max[c], max_window_util=cls_wmax[c]))
    mx = max(L.items(), key=lambda x: x[1][1]) if L else ((0, 0), (0, 0, 0))
    # hot owner out links (YX routing: Y first)
    hot = top[0][0] if top else -1
    hot_out = sorted([(b, L[(a, b)][0]) for (a, b) in L if a == hot], key=lambda x: -x[1])
    hot_out_tot = sum(v for _, v in hot_out)
    summ.append(dict(scenario=sc, workload=wl, config=cfg, total_time_us=T * 1e6, mesh_bytehop_MB=mesh / 1e6,
                     read_bytehop_est_MB=rd_bh / 1e6, read_share_of_mesh=rd_bh / mesh if mesh else 0,
                     remote_writes=wr, other_bytehop_MB=(mesh - rd_bh) / 1e6,
                     logical_reads=n_log, net_lines=n_net, mean_hops_net=bh_lines / n_net if n_net else 0,
                     est_mean_hops_matrix=est_mean_hop, owner_est_method=method,
                     top_owner=hot, top_owner_pos=pos(hot) if hot >= 0 else "", top_owner_share=top[0][1] / tot_r if top else 0,
                     top_owner_mean_hops=sum(hops(gg, hot) * v for (gg, d), v in M.items() if d == hot) / (top[0][1] or 1) if top else 0,
                     second_owner=top[1][0] if len(top) > 1 else -1, second_owner_share=top[1][1] / tot_r if len(top) > 1 else 0,
                     corner_owner_read_share=pos_reads["corner"] / tot_r,
                     max_link=f"{mx[0][0]}->{mx[0][1]}", max_link_util=mx[1][1], max_link_window_util=mx[1][2],
                     hot_owner_out_links=";".join(f"{hot}->{b}:{v / (hot_out_tot or 1):.2f}" for b, v in hot_out),
                     hot_owner_out_max_util=max((L[(hot, b)][1] for b, _ in hot_out), default=0),
                     hot_owner_out_max_window_util=max((L[(hot, b)][2] for b, _ in hot_out), default=0)))
# --- savings: Complete vs baseline
idx = {(r["scenario"], r["workload"], r["config"]): r for r in summ}
hb = collections.defaultdict(dict)
for r in byhop: hb[(r["scenario"], r["workload"], r["config"])][r["hops"]] = r
for (sc, wl, cfg), r in sorted(idx.items()):
    if cfg != "Complete" or (sc, wl, "baseline") not in idx: continue
    b = idx[(sc, wl, "baseline")]; o = R[(sc, wl, cfg)]; G = o["gpm"]
    g = lambda key: sum(v.get(key, 0) for v in G.values())
    H = hb[(sc, wl, cfg)]
    m3 = sum(h * x["local_hits"] for h, x in H.items()) * READ_B
    mrg = sum(h * x["req_merged"] for h, x in H.items()) * READ_B
    hbar = r["mean_hops_net"] or 1
    # packing: request side saves (n-1) single-request flits per n-line bitmap minus the bigger header
    bs = {n: g(f"remote_batch_size_{n:02d}_packets") for n in range(2, 9)}
    pk_req = sum(v * (n * flits(12) - flits(20)) for n, v in bs.items()) * FL * hbar
    bp, bl = g("remote_bitmap_response_packets"), g("remote_bitmap_response_lines")
    nb = bl / bp if bp else 1
    pk_rsp = bp * (nb * flits(68) - bitmap_rsp_flits(nb)) * FL * hbar if bp else 0
    # prefetch cost: piggybacked lines that turned out unused travel anyway (lines - useful)
    pf_cost = g("remote_prefetch_wire_lines") * RSP_B * hbar
    pf_useful = g("remote_prefetch_useful")
    # multicast (M2): unicast d responses vs tree; tree/unicast ratio from m1_windows (1 us, sharers>=2)
    rows = [w for w in (o["m1_windows"] or []) if w["window_ns"] == "1000" and int(w["sharers"]) >= 2]
    so = sum(F(w["sum_owner_hops"]) for w in rows); st = sum(F(w["sum_tree_links"]) for w in rows)
    tree_ratio = st / so if so else 1
    md = g("t18_owner_multicast_dests"); mp = g("t18_owner_multicast_packets")
    mc = md * hbar * RSP_B * (1 - tree_ratio)
    dlog = (b["logical_reads"] - r["logical_reads"]) * READ_B * b["mean_hops_net"]
    actual = (b["mesh_bytehop_MB"] - r["mesh_bytehop_MB"]) * 1e6
    expl = m3 + mrg + pk_req + pk_rsp + mc - pf_cost
    sav.append(dict(scenario=sc, workload=wl, base_mesh_MB=b["mesh_bytehop_MB"], comp_mesh_MB=r["mesh_bytehop_MB"],
                    saved_frac=actual / (b["mesh_bytehop_MB"] * 1e6) if b["mesh_bytehop_MB"] else 0,
                    saved_MB=actual / 1e6, m3_MB=m3 / 1e6, req_merge_MB=mrg / 1e6, pack_req_MB=pk_req / 1e6, pack_rsp_MB=pk_rsp / 1e6,
                    multicast_MB=mc / 1e6, prefetch_cost_MB=-pf_cost / 1e6, prefetch_useful_lines=pf_useful,
                    logical_read_diff_MB=dlog / 1e6, residual_MB=(actual - expl) / 1e6,
                    m3_share=m3 / actual if actual > 0 else 0, merge_share=mrg / actual if actual > 0 else 0,
                    pack_share=(pk_req + pk_rsp) / actual if actual > 0 else 0, mc_share=mc / actual if actual > 0 else 0))
def wcsv(name, rows):
    if not rows: return
    with open(os.path.join(OUT, name), "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0].keys())); w.writeheader()
        for r in rows: w.writerow({k: (f"{v:.4g}" if isinstance(v, float) else v) for k, v in r.items()})
wcsv("bytehop_summary.csv", summ); wcsv("bytehop_by_hop.csv", byhop); wcsv("bytehop_by_ownerpos.csv", bypos)
wcsv("links_class.csv", lk); wcsv("bytehop_savings.csv", sav)
print("ok", len(summ), len(sav))
