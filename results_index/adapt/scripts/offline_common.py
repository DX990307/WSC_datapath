#!/usr/bin/env python3
"""Shared helpers for the P21 offline analyses (offline_*.py).

Data layout (yss2 ~/akita-exp-test/adapt/analysis/offline/):
  data/<w>/alloc                    vpn ppn dev block  (one 4 KiB page per row)
  data/<w>/agg/metrics_pgcls_*      W=128 baseline exact per-page aggregation (-report-pgcls)
  data/<w>/tr/metrics_pgcls_trace.csv.gz   W=128 v4m3 time-ordered L1V trace (op R/W rows used)
  cls/<w>_pages.csv.gz              pgcls_page.py classes on the baseline aggregation
"""
import csv, gzip, os, collections

ROOT = os.path.expanduser("~/akita-exp-test/adapt/analysis/offline")
W = 7
CENTER = 24
TX = [t % W for t in range(W * W)]
TY = [t // W for t in range(W * W)]
WORKLOADS = ["km-g1", "fft2d-n1024", "fw-n2048-p4", "moe-t64", "mt-g4", "pa-b14s7p32", "pr-g24",
             "tc-s12e16", "mm-g12", "sssp-s18e4-r1", "ivf-n8k-q328", "cfd-c172k-b128", "mg-W",
             "embbag-b512", "bh-n86k", "atax-2688x8192-r64"]
HOT = "热点广播"
M64 = (1 << 64) - 1


def hops(a, b):
    return abs(TX[a] - TX[b]) + abs(TY[a] - TY[b])


def lat(a, b):
    """remote read latency model (ns); local = 0"""
    return 0 if a == b else 198 + 72 * hops(a, b)


def dev2tile(d):
    return d - 1 if d - 1 < 24 else d


def tile2dev(t):
    return t + 1 if t < 24 else t


def pghash(x):
    x = (x + 0x9e3779b97f4a7c15) & M64
    x = ((x ^ (x >> 30)) * 0xbf58476d1ce4e5b9) & M64
    x = ((x ^ (x >> 27)) * 0x94d049bb133111eb) & M64
    return x ^ (x >> 31)


def load_alloc(w):
    """returns dict ppn -> (vpn, dev, block, idx_in_block, block_npages) and dict block -> [ppn by vpn]"""
    rows = []
    with open(os.path.join(ROOT, "data", w, "alloc")) as f:
        next(f)
        for line in f:
            a = line.split()
            if len(a) < 4:
                continue
            rows.append((int(a[0]), int(a[1]), int(a[2]), int(a[3])))
    blocks = collections.defaultdict(list)
    for vpn, ppn, dev, blk in rows:
        blocks[blk].append((vpn, ppn))
    info = {}
    bl = {}
    for blk, lst in blocks.items():
        lst.sort()
        bl[blk] = [p for _, p in lst]
        n = len(lst)
        for i, (vpn, ppn) in enumerate(lst):
            info[ppn] = [vpn, None, blk, i, n]
    for vpn, ppn, dev, blk in rows:
        info[ppn][1] = dev
    return info, bl


def load_cls(w):
    d = {}
    with gzip.open(os.path.join(ROOT, "cls", w + "_pages.csv.gz"), "rt") as f:
        for r in csv.DictReader(f):
            d[int(r["page"])] = r
    return d


def trace_rows(w, with_gk=False):
    """yields (t_ns, req, own, page, off, op[, gk]) for op in R/W, in file order (NOT time-sorted)"""
    with gzip.open(os.path.join(ROOT, "data", w, "tr", "metrics_pgcls_trace.csv.gz"), "rt") as f:
        f.readline()
        for line in f:
            a = line.split(",")
            op = a[8]
            if op != "R" and op != "W":
                continue
            if with_gk:
                yield float(a[0]), int(a[3]), int(a[4]), int(a[5]), int(a[6]), op, int(a[1])
            else:
                yield float(a[0]), int(a[3]), int(a[4]), int(a[5]), int(a[6]), op


def trace_sample(w):
    with open(os.path.join(ROOT, "data", w, "tr", "metrics_pgcls_totals.csv")) as f:
        for r in csv.DictReader(f):
            if r["key"] == "raw_sample_every":
                return int(r["value"])
    return 1


def readers(w):
    """yields rows of the baseline _readers.csv.gz (dict)"""
    with gzip.open(os.path.join(ROOT, "data", w, "agg", "metrics_pgcls_readers.csv.gz"), "rt") as f:
        for r in csv.DictReader(f):
            yield r


def pages_agg(w):
    d = {}
    with gzip.open(os.path.join(ROOT, "data", w, "agg", "metrics_pgcls_pages.csv.gz"), "rt") as f:
        for r in csv.DictReader(f):
            d[int(r["page"])] = r
    return d


def load_trace_np(w):
    """time-sorted numpy arrays of the op R/W rows: t, req, own, page, line, isR, gk (cached .npz)"""
    import numpy as np
    cache = os.path.join(ROOT, "npz", w + ".npz")
    if os.path.exists(cache):
        z = np.load(cache)
        if "gk" in z.files:
            return {k: z[k] for k in z.files}
    t, rq, ow, pg, ln, rr, gk = [], [], [], [], [], [], []
    for a in trace_rows(w, with_gk=True):
        t.append(a[0]); rq.append(a[1]); ow.append(a[2]); pg.append(a[3]); ln.append(a[4] >> 6)
        rr.append(a[5] == "R"); gk.append(a[6])
    t = np.array(t, dtype=np.float64)
    o = np.argsort(t, kind="stable")
    d = dict(t=t[o], req=np.array(rq, dtype=np.int16)[o], own=np.array(ow, dtype=np.int16)[o],
             page=np.array(pg, dtype=np.int64)[o], line=np.array(ln, dtype=np.int16)[o],
             isR=np.array(rr, dtype=bool)[o], gk=np.array(gk, dtype=np.int32)[o])
    os.makedirs(os.path.dirname(cache), exist_ok=True)
    np.savez(cache, **d)
    return d


def by_page(d, mask=None):
    """yields (page, idx array into d, in time order) for rows selected by mask"""
    import numpy as np
    idx = np.arange(len(d["t"])) if mask is None else np.nonzero(mask)[0]
    pg = d["page"][idx]
    o = np.argsort(pg, kind="stable")
    idx = idx[o]
    pg = pg[o]
    if len(pg) == 0:
        return
    cut = np.nonzero(np.diff(pg))[0] + 1
    starts = np.concatenate([[0], cut])
    ends = np.concatenate([cut, [len(pg)]])
    for s, e in zip(starts, ends):
        yield int(pg[s]), idx[s:e]


def load_wg(w):
    """per global kernel (1-based, launch order): (total WGs, WGs per CU) from the baseline stdout"""
    out = []
    with open(os.path.join(ROOT, "data", w, "wg.txt")) as f:
        for line in f:
            a = line.split()
            out.append((int(a[2]), int(a[6])))
    return out


CU_PER_GPM = 28
