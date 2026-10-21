"""Complete-config traffic for the P18 network-level mechanism study (option A).

From the akita Complete run of a workload (dm/raw):
  - per requester: logical remote reads, network packets by batch size
    k = 1..8 lines (remote_batch_size_0k_packets), writes;
  - destination shape: pairs in-flight / RTT(hops) (as for baseline);
  - owner service delay from the latency table.
A packet of k lines is a 16 B request (k = 1) or 32 B bitmap request, and
a 96 B (k = 1) or 16 * ceil((4 + 64k) * 1.25 / 16) B response.
M3 hits and requester merges never enter the network (they are inside
logical / packets). Owner merge + in-mesh multicast is NOT modelled: every
packet gets its own unicast response (conservative; akita merges <= 7% of
owner reads into multicasts).
"""
import math, os, sys
HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import traffic as T  # noqa: E402
F, G = T.F, T.GPMS


def rsp_bytes(k):
    return 96 if k == 1 else 16 * math.ceil((4 + 64 * k) * 1.25 / 16)


def req_bytes(k):
    return 16 if k == 1 else 32


def shape(sc, wl):
    o = T.runs()[(sc, wl, "Complete")]
    lat = {(0 if r["hops"] == "all" else int(r["hops"])): r for r in o["latency"]}
    rtt = {h: F(lat[h]["rtt_mean"]) for h in lat if h > 0 and F(lat[h]["n_net"]) > 0}
    est = {}
    for p in o["pairs"]:
        r, d = int(p["gpm"]), int(p["dst"])
        h = T.hops(r, d)
        if h and F(p["mean"]) > 0:
            est[(r, d)] = F(p["mean"]) / rtt.get(h, rtt[max(rtt)])
    hist, logical, writes = {}, {}, {}
    for t in G:
        g = o["gpm"][t]
        hist[t] = {k: F(g.get(f"remote_batch_size_{k:02d}_packets", 0)) for k in range(1, 9)}
        logical[t] = F(g.get("remote_logical_reads", 0))
        writes[t] = F(g.get("rdma_observed_remote_writes", 0))
    pk = {t: sum(hist[t].values()) for t in G}
    # IPF in packets: rows = requester packets, columns = owner-served lines
    # (t18_owner_reads) converted to packets with the requesters' mean batch size
    lines = {t: sum(k * n for k, n in hist[t].items()) for t in G}
    kbar = sum(lines.values()) / (sum(pk.values()) or 1)
    col = {t: F(o["gpm"][t].get("t18_owner_reads", 0)) / kbar for t in G}
    cs = sum(col.values())
    col = {t: v * sum(pk.values()) / cs for t, v in col.items()} if cs else None
    counts = {k: v for k, v in est.items() if pk.get(k[0], 0) > 0}
    for _ in range(50 if col else 1):
        for axis, target in ((0, pk), (1, col)):
            if target is None:
                continue
            sm = {}
            for k, v in counts.items():
                sm[k[axis]] = sm.get(k[axis], 0) + v
            for k in counts:
                if sm[k[axis]] > 0:
                    counts[k] *= target[k[axis]] / sm[k[axis]]
    a0 = lat[0]
    owner_ns = F(a0["rtt_mean"]) - F(a0["fwd_mean"]) - F(a0["ret_net_mean"]) - (F(a0["owner_queue_mean"]) - 10.0)
    return dict(counts=counts, hist=hist, logical=logical, packets=pk, writes=writes, owner_ns=owner_ns,
                links=o["links"], end_ns=F(o["summary"]["end_ns"]))


def link_flits(s):
    """Predicted per-link flits (reads by batch size + writes) for a shape."""
    fl = {}
    for (r, d), n in s["counts"].items():
        h = s["hist"][r]; tot = sum(h.values())
        rq = sum(h[k] * req_bytes(k) / 16 for k in h) / tot
        rp = sum(h[k] * rsp_bytes(k) / 16 for k in h) / tot
        wr = s["writes"][r] * n / s["packets"][r]
        for l in T.yx_links(r, d):
            fl[l] = fl.get(l, 0) + n * rq + wr * 6
        for l in T.yx_links(d, r):
            fl[l] = fl.get(l, 0) + n * rp + wr * 1
    return fl


if __name__ == "__main__":
    import csv
    w = csv.writer(open(os.path.join(HERE, "..", "traffic_link_check_complete.csv"), "w"))
    w.writerow(["scenario", "workload", "akita_flits_total", "pred_flits_total", "ratio_total", "corr",
                "max_link_akita", "max_link_pred", "logical_per_packet", "owner_ns"])
    for sc in ("w64b384", "w256b48m", "w2048b48"):
        for wl in T.load.SIX:
            s = shape(sc, wl)
            pred = link_flits(s)
            ak = {T.load.link_tiles(r): F(r["flits"]) for r in s["links"]}
            keys = sorted(ak)
            xs = [ak[k] for k in keys]; ys = [pred.get(k, 0) for k in keys]
            mx, my = sum(xs) / len(xs), sum(ys) / len(ys)
            cov = sum((a - mx) * (b - my) for a, b in zip(xs, ys))
            corr = cov / ((sum((a - mx) ** 2 for a in xs) * sum((b - my) ** 2 for b in ys)) ** 0.5 or 1)
            ka = max(ak, key=ak.get); kp = max(pred, key=pred.get)
            lpp = sum(s["logical"].values()) / sum(s["packets"].values())
            w.writerow([sc, wl, f"{sum(xs):.0f}", f"{sum(ys):.0f}", f"{sum(ys)/sum(xs):.3f}", f"{corr:.4f}",
                        f"{ka[0]}->{ka[1]}", f"{kp[0]}->{kp[1]}", f"{lpp:.2f}", f"{s['owner_ns']:.1f}"])
    print(open(os.path.join(HERE, "..", "traffic_link_check_complete.csv")).read())
