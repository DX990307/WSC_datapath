"""Summarise P18 ASTRA-sim runs (run on the server next to runs/).
Writes runs.csv (one row per run) and hops.csv (per run x hop count).
Times in ns (1 tick = 1/24 ns). Links: router-router links are device ids
>= 49 at both ends; tile = id - 49. Endpoint links: npu <-> its router."""
import csv, glob, gzip, json, os, statistics as st, sys
TPN, NPU, W7 = 24.0, 49, 7
HOT = (0, 7)  # Y-first corner link 0 -> 7


def hops(a, b):
    return abs(a % W7 - b % W7) + abs(a // W7 - b // W7)


def pct(xs, p):
    if not xs:
        return 0.0
    xs = sorted(xs)
    return xs[min(len(xs) - 1, int(p * len(xs)))]


def one(d):
    meta = json.load(open(d + "/meta.json"))
    meta_out = {k: v for k, v in meta.items() if k != "logical_factor"}
    rc = open(d + "/done").read().strip()
    if rc != "0" or not os.path.exists(d + "/links.csv"):
        return dict(meta_out, run=os.path.basename(d), rc=rc), []
    req, rsp = {}, {}
    for r in csv.DictReader(gzip.open(d + "/msgs.csv.gz", "rt")):
        key = int(r["tag"])
        rec = (int(r["src"]), int(r["dst"]), int(r["bytes"]), int(r["send_time"]), int(r["arrival_time"]))
        (rsp if key in req else req)[key] = rec
    reads = []  # (requester, owner, is_write, t_send, fwd, ret, rtt, t_done)
    for k, (s, o, b, ts, ta) in req.items():
        if k not in rsp:
            continue
        _, _, _, us, ua = rsp[k]
        reads.append((s, o, b == 96, ts / TPN, (ta - ts) / TPN, (ua - us) / TPN, (ua - ts) / TPN, ua / TPN))
    end = max(x[7] for x in reads)
    if meta.get("open"):
        t0, t1 = 0.0, meta["dur_ns"]
        offered = len(req)
    else:
        last = {}
        for x in reads:
            last[x[0]] = max(last.get(x[0], 0), x[7])
        t1 = min(last.values())
        t0 = 0.1 * t1
    win = [x for x in reads if t0 <= x[7] < t1]
    span = t1 - t0
    per_gpm = {}
    for x in win:
        per_gpm[x[0]] = per_gpm.get(x[0], 0) + 1
    thr = [n / span for n in per_gpm.values()]  # reads (or writes) per ns per GPM
    # logical reads per ns: read packets x logical reads per packet (E3), 1 otherwise
    lf = meta.get("logical_factor", {})
    logical = sum(lf.get(str(x[0]), 1.0) for x in win if not x[2]) / span
    rtts = [x[6] for x in win]
    links = list(csv.DictReader(open(d + "/links.csv")))
    lend = float(links[0]["end_time"]) / TPN
    bpt = {"b384": 16, "b48": 2}[meta["bw"]]
    rr, hot, ej0, inj0 = [], None, None, None
    for l in links:
        a, b = int(l["src"]), int(l["dst"])
        busy = float(l["busy"]) / TPN
        u = busy / (span if meta.get("open") else lend)
        mw = float(l["max_window_busy"]) / 24000
        if a >= NPU and b >= NPU:
            rr.append((u, mw, a - NPU, b - NPU, float(l["wait"]) / TPN / max(1, float(l["chunks"]))))
            if (a - NPU, b - NPU) == HOT:
                hot = (u, mw)
        if a == NPU + 0 and b == 0:
            ej0 = (u, mw)
        if a == 0 and b == NPU + 0:
            inj0 = (u, mw)
    us = [x[0] for x in rr]
    top = max(rr)
    row = dict(meta_out, run=os.path.basename(d), rc=rc, n_req=len(req), n_done=len(reads), end_ns=end,
               window_ns=span, gpms=len(per_gpm),
               thr_gpm_mean=st.mean(thr) if thr else 0, thr_gpm_min=min(thr) if thr else 0,
               thr_gpm_max=max(thr) if thr else 0, thr_total=sum(thr), logical_reads_per_ns=logical,
               rtt_mean=st.mean(rtts) if rtts else 0, rtt_p50=pct(rtts, .5), rtt_p95=pct(rtts, .95),
               rtt_p99=pct(rtts, .99),
               fwd_mean=st.mean(x[4] for x in win) if win else 0, ret_mean=st.mean(x[5] for x in win) if win else 0,
               link_util_mean=st.mean(us), link_util_p99=pct(us, .99), link_util_max=top[0],
               link_max_at=f"{top[2]}->{top[3]}", link_window_max=max(x[1] for x in rr),
               hot07_util=hot[0], hot07_window=hot[1], eject0_util=ej0[0], eject0_window=ej0[1], inject0_util=inj0[0], inject0_window=inj0[1],
               link_wait_mean_ns=st.mean(x[4] for x in rr))
    if not meta.get("open"):
        row["little_W_over_rtt"] = meta["W"] / row["rtt_mean"] if row["rtt_mean"] else 0
    hrows = []
    byh = {}
    for x in win:
        byh.setdefault(hops(x[0], x[1]), []).append(x)
    for h, xs in sorted(byh.items()):
        f = [x[4] for x in xs]; r = [x[5] for x in xs]; t = [x[6] for x in xs]
        hrows.append(dict(run=row["run"], hops=h, n=len(xs), fwd_mean=st.mean(f), fwd_p95=pct(f, .95),
                          ret_mean=st.mean(r), ret_p95=pct(r, .95), rtt_mean=st.mean(t), rtt_p95=pct(t, .95)))
    return row, hrows


def main(root):
    from multiprocessing import Pool
    dirs = sorted(os.path.dirname(d) for d in glob.glob(root + "/runs/*/done"))
    with Pool(int(os.environ.get("JOBS", "8"))) as pool:
        res = pool.map(one, dirs)
    rows, hrows = [], []
    for r, h in res:
        rows.append(r); hrows += h
    keys = []
    for r in rows:
        keys += [k for k in r if k not in keys]
    with open(root + "/runs.csv", "w") as f:
        w = csv.DictWriter(f, keys); w.writeheader(); w.writerows(rows)
    if hrows:
        with open(root + "/hops.csv", "w") as f:
            w = csv.DictWriter(f, list(hrows[0])); w.writeheader(); w.writerows(hrows)
    print(len(rows), "runs")


if __name__ == "__main__":
    main(sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/astrasim-test/p18"))
