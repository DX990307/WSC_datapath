#!/usr/bin/env python3
"""P21 offline A: early hotspot identification on the W=128 v4m3 L1V trace.

usage: offline_hot.py <workload>   -> appends rows to stdout as CSV (no header)

Truth = pgcls class 热点广播 (pgcls_page.py on the W=128 baseline exact aggregation).
Signal (i) owner side: the first N remote reads of a page (req != own, op R, time order);
  hot as soon as the number of distinct readers among them reaches K (decided at that read).
Signal (ii) translation side: distinct GPMs (owner included) whose first access (R or W) to
  the page falls within T us of the page's first access; hot when the K-th arrives (<= t0+T).
Signal (ii-s) sliding variant: hot when the first accesses of K distinct GPMs fall within any
  window of T us (decided at the K-th of them); needed when the page is first touched (e.g.
  initialised by its owner) long before the readers arrive (PR, atax).
Signal (ii-sr): as (ii-s) but only the first *read* of each GPM counts (writes ignored).
Per workload and parameter set, weighted by the trace's remote reads:
  recall      hot-page remote reads of detected hot pages / all hot-page remote reads
  after       hot-page remote reads after the decision / all hot-page remote reads
  before      reads up to the decision / reads of the detected hot pages (timeliness)
  t_med_us    median (pages) time first access -> decision, detected hot pages
  false       remote reads of detected non-hot pages / all remote reads, total and per class
"""
import sys, csv, collections
import numpy as np
from offline_common import *

NS = [8, 16, 32, 64]
KS = [3, 4, 6, 8]
TS = [1, 2, 5, 10]           # us
HARM = ["私有放错", "流式", "自己反复读"]


def main(w):
    cls = load_cls(w)
    d = load_trace_np(w)
    t, req, own, isR = d["t"], d["req"], d["own"], d["isR"]
    remote = isR & (req != own)
    tot_remote = int(remote.sum())
    acc_i = collections.defaultdict(lambda: collections.Counter())   # (N,K) -> counters
    acc_ii = collections.defaultdict(lambda: collections.Counter())
    acc_is = collections.defaultdict(lambda: collections.Counter())
    tmed_is = collections.defaultdict(list)
    acc_sr = collections.defaultdict(lambda: collections.Counter())
    tmed_sr = collections.defaultdict(list)
    tmed_i = collections.defaultdict(list)
    tmed_ii = collections.defaultdict(list)
    hot_tot = 0
    for pg, ix in by_page(d):
        rm = remote[ix]
        nr = int(rm.sum())
        c = cls[pg]["cls"] if pg in cls else "无远程读"
        hot = c == HOT
        if hot:
            hot_tot += nr
        t0 = t[ix[0]]
        # ---- (i)
        rix = ix[rm]
        if nr:
            rs = req[rix]
            _, fi = np.unique(rs, return_index=True)
            fi.sort()
            for K in KS:
                if len(fi) < K:
                    continue
                j = int(fi[K - 1])
                for N in NS:
                    if j >= N:
                        continue
                    a = acc_i[(N, K)]
                    if hot:
                        a["det_hot"] += nr
                        a["before"] += j + 1
                        a["after"] += nr - j - 1
                        tmed_i[(N, K)].append((t[rix[j]] - t0) / 1000.0)
                    else:
                        a["false"] += nr
                        a["false_" + c] += nr
                        a["false_pages"] += 1
        # ---- (ii)
        g = req[ix]
        _, fi = np.unique(g, return_index=True)
        ft = np.sort(t[ix[fi]])
        rtimes = t[rix] if nr else None
        for K in KS:
            if len(ft) < K:
                continue
            tk = ft[K - 1]
            for T in TS:
                if tk > t0 + T * 1000.0:
                    continue
                a = acc_ii[(T, K)]
                nb = int(np.searchsorted(rtimes, tk, side="right")) if nr else 0
                if hot:
                    a["det_hot"] += nr
                    a["before"] += nb
                    a["after"] += nr - nb
                    tmed_ii[(T, K)].append((tk - t0) / 1000.0)
                else:
                    a["false"] += nr
                    a["false_" + c] += nr
                    a["false_pages"] += 1
        # ---- (ii-s) sliding window over the first-touch times; (ii-sr) first reads only
        rd = ix[isR[ix]]
        if len(rd):
            _, fr = np.unique(req[rd], return_index=True)
            ftr = np.sort(t[rd[fr]])
        else:
            ftr = np.array([])
        for ftx, acc, tm in ((ft, acc_is, tmed_is), (ftr, acc_sr, tmed_sr)):
          for K in KS:
              if len(ftx) < K:
                  continue
              gap = ftx[K - 1:] - ftx[:len(ftx) - K + 1]
              for T in TS:
                  ok = np.nonzero(gap <= T * 1000.0)[0]
                  if len(ok) == 0:
                      continue
                  tk = ftx[ok[0] + K - 1]
                  a = acc[(T, K)]
                  nb = int(np.searchsorted(rtimes, tk, side="right")) if nr else 0
                  if hot:
                      a["det_hot"] += nr
                      a["before"] += nb
                      a["after"] += nr - nb
                      tm[(T, K)].append((tk - t0) / 1000.0)
                  else:
                      a["false"] += nr
                      a["false_" + c] += nr
                      a["false_pages"] += 1
    wr = csv.writer(sys.stdout)
    for sig, acc, tm, P in (("i", acc_i, tmed_i, NS), ("ii", acc_ii, tmed_ii, TS), ("ii-s", acc_is, tmed_is, TS), ("ii-sr", acc_sr, tmed_sr, TS)):
        for p in P:
            for K in KS:
                a = acc[(p, K)]
                ht = max(1, hot_tot)
                other = a["false"] - sum(a["false_" + h] for h in HARM)
                wr.writerow([w, sig, p, K, hot_tot, tot_remote,
                             round(a["det_hot"] / ht, 4) if hot_tot else "",
                             round(a["after"] / ht, 4) if hot_tot else "",
                             round(a["before"] / a["det_hot"], 4) if a["det_hot"] else "",
                             round(float(np.median(tm[(p, K)])), 3) if tm[(p, K)] else "",
                             round(a["false"] / tot_remote, 4)] +
                            [round(a["false_" + h] / tot_remote, 4) for h in HARM] +
                            [round(other / tot_remote, 4), a["false_pages"]])


if __name__ == "__main__":
    main(sys.argv[1])
