#!/usr/bin/env python3
"""P21 offline G: how good is first-touch placement?

usage: offline_ft.py <workload>  -> JSON line on stdout

Units: 4 KB pages and 1 KB blocks. Data: W=128 baseline exact aggregation (_readers: per
(page, GPM) reads, 8 x 512 B sub-block reads, writes, first_ns, rep_intra / rep_cross).
First toucher (FT):
  page   GPM with the smallest first_ns over its reader rows (reads or writes; host copies are
         not GPM accesses and do not appear) -- exact per page.
  1 KB   approximation: smallest page-level first_ns among the GPMs that read this block or
         only wrote the page (writes are not split by sub-block in the aggregation). Checked
         against the exact block first toucher of the W=128 v4m3 trace on its sampled pages.
Main user = GPM with the most L1V reads of the unit (owner included; ties keep the owner).
Agreement FT == main, weighted by the unit's remote reads (reads by GPMs other than the owner).
Placements compared (remaining remote reads R and read-hops H / baseline, bound.md model, plus
"+M3+" retention: reader g keeps (reads - rep_intra - rep_cross) / reads):
  ft (first touch), main (each unit to its main user = P20 pg / sb oracle),
  rule (offline C rollback learner, m = 2: units placed by the confident rule at their first
  access, the others stay at the owner; no later migration counted).
FT errors (page granularity, FT != main), excess read-hops over the main-user placement / H0
and their remote reads / R0:
  hot        pgcls 热点广播 page (FT puts the whole page at its first reader)
  init       FT reads < 10 % of the page's reads (initialised / written by another GPM)
  noshare    no reader with >= 0.6 of the reads (shared page; any single home is a compromise)
  minor      FT is a minor reader of a page with a dominant main reader; for trace-sampled
             pages: share whose first kernel's main reader == FT (= main reader changes across
             kernels)
"""
import sys, json, gzip, os, collections
import numpy as np
from offline_common import *


