#!/usr/bin/env python3
"""P21 offline B: same-buffer propagation of the hotspot decision.

usage: offline_buffer.py <workload> [T_us K]   (default: signal ii-sr, T = 2 us, K = 4)
  -> JSON line on stdout: per-buffer rows + workload summary

Buffer = one driver allocation (alloc 'block'). Hot = pgcls 热点广播 on the W=128 baseline.
Detection = signal (ii-sr) on the W=128 v4m3 trace: the first reads of K distinct GPMs fall in
a T-us window. For each buffer with a hot page:
  hot_frac      hot pages / pages with remote reads (baseline), longest hot run / hot pages
  at the buffer's first detection time t_d (any page, true or false):
    unacc       pages of the buffer not yet accessed by any GPM (trace) / buffer pages in the trace
    prec        hot share of these pages (pages, and remote-read weighted)
    gain        their hot remote reads / all hot remote reads of the workload
    gain_inc    the part of it that their own detection would miss (reads up to their own
                detection, or all reads if never detected) / all hot remote reads
  gated variant (g_*): propagate only at the first detection event at which >= GATE of the
                buffer's pages accessed so far (>= 4) have been detected hot
  conc          hot pages' busiest owner device share (pages; reads) -> >= 0.3 concentrated
Trace page sampling 1/4 (atax, embbag, ...): only hashed-in pages are counted in the trace
metrics (unbiased page sample); baseline metrics use all pages.
"""
import sys, json, collections
import numpy as np
from offline_common import *

GATE = 0.5


