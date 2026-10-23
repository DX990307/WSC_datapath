#!/usr/bin/env python3
"""P21 offline D: benefit/cost migration threshold (ski rental) vs the fixed 256 counter.

usage: offline_cost.py <workload>  -> CSV rows on stdout (no header)

Trace: W=128 v4m3 L1V trace (op R/W rows, time order) as a proxy of the baseline access stream.
Unit = 4 KB page or 1 KB block (64 / 16 lines). Counted access = remote access (req != owner,
R or W). Latency model: lat(a, b) = 198 + 72 * hops ns, local 0.
alpha policy: every 8 counted accesses, with c_g = all accesses seen so far by GPM g (owner's
  local ones included), dst = argmax c_g if its share >= 0.6 and dst != owner;
  benefit = sum_g c_g * (lat(g, owner) - lat(g, dst));  cost = 2000 + L * lat(owner, dst) * w;
  migrate when benefit > alpha * cost (once; the unit then stays).
fixed256: migrate to the first GPM whose remote count reaches 256.
Per policy: migrated units, their share of remote reads, reads before the decision / their reads,
  streaming units among them (pgcls 流式 page class; and "each line read < 2 times by dst"),
  hot units (热点广播), realised net saving = latency saved after the decision - cost (w = 1),
  relative to the total remote read+write latency of the workload, and the share of migrations
  with negative realised net.
"""
import sys, csv, collections
import numpy as np
from offline_common import *

ALPHAS = [0.25, 0.5, 1, 2, 4]
WS = [1.0, 0.5]
EVERY = 8
SHARE = 0.6
FIX = 256
LAT = np.array([[lat(a, b) for b in range(49)] for a in range(49)], dtype=np.float64)


def run(w, gran):
    cls = load_cls(w)
    d = load_trace_np(w)
    t, req, own, isR, line = d["t"], d["req"], d["own"], d["isR"], d["line"]
    L = 64 if gran == "page" else 16
    if gran == "page":
        key = d["page"]
    else:
        key = d["page"] * 4 + (line.astype(np.int64) >> 4)
    o = np.argsort(key, kind="stable")
    ks = key[o]
    cut = np.nonzero(np.diff(ks))[0] + 1
    starts = np.concatenate([[0], cut]); ends = np.concatenate([cut, [len(ks)]])
    pol = [("a", wv, a) for wv in WS for a in ALPHAS] + [("fixed256", 1.0, 0)]
    A = {p: collections.Counter() for p in pol}
    tot_rr = 0
    tot_lat = 0.0
    for s0, e0 in zip(starts, ends):
        ix = o[s0:e0]
        g = req[ix].astype(np.int64)
        ow = int(own[ix[0]])
        r = isR[ix]
        rem = g != ow
        rr_mask = r & rem
        nrr = int(rr_mask.sum())
        tot_rr += nrr
        latv = LAT[g, ow]
        tot_lat += float(latv[rem].sum())
        if rem.sum() < EVERY:
            continue
        pg = int(ks[s0]) if gran == "page" else int(ks[s0]) >> 2
        c = cls[pg]["cls"] if pg in cls else "无远程读"
        crem = np.cumsum(rem)
        cum_rr = np.cumsum(rr_mask)
        # decision points
        dec = {}
        evals = np.nonzero(rem & (crem % EVERY == 0))[0]
        cnt = np.zeros(49)
        prev = 0
        pending = [p for p in pol if p[0] == "a"]
        for pos in evals:
            cnt += np.bincount(g[prev:pos + 1], minlength=49)
            prev = pos + 1
            tot = cnt.sum()
            dst = int(np.argmax(cnt))
            if dst == ow or cnt[dst] < SHARE * tot:
                continue
            ben = float((cnt * (LAT[:, ow] - LAT[:, dst])).sum())
            left = []
            for p in pending:
                cost = 2000.0 + L * LAT[ow, dst] * p[1]
                if ben > p[2] * cost:
                    dec[p] = (pos, dst)
                else:
                    left.append(p)
            pending = left
            if not pending:
                break
        # fixed 256
        best = None
        for gg in np.unique(g[rem]):
            pos = np.nonzero(g == gg)[0]
            if len(pos) >= FIX and (best is None or pos[FIX - 1] < best[0]):
                best = (int(pos[FIX - 1]), int(gg))
        if best:
            dec[("fixed256", 1.0, 0)] = best
        for p, (pos, dst) in dec.items():
            a = A[p]
            a["units"] += 1
            a["rr"] += nrr
            a["before"] += int(cum_rr[pos])
            aft = slice(pos + 1, None)
            saved = float((LAT[g[aft], ow] - LAT[g[aft], dst]).sum())
            net = saved - (2000.0 + L * LAT[ow, dst])
            a["net"] += net
            a["neg"] += 1 if net < 0 else 0
            if c == "流式":
                a["stream_units"] += 1; a["stream_rr"] += nrr
            if c == HOT:
                a["hot_units"] += 1; a["hot_rr"] += nrr
            # direct streaming test: dst reads each touched line < 2 times
            dm = r & (g == dst)
            nl = len(np.unique(line[ix][dm]))
            if nl and dm.sum() / nl < 2:
                a["s1_units"] += 1; a["s1_rr"] += nrr
    rows = []
    for p in pol:
        a = A[p]
        rows.append([w, gran, p[0], p[1], p[2], a["units"], round(a["rr"] / max(1, tot_rr), 4),
                     round(a["before"] / a["rr"], 4) if a["rr"] else "",
                     a["stream_units"], round(a["stream_rr"] / max(1, tot_rr), 4),
                     a["s1_units"], round(a["s1_rr"] / max(1, tot_rr), 4),
                     a["hot_units"], round(a["hot_rr"] / max(1, tot_rr), 4),
                     round(a["net"] / max(1.0, tot_lat), 4), round(a["neg"] / a["units"], 3) if a["units"] else "",
                     tot_rr])
    return rows


def main(w):
    wr = csv.writer(sys.stdout)
    for gran in ("page", "blk1k"):
        for r in run(w, gran):
            wr.writerow(r)


if __name__ == "__main__":
    main(sys.argv[1])
