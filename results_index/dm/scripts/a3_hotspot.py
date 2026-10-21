#!/usr/bin/env python3
"""Hot-owner placement / replication what-if (offline). Routes the estimated read matrix
(a1_bytehop.pair_matrix, column-scaled as in a1) over the 7x7 mesh with Y-first routing
(akita mesh_routing_table.go) and compares (1) predicted vs measured link bytes and
(2) moving the hottest owner's reads to the best tile / 4 replicas. Output: hotspot_whatif.csv."""
import sys, os, csv, math, collections
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from load import *
RSP, REQ = 96, 16
R = load()
def lat_rows(o): return [r for r in o["latency"] if r["hops"] != "all"]
def matrix(o, cfg):
    rt = {int(r["hops"]): (F(r["rtt_mean"]) + F(r["wait_rrt_mean"])) or 1 for r in lat_rows(o)}
    occ = {int(r["gpm"]): F(r["remote_reads"]) for r in o["occupancy"]}
    P = collections.defaultdict(dict)
    for r in o["pairs"]:
        g, d = int(r["gpm"]), int(r["dst"]); P[g][d] = F(r["mean"]) / rt.get(hops(g, d), 1)
    M = {}
    for g, dd in P.items():
        s = sum(dd.values())
        for d, v in dd.items():
            if s: M[(g, d)] = occ.get(g, 0) * v / s
    G = o["gpm"]; n_log = sum(F(r["n"]) for r in lat_rows(o)); wr = o["summary"].get("remote_writes_seen", 0)
    own = None
    if cfg == "Complete": own = {t: G[t].get("t18_owner_reads", 0) for t in G}
    elif wr <= 0.2 * n_log:
        inc = {t: G[t].get("incoming_trans_count", 0) for t in G}; ti = sum(inc.values()) or 1
        own = {t: v * n_log / ti for t, v in inc.items()}
    if own:
        eo = collections.Counter()
        for (g, d), v in M.items(): eo[d] += v
        M = {(g, d): v * own.get(d, 0) / eo[d] for (g, d), v in M.items() if eo[d]}
    return M
def route(a, b):
    """Y first, then X. Returns directed links (tile->tile)."""
    x, y = a % W, a // W; bx, by = b % W, b // W; out = []
    while y != by:
        ny = y + (1 if by > y else -1); out.append((x + W * y, x + W * ny)); y = ny
    while x != bx:
        nx = x + (1 if bx > x else -1); out.append((x + W * y, nx + W * y)); x = nx
    return out
def load_links(M, remap=None):
    L = collections.Counter(); bh = 0.0
    for (g, d), v in M.items():
        srcs = remap(g, d) if remap else [(d, 1.0)]
        for dd, w in srcs:
            if dd == g: continue
            for l in route(dd, g): L[l] += v * w * RSP
            for l in route(g, dd): L[l] += v * w * REQ
            bh += v * w * (RSP + REQ) * hops(g, dd)
    return L, bh
GPMS = [t for t in range(49) if t != CENTER]
REPL4 = [8, 12, 36, 40]
rows = []
for k in sorted(R):
    sc, wl, cfg = k; o = R[k]
    M = matrix(o, cfg)
    meas = {}
    for r in o["links"] or []:
        meas[link_tiles(r)] = F(r["bytes"])
    L0, bh0 = load_links(M)
    keys = [l for l in set(meas) | set(L0)]
    xs = [L0.get(l, 0) for l in keys]; ys = [meas.get(l, 0) for l in keys]
    mx, my = sum(xs) / len(xs), sum(ys) / len(ys)
    corr = sum((a - mx) * (b - my) for a, b in zip(xs, ys)) / math.sqrt(sum((a - mx) ** 2 for a in xs) * sum((b - my) ** 2 for b in ys) + 1e-30)
    own = collections.Counter()
    for (g, d), v in M.items(): own[d] += v
    tot = sum(own.values()) or 1
    hot, hv = own.most_common(1)[0]
    # best single tile for the hot owner's data
    def mv(t): return lambda g, d: [(t, 1.0)] if d == hot else [(d, 1.0)]
    best = min(GPMS, key=lambda t: sum(v * hops(g, t) for (g, d), v in M.items() if d == hot))
    Lm, bhm = load_links(M, mv(best))
    def rep(g, d):
        if d != hot: return [(d, 1.0)]
        c = min(REPL4 + [hot], key=lambda t: (hops(g, t), t)); return [(c, 1.0)]
    Lr, bhr = load_links(M, rep)
    mxl = lambda L: max(L.values()) if L else 0
    argl = lambda L: "%d->%d" % max(L.items(), key=lambda x: x[1])[0] if L else ""
    T = o.get("total_time") or 1; bw = 48 if "b48" in sc else 384
    cap = bw * T * 1e9
    rows.append(dict(scenario=sc, workload=wl, config=cfg, pred_vs_measured_link_corr=corr,
                     hot_owner=hot, hot_pos=pos(hot), hot_share=hv / tot, best_tile=best,
                     read_bytehop_MB=bh0 / 1e6, move_bytehop_MB=bhm / 1e6, rep4_bytehop_MB=bhr / 1e6,
                     move_bytehop_ratio=bhm / bh0 if bh0 else 1, rep4_bytehop_ratio=bhr / bh0 if bh0 else 1,
                     max_link=argl(L0), max_link_util_pred=mxl(L0) / cap, max_link_util_meas=max(meas.values()) / cap if meas else 0,
                     move_max_link_util=mxl(Lm) / cap, rep4_max_link_util=mxl(Lr) / cap,
                     move_max_link_ratio=mxl(Lm) / mxl(L0) if mxl(L0) else 1, rep4_max_link_ratio=mxl(Lr) / mxl(L0) if mxl(L0) else 1))
with open(os.path.join(HERE, "hotspot_whatif.csv"), "w", newline="") as f:
    w = csv.DictWriter(f, fieldnames=list(rows[0].keys())); w.writeheader()
    for r in rows: w.writerow({k: (f"{v:.4g}" if isinstance(v, float) else v) for k, v in r.items()})
for r in rows:
    if r["hot_share"] > 0.15:
        print(r["scenario"], r["workload"][:10], r["config"][:4], f"corr={r['pred_vs_measured_link_corr']:.2f} hot={r['hot_owner']}({r['hot_share']:.2f}) best={r['best_tile']} bh move={r['move_bytehop_ratio']:.2f} rep4={r['rep4_bytehop_ratio']:.2f} maxlink {r['max_link']} pred={r['max_link_util_pred']:.2f} meas={r['max_link_util_meas']:.2f} move={r['move_max_link_ratio']:.2f} rep4={r['rep4_max_link_ratio']:.2f}")
import statistics
print("median corr", statistics.median(r["pred_vs_measured_link_corr"] for r in rows), min(r["pred_vs_measured_link_corr"] for r in rows))
