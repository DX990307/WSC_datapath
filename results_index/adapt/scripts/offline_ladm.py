#!/usr/bin/env python3
"""P21 offline F: LADM-style static placement table and first-touch estimate.

usage: offline_ladm.py <workload> <out_table>   -> JSON line on stdout, writes <out_table>

LADM approximation (MICRO'20: index analysis; regular arrays -> each TB's data block on the node
its TB is scheduled to, page-interleaved; irregular arrays keep the kernel-wide contiguous split
= our driver default; the first kernel's pattern is used for all kernels):
  per buffer, take the FIRST global kernel that reads it (W=128 v4m3 trace, op R; 1/4 page
  sample in non-hot workloads); page target = reader with >= 0.6 of the page's reads in that
  kernel (owner included); fit the generative rules of offline_pattern (lin: refined linear
  split; step; owner offset) on all pages with a target, weighted by their reads; the best rule
  is applied to EVERY page of the buffer only if its hit rate >= 0.8 ("static analysis can tell")
  and the pages it moves away from the owner hold >= 10 % of the buffer's reads in that kernel
  (>= 8 such pages); otherwise the buffer stays. The target comes only from the rule.
  Table: "<1 KB block = phys addr >> 10> <tile>", 4 lines per moved page (P20 -place-table).
First touch: each page is homed at the GPM that accesses it first (baseline readers first_ns,
  reads or writes; host copies are not GPM accesses).
Remaining read-hops (bound.md model, L1V reads only, baseline exact aggregation, whole run):
  sum_g reads_g * hops(g, home) / baseline; "+M3+" = retention: reader g keeps
  (reads - rep_intra - rep_cross) / reads of its reads. Also pg = each page at its main reader
  (= P20 'pg' oracle) for reference.
"""
import sys, json, collections
import numpy as np
from offline_common import *
from offline_pattern import fit_lin, pred_lin, fit_step, wmode

SHARE = 0.6
HIT = 0.8
MIGW = 0.10
MINMIG = 8


