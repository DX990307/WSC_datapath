"""Owner->requester read matrices for the ASTRA-sim cross-check (P18), from dm/raw.

pairs.mean is the sampled mean number of in-flight reads from requester r to
owner o (1 us samples). By Little's law the read rate is in-flight / RTT, so
each pair is divided by the akita RTT at its hop count. The matrix is then
fitted (IPF) to the exact per-requester read counts (rdma_observed_remote_reads)
and per-owner counts (incoming_trans_count, scaled to reads only).
Writes are not modelled. Also returns the owner service delay (zero-load
owner queue + service + response wait) and the akita per-link flits.
"""
import os, sys
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "..", "dm", "scripts"))
import load  # noqa: E402
F, hops, W, CENTER = load.F, load.hops, load.W, load.CENTER
GPMS = [t for t in range(W * W) if t != CENTER]
_R = None


def runs():
    global _R
    if _R is None:
        _R = load.load()
    return _R


def matrix(sc, wl, iters=50):
    o = runs()[(sc, wl, "baseline")]
    lat = {(0 if r["hops"] == "all" else int(r["hops"])): r for r in o["latency"]}
    rtt = {h: F(lat[h]["rtt_mean"]) for h in lat if h > 0 and F(lat[h]["n_net"]) > 0}
    est = {}
    for p in o["pairs"]:
        r, d = int(p["gpm"]), int(p["dst"])
        h = hops(r, d)
        if h and F(p["mean"]) > 0:
            est[(r, d)] = F(p["mean"]) / rtt.get(h, rtt[max(rtt)])
    row = {t: F(o["gpm"][t].get("rdma_observed_remote_reads", 0)) for t in GPMS}
    inc = {t: F(o["gpm"][t].get("incoming_trans_count", 0)) for t in GPMS}
    scale = sum(row.values()) / (sum(inc.values()) or 1)
    col = {t: inc[t] * scale for t in GPMS}
    m = dict(est)
    for _ in range(iters):
        for axis, target in ((0, row), (1, col)):
            s = {}
            for k, v in m.items():
                s[k[axis]] = s.get(k[axis], 0) + v
            for k in m:
                if s[k[axis]] > 0:
                    m[k] *= target[k[axis]] / s[k[axis]]
    a0 = lat[0]
    owner_ns = F(a0["rtt_mean"]) - F(a0["fwd_mean"]) - F(a0["ret_net_mean"]) - (F(a0["owner_queue_mean"]) - 10.0)
    return dict(counts=m, end_ns=F(o["summary"]["end_ns"]), owner_ns=owner_ns, lat=lat,
                links=o["links"], occupancy=o["occupancy"], total_time=o["total_time"])


def yx_links(s, d):
    """Directed router links (tile, tile) on the Y-then-X route s -> d."""
    x, y = s % W, s // W
    dx, dy = d % W, d // W
    out = []
    while y != dy:
        ny = y + (1 if dy > y else -1)
        out.append((x + W * y, x + W * ny)); y = ny
    while x != dx:
        nx = x + (1 if dx > x else -1)
        out.append((x + W * y, nx + W * y)); x = nx
    return out


def link_flits(counts, req_flits=1, rsp_flits=6):
    fl = {}
    for (r, o), n in counts.items():
        for l in yx_links(r, o):
            fl[l] = fl.get(l, 0) + n * req_flits
        for l in yx_links(o, r):
            fl[l] = fl.get(l, 0) + n * rsp_flits
    return fl


if __name__ == "__main__":
    # check: predicted per-link flits vs akita metrics_obs_links (reads only)
    import csv
    w = csv.writer(open(os.path.join(HERE, "..", "traffic_link_check.csv"), "w"))
    w.writerow(["scenario", "workload", "akita_flits_total", "pred_flits_total", "ratio_total",
                "corr", "max_link_akita", "max_link_pred", "akita_max_link_flits", "pred_max_link_flits", "owner_ns"])
    for sc in ("w64b384", "w256b48m", "w2048b48"):
        for wl in load.SIX:
            if (sc, wl, "baseline") not in runs():
                continue
            m = matrix(sc, wl)
            pred = link_flits(m["counts"])
            ak = {load.link_tiles(r): F(r["flits"]) for r in m["links"]}
            keys = sorted(ak)
            xs = [ak[k] for k in keys]; ys = [pred.get(k, 0) for k in keys]
            mx, my = sum(xs) / len(xs), sum(ys) / len(ys)
            cov = sum((a - mx) * (b - my) for a, b in zip(xs, ys))
            corr = cov / ((sum((a - mx) ** 2 for a in xs) * sum((b - my) ** 2 for b in ys)) ** 0.5 or 1)
            ka = max(ak, key=ak.get); kp = max(pred, key=pred.get)
            w.writerow([sc, wl, f"{sum(xs):.0f}", f"{sum(ys):.0f}", f"{sum(ys)/sum(xs):.3f}", f"{corr:.4f}",
                        f"{ka[0]}->{ka[1]}", f"{kp[0]}->{kp[1]}", f"{ak[ka]:.0f}", f"{pred[kp]:.0f}", f"{m['owner_ns']:.1f}"])
    print(open(os.path.join(HERE, "..", "traffic_link_check.csv")).read())
