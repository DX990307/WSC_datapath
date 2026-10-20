#!/usr/bin/env python3
"""P12: combine the per-run p12_footprint.py outputs into two summary tables.

    python3 akkalat/p12_tables.py <dir with <workload>_affinity.csv / _stability.csv>

<dir>/affinity_summary.csv   workload x kernel name x pair class, pair-weighted
                             over the launches: mean Jaccard, mean containment,
                             fraction of pairs with Jaccard >= 0.5
<dir>/stability_summary.csv  workload x kernel name x lag x mode, pair-weighted
"""

import csv
import glob
import os
import sys
from collections import defaultdict


def fnum(v):
    try:
        return float(v)
    except (TypeError, ValueError):
        return None


def combine(rows, keys, out, fields):
    acc = defaultdict(lambda: defaultdict(float))
    order = []
    for r in rows:
        n = fnum(r.get("pairs")) or 0
        if n == 0:
            continue
        k = tuple(r[x] for x in keys)
        if k not in acc:
            order.append(k)
        a = acc[k]
        a["pairs"] += n
        a["launch_pairs"] += 1
        for f in fields:
            v = fnum(r.get(f))
            if v is not None:
                a[f] += v * n
    with open(out, "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(list(keys) + ["launch_pairs", "pairs"] + fields)
        for k in order:
            a = acc[k]
            w.writerow(list(k) + [int(a["launch_pairs"]), int(a["pairs"])] +
                       [f"{a[x] / a['pairs']:.4f}" for x in fields])
    print("wrote", out)


def main():
    d = sys.argv[1]
    aff, stab = [], []
    for p in sorted(glob.glob(os.path.join(d, "*_affinity.csv"))):
        aff += list(csv.DictReader(open(p)))
    for p in sorted(glob.glob(os.path.join(d, "*_stability.csv"))):
        if open(p).readline().startswith("none"):
            continue
        stab += list(csv.DictReader(open(p)))
    combine(aff, ["workload", "name", "class"], os.path.join(d, "affinity_summary.csv"),
            ["jaccard_mean", "contain_mean", "jaccard_frac_ge_0.5", "jaccard_frac_zero"])
    combine(stab, ["workload", "name", "lag", "mode"], os.path.join(d, "stability_summary.csv"),
            ["jaccard_mean", "contain_mean", "jaccard_frac_ge_0.9", "same_gpm_frac"])


if __name__ == "__main__":
    main()