def main(w, out_table):
    info, bl = load_alloc(w)
    cls = load_cls(w)
    # ---------------- exact readers
    R = collections.defaultdict(dict)       # page -> g -> (reads, keep)
    first = {}
    for r in readers(w):
        p = int(r["page"])
        rd = int(r["reads"])
        fn = float(r["first_ns"]) if r["first_ns"] else None
        g = int(r["reader"])
        if fn is not None and (rd or int(r["writes"])):
            if p not in first or fn < first[p][0] or (fn == first[p][0] and g < first[p][1]):
                first[p] = (fn, g)
        if rd:
            rep = int(r["rep_intra"]) + int(r["rep_cross"])
            R[p][g] = (rd, max(0.0, (rd - rep) / rd))
    # ---------------- first-kernel targets from the trace
    d = load_trace_np(w)
    m = d["isR"]
    cnt = collections.defaultdict(collections.Counter)
    for p, k, g in zip(d["page"][m].tolist(), d["gk"][m].tolist(), d["req"][m].tolist()):
        cnt[(p, k)][g] += 1
    kfirst = {}
    for (p, k) in cnt:
        if p in info:
            b = info[p][2]
            kfirst[b] = min(k, kfirst.get(b, 1 << 30))
    home = {}                                # page -> tile (only moved pages)
    bufrep = []
    for blk, pl in bl.items():
        if blk not in kfirst:
            continue
        k0 = kfirst[blk]
        I, D, Wt, O = [], [], [], []
        tot_reads = 0
        for i, p in enumerate(pl):
            c = cnt.get((p, k0))
            if not c:
                continue
            tot = sum(c.values())
            tot_reads += tot
            g, v = max(c.items(), key=lambda x: (x[1], -x[0]))
            if v < SHARE * tot:
                continue
            I.append(i); D.append(tile2dev(g) - 1); Wt.append(tot); O.append(info[p][1] - 1)
        rec = dict(blk=blk, N=len(pl), first_k=k0, tgt_pages=len(I), applied=False)
        if len(I) >= 2:
            I = np.array(I, dtype=np.int64); D = np.array(D, dtype=np.int64)
            Wt = np.array(Wt, dtype=float); O = np.array(O, dtype=np.int64)
            cands = []
            p = fit_lin(I, D, Wt, refine=True)
            if p:
                pr = np.array([pred_lin(p, i) for i in I])
                cands.append(("lin", Wt[pr == D].sum() / Wt.sum(), lambda i, o, p=p: pred_lin(p, i)))
            q = fit_step(I, D, Wt)
            if q:
                pr = (q[1] + q[0] * I) % 48
                cands.append(("step", Wt[pr == D].sum() / Wt.sum(), lambda i, o, q=q: (q[1] + q[0] * i) % 48))
            dl = wmode(D - O, Wt)
            cands.append(("owner", Wt[((O + dl) % 48) == D].sum() / Wt.sum(), lambda i, o, dl=dl: (o + dl) % 48))
            name, h, f = max(cands, key=lambda x: x[1])
            migmask = D != O
            rec.update(rule=name, hit=h, mig_pages=int(migmask.sum()),
                       mig_frac=float(Wt[migmask].sum() / max(1, tot_reads)))
            if h >= HIT and migmask.sum() >= MINMIG and rec["mig_frac"] >= MIGW:
                rec["applied"] = True
                moved = 0
                for i, p in enumerate(pl):
                    o = info[p][1] - 1
                    t = dev2tile(f(i, o) + 1)
                    if t != dev2tile(o + 1):
                        home[p] = t
                        moved += 1
                rec["moved_pages"] = moved
        bufrep.append(rec)
    # ---------------- table
    lines = []
    for p in sorted(home):
        for b in range(4):
            lines.append("%d %d" % ((p << 2) + b, home[p]))
    with open(out_table, "w") as fo:
        fo.write("# P21 LADM-style placement table %s (rule from the first kernel, hit >= %.1f)\n" % (w, HIT))
        fo.write("\n".join(lines) + ("\n" if lines else ""))
    # ---------------- read-hops
    acc = collections.Counter()
    loss_cls = collections.Counter()
    for p, rd in R.items():
        o = dev2tile(info[p][1]) if p in info else int(cls[p]["owner"]) if p in cls else None
        if o is None:
            continue
        main = max(rd, key=lambda g: (rd[g][0], g == o, -g))
        homes = dict(base=o, ladm=home.get(p, o), ft=first[p][1] if p in first else o, pg=main)
        rr = sum(v[0] for g, v in rd.items() if g != o)
        acc["rr"] += rr
        for k, h in homes.items():
            acc["H_" + k] += sum(v[0] * hops(g, h) for g, v in rd.items())
            acc["Hr_" + k] += sum(v[0] * v[1] * hops(g, h) for g, v in rd.items())
            acc["R_" + k] += sum(v[0] for g, v in rd.items() if g != h)
        c = cls[p]["cls"] if p in cls else "无远程读"
        if p in home:
            acc["ladm_rr"] += rr
        if homes["ft"] != main:
            acc["ft_ne_main_rr"] += rr
            loss_cls[c] += sum(v[0] * hops(g, homes["ft"]) for g, v in rd.items()) - sum(v[0] * hops(g, main) for g, v in rd.items())
        if homes["ft"] != o:
            acc["ft_moved_rr"] += rr
    H0 = acc["H_base"] or 1
    Hr0 = acc["Hr_base"] or 1
    out = dict(w=w, sample=trace_sample(w), buffers=len(bl), applied_buffers=sum(1 for b in bufrep if b["applied"]),
               moved_pages=len(home), table_lines=len(lines), ladm_rr_share=acc["ladm_rr"] / max(1, acc["rr"]),
               **{"H_" + k: acc["H_" + k] / H0 for k in ("ladm", "ft", "pg")},
               **{"Hret_" + k: acc["Hr_" + k] / H0 for k in ("base", "ladm", "ft", "pg")},
               **{"R_" + k: acc["R_" + k] / max(1, acc["rr"]) for k in ("ladm", "ft", "pg")},
               ft_ne_main_rr=acc["ft_ne_main_rr"] / max(1, acc["rr"]),
               ft_moved_rr=acc["ft_moved_rr"] / max(1, acc["rr"]),
               ft_loss_by_cls={c: v / H0 for c, v in loss_cls.items() if abs(v) / H0 >= 0.005},
               buffer_detail=bufrep)
    print(json.dumps(out, ensure_ascii=False))


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