def main(w, T_us=2.0, K=4):
    info, bl = load_alloc(w)
    cls = load_cls(w)
    S = trace_sample(w)
    d = load_trace_np(w)
    t, req, own, isR = d["t"], d["req"], d["own"], d["isR"]
    remote = isR & (req != own)
    first = {}
    det = {}
    nrem = {}
    before_det = {}
    for pg, ix in by_page(d):
        first[pg] = t[ix[0]]
        rix = ix[remote[ix]]
        nrem[pg] = len(rix)
        rd = ix[isR[ix]]
        if len(rd) == 0:
            continue
        _, fr = np.unique(req[rd], return_index=True)
        ftr = np.sort(t[rd[fr]])
        if len(ftr) < K:
            continue
        gap = ftr[K - 1:] - ftr[:len(ftr) - K + 1]
        ok = np.nonzero(gap <= T_us * 1000.0)[0]
        if len(ok):
            tk = ftr[ok[0] + K - 1]
            det[pg] = tk
            before_det[pg] = int(np.searchsorted(t[rix], tk, side="right"))
    sampled = lambda p: S == 1 or pghash(p) % S == 0
    hot_tr = sum(nrem.get(p, 0) for p, r in cls.items() if r["cls"] == HOT)
    # workload-level concentration (P20 rule): busiest owner share of hot remote reads (baseline)
    own_r = collections.Counter()
    for p, r in cls.items():
        if r["cls"] == HOT:
            own_r[int(r["owner"])] += int(r["rd_remote"])
    hot_base = sum(own_r.values())
    wl_share = max(own_r.values()) / hot_base if hot_base else 0.0
    rows = []
    for blk, pl in sorted(bl.items()):
        hot = [p for p in pl if p in cls and cls[p]["cls"] == HOT]
        if not hot:
            continue
        acc = [p for p in pl if p in cls]
        hs = set(hot)
        run = best = 0
        for p in pl:
            run = run + 1 if p in hs else 0
            best = max(best, run)
        dv = collections.Counter(info[p][1] for p in hot)
        dr = collections.Counter()
        for p in hot:
            dr[info[p][1]] += int(cls[p]["rd_remote"])
        hr = sum(int(cls[p]["rd_remote"]) for p in hot)
        row = dict(blk=blk, N=len(pl), pages_rr=len(acc), hot=len(hot), hot_frac=len(hot) / max(1, len(acc)),
                   hot_frac_all=len(hot) / len(pl), longest_run=best / len(hot),
                   hot_reads_share=hr / max(1, hot_base),
                   conc_pages=max(dv.values()) / len(hot), conc_reads=max(dr.values()) / max(1, hr),
                   devices=len(dv))
        trp = [p for p in pl if p in first and sampled(p)]
        dets = [det[p] for p in trp if p in det]
        if dets:
            td = min(dets)
            un = [p for p in trp if first[p] > td]
            uh = [p for p in un if p in hs]
            row.update(t_d_us=td / 1000.0, tr_pages=len(trp), unacc=len(un) / len(trp),
                       prec=len(uh) / len(un) if un else None,
                       prec_r=(sum(nrem[p] for p in uh) / sum(nrem[p] for p in un)) if un and sum(nrem[p] for p in un) else None,
                       gain=sum(nrem[p] for p in uh) / max(1, hot_tr),
                       gain_inc=sum(before_det.get(p, nrem[p]) for p in uh) / max(1, hot_tr),
                       wrong_reads=sum(nrem[p] for p in un if p not in hs) / max(1, sum(nrem.values())),
                       first_det_hot=int(min(trp, key=lambda p: det.get(p, 1e30)) in hs))
            # gated propagation
            evs = sorted((det[p], p) for p in trp if p in det)
            fa = sorted(first[p] for p in trp)
            ndet = 0
            for tdv, p in evs:
                ndet += 1
                na = int(np.searchsorted(fa, tdv, side="right"))
                if na >= 4 and ndet / na >= GATE:
                    un = [q for q in trp if first[q] > tdv]
                    uh = [q for q in un if q in hs]
                    row.update(g_t_us=tdv / 1000.0, g_unacc=len(un) / len(trp),
                               g_prec=len(uh) / len(un) if un else None,
                               g_gain=sum(nrem[q] for q in uh) / max(1, hot_tr),
                               g_gain_inc=sum(before_det.get(q, nrem[q]) for q in uh) / max(1, hot_tr),
                               g_wrong_reads=sum(nrem[q] for q in un if q not in hs) / max(1, sum(nrem.values())))
                    break
        rows.append(row)
    # summary
    s = dict(w=w, sample=S, T_us=T_us, K=K, hot_buffers=len(rows), wl_conc=wl_share,
             wl_concentrated=wl_share >= 0.3)
    hb = sum(r["hot_reads_share"] for r in rows) or 1
    s["hot_frac_w"] = sum(r["hot_frac"] * r["hot_reads_share"] for r in rows) / hb
    s["longest_run_w"] = sum(r["longest_run"] * r["hot_reads_share"] for r in rows) / hb
    s["buf_conc_agree_w"] = sum(r["hot_reads_share"] for r in rows if (r["conc_reads"] >= 0.3) == (wl_share >= 0.3)) / hb
    s["buf_conc_w"] = sum(r["hot_reads_share"] for r in rows if r["conc_reads"] >= 0.3) / hb
    dr = [r for r in rows if "t_d_us" in r]
    s["gain"] = sum(r["gain"] for r in dr)
    s["gain_inc"] = sum(r["gain_inc"] for r in dr)
    un_tot = sum(r["unacc"] * r["tr_pages"] for r in dr)
    s["unacc_pages_w"] = un_tot / max(1, sum(r["tr_pages"] for r in dr))
    s["prec_pages"] = sum((r["prec"] or 0) * r["unacc"] * r["tr_pages"] for r in dr) / un_tot if un_tot else None
    gr = [r for r in rows if "g_t_us" in r]
    s["g_buffers"] = len(gr)
    s["g_gain"] = sum(r["g_gain"] for r in gr)
    s["g_gain_inc"] = sum(r["g_gain_inc"] for r in gr)
    s["g_wrong_reads"] = sum(r["g_wrong_reads"] for r in gr)
    gu = sum(r["g_unacc"] * r["tr_pages"] for r in gr)
    s["g_prec_pages"] = sum((r["g_prec"] or 0) * r["g_unacc"] * r["tr_pages"] for r in gr) / gu if gu else None
    s["wrong_reads"] = sum(r["wrong_reads"] for r in dr)
    s["first_det_hot"] = sum(r["first_det_hot"] for r in dr) / len(dr) if dr else None
    print(json.dumps(dict(summary=s, buffers=rows), ensure_ascii=False))


if __name__ == "__main__":
    a = sys.argv[1:]
    main(a[0], *(float(a[1]), int(a[2])) if len(a) > 2 else ())
