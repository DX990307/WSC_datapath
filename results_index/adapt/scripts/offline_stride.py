#!/usr/bin/env python3
"""P21 offline E: do buffers of one kernel share the same linear-split "stride", and does the
page -> main-reader rule of a buffer survive across kernels?

usage: offline_stride.py <workload>  -> JSON line on stdout

Page target (whole run, baseline exact aggregation): reader with >= 0.6 of the page's L1V reads
(owner's local reads included), else none. Here the full target map is used (pages whose main
reader is the owner included): unit weight = the page's reads.
Linear rule (offline_pattern.fit_lin, refined): d = (c + floor(i * sigma + phi)) mod 48, i = page
index in the buffer, d = device - 1. sigma = devices per page; n = 1 / sigma pages per device.
Driver WG distribution (mgpusim driver.distributeWGToGPUs): each GPM gets 28 * wgPerCU
consecutive WGs, so a buffer split n pages per device moves  n * 4096 / (28 * wgPerCU)  bytes per
WG (wgPerCU of the first kernel that reads the buffer, from the baseline stdout).
Pairs (A, B) of "regular" buffers (>= 16 target pages, own lin hit >= 0.5) whose kernel ranges
overlap (first_k..last_k of their pages):
  ratio      sigma_B / sigma_A, nearest power of two and relative error
  hit_full   A's (sigma, phi, c) applied to B's page index
  hit_sigma  A's sigma, B's best phase / offset (= learn only the offset in B)
  hit_pow2   A's sigma x best power of two (1/8..8), B's best phase / offset (element size)
  hit_own    B's own fit
Across kernels (trace, op R rows, per global kernel; 1/4 page sample in non-hot workloads):
per buffer and kernel the page targets (share >= 0.6, owner included); for consecutive kernels
that both read the buffer: agreement of page targets (pages with a target in both, weighted by
the later kernel's reads), and the first kernel's fitted lin rule applied to the later kernel.
"""
import sys, json, collections, itertools
import numpy as np
from offline_common import *
from offline_pattern import fit_lin, pred_lin, wmode

SHARE = 0.6
MINP = 16


def hit_sigma(I, D, Wt, s, nph=64):
    """best phase / offset for a fixed sigma: returns (hit fraction, phi, c)"""
    v = I * s
    fl = np.floor(v)
    fr = v - fl
    best = (-1, 0, 0)
    for ph in np.arange(nph) / float(nph):
        pred = (fl + (fr >= 1 - ph - 1e-12)).astype(np.int64)
        c = wmode(D - pred, Wt)
        h = Wt[((pred + c) % 48) == D].sum()
        if h > best[0]:
            best = (h, ph, c)
    return best[0] / Wt.sum(), best[1], best[2]


def hit_params(I, D, Wt, p):
    pr = np.array([pred_lin(p, i) for i in I])
    return Wt[pr == D].sum() / Wt.sum()


def targets_from_counts(cnt):
    tot = sum(cnt.values())
    g, v = max(cnt.items(), key=lambda x: (x[1], -x[0]))
    return (tile2dev(g) - 1 if v >= SHARE * tot else -1), tot