def main(w):
    info, bl = load_alloc(w)
    cls = load_cls(w)
    P = collections.defaultdict(dict)   # page -> g -> dict
    for r in readers(w):
        p = int(r["page"])
        rd = int(r["reads"]); wr = int(r["writes"])
        if not (rd or wr):
            continue
        s = [int(x) for x in r["sub_reads"].split(";")]
        rep = int(r["rep_intra"]) + int(r["rep_cross"])
        P[p][int(r["reader"])] = dict(rd=rd, wr=wr, sub=[s[0] + s[1], s[2] + s[3], s[4] + s[5], s[6] + s[7]],
                                       keep=max(0.0, (rd - rep) / rd) if rd else 1.0,
                                       fn=float(r["first_ns"]) if r["first_ns"] else 1e30)
    # ---------------- trace: exact block first toucher, first-kernel main reader (sampled pages)
    d = load_trace_np(w)
    blk_ft = {}
    pg_ft_tr = {}
    k1cnt = {}
    pages_tr = d["page"]
    o = np.argsort(pages_tr * 4 + (d["line"].astype(np.int64) >> 4), kind="stable")
    key = (pages_tr * 4 + (d["line"].astype(np.int64) >> 4))[o]
    first_idx = np.concatenate([[0], np.nonzero(np.diff(key))[0] + 1])
    for i in first_idx:
        j = o[i]
        blk_ft[int(key[i])] = int(d["req"][j])
    o2 = np.argsort(pages_tr, kind="stable")
    k2 = pages_tr[o2]
    starts = np.concatenate([[0], np.nonzero(np.diff(k2))[0] + 1])
    ends = np.concatenate([starts[1:], [len(k2)]])
    for s0, e0 in zip(starts, ends):
        ix = o2[s0:e0]
        pg_ft_tr[int(k2[s0])] = int(d["req"][ix[0]])
        rix = ix[d["isR"][ix]]
        if len(rix):
            k0 = d["gk"][rix].min()
            sel = rix[d["gk"][rix] == k0]
            c = np.bincount(d["req"][sel], minlength=49)
            k1cnt[int(k2[s0])] = int(np.argmax(c))
    S = trace_sample(w)
    # ---------------- accumulate
    A = collections.Counter()
    agree = {"page": collections.Counter(), "blk": collections.Counter()}
    agree_cls = collections.defaultdict(collections.Counter)
    err = collections.Counter()
    chk = collections.Counter()
    rule = {}
    for gran in ("page", "blk1k"):
        f = os.path.join(ROOT, "C2", "%s_%s.csv.gz" % (w, gran))
        rule[gran] = {}
        if os.path.exists(f):
            with gzip.open(f, "rt") as fi:
                next(fi)
                for line in fi:
                    a = line.split(",")
                    rule[gran][(int(a[0]), int(a[1]))] = dev2tile(int(a[2]) + 1)
    for p, R in P.items():
        if p in info:
            ow = dev2tile(info[p][1])
        elif p in cls:
            ow = int(cls[p]["owner"])
        else:
            continue
        c = cls[p]["cls"] if p in cls else "无远程读"
        readers_ = {g: v for g, v in R.items() if v["rd"]}
        if not readers_:
            continue
        ft = min(R, key=lambda g: (R[g]["fn"], g))
        # ---- page level
        tot = sum(v["rd"] for v in readers_.values())
        mx = max(v["rd"] for v in readers_.values())
        main = ow if readers_.get(ow, {}).get("rd", 0) == mx else max(readers_, key=lambda g: (readers_[g]["rd"], -g))
        rr = sum(v["rd"] for g, v in readers_.items() if g != ow)
        homes = dict(base=ow, ft=ft, main=main, rule=rule["page"].get((p, -1), ow))
        Hs = {}
        for k, h in homes.items():
            Hs[k] = sum(v["rd"] * hops(g, h) for g, v in readers_.items())
            A["pH_" + k] += Hs[k]
            A["pR_" + k] += sum(v["rd"] for g, v in readers_.items() if g != h)
            A["pHr_" + k] += sum(v["rd"] * v["keep"] * hops(g, h) for g, v in readers_.items())
            A["pRr_" + k] += sum(v["rd"] * v["keep"] for g, v in readers_.items() if g != h)
        agree["page"]["n"] += rr
        agree_cls[c]["n"] += rr
        if ft == main:
            agree["page"]["ok"] += rr
            agree_cls[c]["ok"] += rr
        else:
            ex = Hs["ft"] - Hs["main"]
            ftr = readers_.get(ft, {}).get("rd", 0)
            if c == HOT:
                cat = "hot"
            elif ftr < 0.1 * tot:
                cat = "init"
            elif mx < 0.6 * tot:
                cat = "noshare"
            else:
                cat = "minor"
                if p in k1cnt:
                    err["minor_tr_rr"] += rr
                    if k1cnt[p] == ft:
                        err["minor_kchg_rr"] += rr
            err[cat + "_rr"] += rr
            err[cat + "_H"] += ex
        # trace check of the page-level first toucher
        if p in pg_ft_tr:
            chk["pg_n"] += 1
            chk["pg_ok"] += 1 if pg_ft_tr[p] == ft else 0
        # ---- 1 KB blocks
        cand = {g: v for g, v in R.items()}
        for b in range(4):
            bw = {g: v["sub"][b] for g, v in readers_.items() if v["sub"][b]}
            if not bw:
                continue
            cg = [g for g, v in R.items() if (v["rd"] and v["sub"][b]) or (not v["rd"] and v["wr"])]
            bft = min(cg, key=lambda g: (R[g]["fn"], g))
            bmx = max(bw.values())
            bmain = ow if bw.get(ow, 0) == bmx else max(bw, key=lambda g: (bw[g], -g))
            brr = sum(x for g, x in bw.items() if g != ow)
            bh = dict(base=ow, ft=bft, main=bmain, rule=rule["blk1k"].get((p, b), ow))
            for k, h in bh.items():
                A["bH_" + k] += sum(x * hops(g, h) for g, x in bw.items())
                A["bR_" + k] += sum(x for g, x in bw.items() if g != h)
                A["bHr_" + k] += sum(x * R[g]["keep"] * hops(g, h) for g, x in bw.items())
                A["bRr_" + k] += sum(x * R[g]["keep"] for g, x in bw.items() if g != h)
            agree["blk"]["n"] += brr
            agree["blk"]["ok"] += brr if bft == bmain else 0
            kk = p * 4 + b
            if kk in blk_ft:
                chk["b_n"] += 1
                chk["b_ok"] += 1 if blk_ft[kk] == bft else 0
                chk["b_exact_eq_main_n"] += brr
                chk["b_exact_eq_main_ok"] += brr if blk_ft[kk] == bmain else 0
                chk["b_approx_eq_main_ok"] += brr if bft == bmain else 0
    H0 = A["pH_base"] or 1
    R0 = A["pR_base"] or 1
    out = dict(w=w, sample=S)
    for gran, pre in (("page", "p"), ("blk1k", "b")):
        for k in ("ft", "main", "rule", "base"):
            out["%s_R_%s" % (gran, k)] = A[pre + "R_" + k] / R0
            out["%s_H_%s" % (gran, k)] = A[pre + "H_" + k] / H0
            out["%s_Rret_%s" % (gran, k)] = A[pre + "Rr_" + k] / R0
            out["%s_Hret_%s" % (gran, k)] = A[pre + "Hr_" + k] / H0
    out["agree_page"] = agree["page"]["ok"] / max(1, agree["page"]["n"])
    out["agree_blk"] = agree["blk"]["ok"] / max(1, agree["blk"]["n"])
    out["agree_cls"] = {c: (v["ok"] / v["n"], v["n"] / R0) for c, v in agree_cls.items() if v["n"] / R0 >= 0.01}
    for cat in ("hot", "init", "noshare", "minor"):
        out["err_%s_rr" % cat] = err[cat + "_rr"] / R0
        out["err_%s_H" % cat] = err[cat + "_H"] / H0
    out["minor_kchg_share"] = err["minor_kchg_rr"] / err["minor_tr_rr"] if err["minor_tr_rr"] else None
    out["chk_page_ft_trace_eq"] = chk["pg_ok"] / chk["pg_n"] if chk["pg_n"] else None
    out["chk_blk_ft_trace_eq"] = chk["b_ok"] / chk["b_n"] if chk["b_n"] else None
    out["chk_blk_exact_agree"] = chk["b_exact_eq_main_ok"] / chk["b_exact_eq_main_n"] if chk["b_exact_eq_main_n"] else None
    out["chk_blk_approx_agree_same_sample"] = chk["b_approx_eq_main_ok"] / chk["b_exact_eq_main_n"] if chk["b_exact_eq_main_n"] else None
    print(json.dumps(out, ensure_ascii=False))


if __name__ == "__main__":
    main(sys.argv[1])
