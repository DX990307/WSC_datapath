#!/usr/bin/env python3
"""P21 offline H: remaining remote reads after "M3+ only" and after "first touch + M3+" -- how
many land on data that is read-only within the current kernel (regional replication target)?

usage: offline_ro.py <workload>  -> JSON line on stdout

The exact aggregation has no per-kernel write information, so this uses the W=128 v4m3 L1V
trace (op R/W rows, time order, global kernel gk; 1/4 page-hash sample in non-hot workloads:
the sampled pages are complete, results are over those pages).
Retention (M3+, as bound.md / pgcls R_retain): a GPM re-reading a line it read before does not
go to the network unless the line was written in between (by any GPM; same or earlier kernel).
Home: base = owner; ft = first toucher of the page (smallest first_ns in the baseline exact
aggregation, reads or writes). A read is remote if reader != home and not retained.
For each remaining remote read, at 1 KB block (and line) granularity within its kernel:
  ro        no GPM wrote the block (line) in that kernel
  readers   distinct GPMs that read the block in that kernel: 1 / 2-3 / 4-15 / >= 16
  hops      reader -> home (mean, p50, p90)
Fractions are of the remaining remote reads; "rem" = remaining remote reads / remote reads of
the baseline (no retention, owner home) on the same pages.
"""
import sys, json, collections
import numpy as np
from offline_common import *

BUCKETS = [(1, 1, "1"), (2, 3, "2-3"), (4, 15, "4-15"), (16, 99, ">=16")]


def main(w):
    info, bl = load_alloc(w)
    first = {}
    for r in readers(w):
        rd = int(r["reads"]); wr = int(r["writes"])
        if not (rd or wr) or not r["first_ns"]:
            continue
        p = int(r["page"]); g = int(r["reader"]); fn = float(r["first_ns"])
        if p not in first or (fn, g) < first[p]:
            first[p] = (fn, g)
    d = load_trace_np(w)
    t, req, own, page, line, isR, gk = d["t"], d["req"], d["own"], d["page"], d["line"], d["isR"], d["gk"]
    n = len(t)
    blk = page * 4 + (line.astype(np.int64) >> 4)
    lkey = page * 64 + line.astype(np.int64)
    # written-in-kernel sets and readers per (block, kernel)
    wm = ~isR
    wblk = set(zip(blk[wm].tolist(), gk[wm].tolist()))
    wline = set(zip(lkey[wm].tolist(), gk[wm].tolist()))
    rdrs = collections.defaultdict(set)
    for b, k, g in zip(blk[isR].tolist(), gk[isR].tolist(), req[isR].tolist()):
        rdrs[(b, k)].add(g)
    ftile = np.array([first.get(int(p), (0, int(o)))[1] for p, o in zip(page.tolist(), own.tolist())], dtype=np.int16)
    # retention state per line: set of GPMs holding a valid copy
    valid = collections.defaultdict(set)
    acc = {h: collections.Counter() for h in ("base", "ft")}
    hopsl = {h: [] for h in ("base", "ft")}
    base_remote = 0
    R = isR.tolist(); G = req.tolist(); O = own.tolist(); F = ftile.tolist(); L = lkey.tolist()
    B = blk.tolist(); K = gk.tolist()
    for i in range(n):
        g = G[i]; key = L[i]
        if not R[i]:
            valid[key].clear()      # a write invalidates every retained copy (writes are not retained)
            continue
        v = valid[key]
        retained = g in v
        v.add(g)
        if g != O[i]:
            base_remote += 1
        if retained:
            continue
        for h, home in (("base", O[i]), ("ft", F[i])):
            if g == home:
                continue
            a = acc[h]
            a["rem"] += 1
            ro = (B[i], K[i]) not in wblk
            if (L[i], K[i]) not in wline:
                a["ro_line"] += 1
            if ro:
                a["ro"] += 1
                nr = len(rdrs[(B[i], K[i])])
                for lo, hi, nm in BUCKETS:
                    if lo <= nr <= hi:
                        a["rd_" + nm] += 1
                hopsl[h].append(hops(g, home))
    out = dict(w=w, sample=trace_sample(w), trace_remote_reads=base_remote)
    for h in ("base", "ft"):
        a = acc[h]
        rem = max(1, a["rem"])
        hp = np.array(hopsl[h]) if hopsl[h] else np.array([0])
        out[h] = dict(rem=a["rem"] / max(1, base_remote), ro=a["ro"] / rem, ro_line=a["ro_line"] / rem,
                      ro_of_baseline=a["ro"] / max(1, base_remote),
                      readers={nm: a["rd_" + nm] / max(1, a["ro"]) for _, _, nm in BUCKETS},
                      hops_mean=float(hp.mean()), hops_p50=float(np.percentile(hp, 50)),
                      hops_p90=float(np.percentile(hp, 90)))
    print(json.dumps(out, ensure_ascii=False))


if __name__ == "__main__":
    main(sys.argv[1])