def main(w):
    info, bl = load_alloc(w)
    wg = load_wg(w)
    per = collections.defaultdict(collections.Counter)
    krange = {}
    for r in readers(w):
        p = int(r["page"])
        rd = int(r["reads"])
        if rd:
            per[p][int(r["reader"])] += rd
            fk, lk = int(r["first_k"]), int(r["last_k"])
            a = krange.get(p)
            krange[p] = (min(fk, a[0]), max(lk, a[1])) if a else (fk, lk)
    # ---------------- per-buffer fit (whole run)
    bufs = {}
    for blk, pl in bl.items():
        I, D, Wt, O = [], [], [], []
        ks = set()
        for i, p in enumerate(pl):
            if p not in per:
                continue
            t, tot = targets_from_counts(per[p])
            ks.update(range(krange[p][0], krange[p][1] + 1))
            if t < 0:
                continue
            I.append(i); D.append(t); Wt.append(tot); O.append(info[p][1] - 1)
        if len(I) < MINP:
            continue
        I = np.array(I, dtype=np.int64); D = np.array(D, dtype=np.int64); Wt = np.array(Wt, dtype=float)
        p = fit_lin(I, D, Wt, refine=True)
        if p is None:
            continue
        h = hit_params(I, D, Wt, p)
        mig = float(Wt[D != np.array(O)].sum() / Wt.sum())
        k0 = min(ks) if ks else 1
        wpc = wg[k0 - 1][1] if 0 < k0 <= len(wg) else None
        n = 1.0 / p[0] if abs(p[0]) > 1e-12 else None
        bufs[blk] = dict(blk=blk, N=len(pl), tgt_pages=len(I), mig_w=mig, sigma=p[0], n_pages_per_dev=n,
                         hit=h, kernels=[min(ks), max(ks)] if ks else None, first_k=k0, wg_per_cu=wpc,
                         bytes_per_wg=(n * 4096 / (CU_PER_GPM * wpc)) if (n and wpc) else None,
                         _I=I, _D=D, _W=Wt, _p=p)
    reg = [b for b in bufs.values() if b["hit"] >= 0.5]
    pairs = []
    for A, B in itertools.permutations(reg, 2):
        ka, kb = A["kernels"], B["kernels"]
        if not ka or not kb or ka[1] < kb[0] or kb[1] < ka[0]:
            continue
        sA, sB = A["sigma"], B["sigma"]
        ratio = sB / sA if abs(sA) > 1e-12 else None
        p2 = 2.0 ** round(np.log2(abs(ratio))) * np.sign(ratio) if ratio else None
        hs = hit_sigma(B["_I"], B["_D"], B["_W"], sA)[0]
        best2 = max((hit_sigma(B["_I"], B["_D"], B["_W"], sA * (2.0 ** e))[0], e) for e in range(-3, 4))
        pairs.append(dict(A=A["blk"], B=B["blk"], ratio=ratio, pow2=p2,
                          ratio_err=abs(ratio / p2 - 1) if p2 else None,
                          hit_full=hit_params(B["_I"], B["_D"], B["_W"], A["_p"]), hit_sigma=hs,
                          hit_pow2=best2[0], pow2_exp=best2[1], hit_own=B["hit"]))
    # ---------------- across kernels (trace)
    d = load_trace_np(w)
    m = d["isR"]
    pgs, gks, rqs = d["page"][m], d["gk"][m], d["req"][m]
    cnt = collections.defaultdict(collections.Counter)      # (page, gk) -> Counter(req)
    for p, k, g in zip(pgs.tolist(), gks.tolist(), rqs.tolist()):
        cnt[(p, k)][g] += 1
    bypk = collections.defaultdict(dict)                    # blk -> gk -> {i: (t, reads, own)}
    for (p, k), c in cnt.items():
        if p not in info:
            continue
        t, tot = targets_from_counts(c)
        blk, i = info[p][2], info[p][3]
        bypk[blk].setdefault(k, {})[i] = (t, tot, info[p][1] - 1)
    xk = collections.Counter()
    xb = []
    for blk, ks in bypk.items():
        kl = sorted(ks)
        if len(kl) < 2:
            continue
        first = None
        for k in kl:
            pts = {i: v for i, v in ks[k].items() if v[0] >= 0}
            if len(pts) >= MINP:
                I = np.array(sorted(pts)); D = np.array([pts[i][0] for i in I]); Wt = np.array([pts[i][1] for i in I], float)
                first = (k, fit_lin(I, D, Wt, refine=True))
                break
        agree_w = tot_w = 0.0
        rule_h = rule_w = 0.0
        for a, b in zip(kl, kl[1:]):
            A, B = ks[a], ks[b]
            for i, (t, tot, _) in B.items():
                if t >= 0 and i in A and A[i][0] >= 0:
                    tot_w += tot
                    agree_w += tot if A[i][0] == t else 0
            if first and b > first[0] and first[1]:
                for i, (t, tot, _) in B.items():
                    if t >= 0:
                        rule_w += tot
                        rule_h += tot if pred_lin(first[1], i) == t else 0
        if tot_w or rule_w:
            xb.append(dict(blk=blk, kernels=len(kl), agree=agree_w / tot_w if tot_w else None, agree_w=tot_w,
                           rule_first=rule_h / rule_w if rule_w else None, rule_w=rule_w,
                           first_k=first[0] if first else None))
            xk["agree"] += agree_w; xk["agree_n"] += tot_w; xk["rule"] += rule_h; xk["rule_n"] += rule_w
    for b in bufs.values():
        for k in ("_I", "_D", "_W", "_p"):
            b.pop(k)
    summ = dict(w=w, kernels=len(wg), regular_buffers=len(reg), pairs=len(pairs),
                pairs_same_sigma=sum(1 for q in pairs if q["ratio_err"] is not None and q["pow2"] == 1 and q["ratio_err"] < 0.02),
                pairs_pow2=sum(1 for q in pairs if q["ratio_err"] is not None and q["ratio_err"] < 0.02),
                hit_sigma_mean=(sum(q["hit_sigma"] for q in pairs) / len(pairs)) if pairs else None,
                hit_pow2_mean=(sum(q["hit_pow2"] for q in pairs) / len(pairs)) if pairs else None,
                hit_own_mean=(sum(q["hit_own"] for q in pairs) / len(pairs)) if pairs else None,
                hit_full_mean=(sum(q["hit_full"] for q in pairs) / len(pairs)) if pairs else None,
                xk_buffers=len(xb), xk_agree=xk["agree"] / xk["agree_n"] if xk["agree_n"] else None,
                xk_rule_first=xk["rule"] / xk["rule_n"] if xk["rule_n"] else None,
                trace_sample=trace_sample(w))
    print(json.dumps(dict(summary=summ, buffers=list(bufs.values()), pairs=pairs, xkernel=xb), ensure_ascii=False))


if __name__ == "__main__":
    main(sys.argv[1])
