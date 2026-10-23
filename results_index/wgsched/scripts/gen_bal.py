#!/usr/bin/env python3
"""Load-balance-only control: per kernel, WG w -> GPM floor(w*48/N) (contiguous,
even blocks, data location ignored). Writes maps/<wl>.bal.map for workloads where
any kernel's default WG/GPM max/avg > 1.02, and prints predicted local byte
fraction (default / bal / data map) from pass-1 stats."""
import csv, glob, os, sys, collections
D = os.path.expanduser("~/akita-exp-test/wgsched")
NG = 48
for l in open(f"{D}/pass1.tsv"):
    wl, exp = l.split()
    sp = f"{D}/maps/{wl}.map.summary.csv"
    if not os.path.exists(sp): continue
    ks = list(csv.DictReader(open(sp)))
    if not any(float(k["max_over_avg_default"]) > 1.02 for k in ks):
        print(wl, "balanced-by-default"); continue
    rd = f"{D}/src/results/{exp}/stats"
    L = {}
    for r in csv.DictReader(open(f"{rd}/metrics_wglaunch.csv")):
        L[int(r["kernel"])] = int(r["total_wg"])
    lines = []
    bal = {}
    for k, n in sorted(L.items()):
        a = [w * NG // n for w in range(n)]
        bal[k] = a
        lines.append(f"{k} {n} " + " ".join(map(str, a)))
    with open(f"{D}/maps/{wl}.bal.map.tmp", "w") as f:
        f.write("# balance-only map\n" + "\n".join(lines) + "\n")
    os.replace(f"{D}/maps/{wl}.bal.map.tmp", f"{D}/maps/{wl}.bal.map")
    tot = loc = 0
    for r in csv.DictReader(open(f"{rd}/metrics_wgaff.csv")):
        k, w, o, b = int(r["kernel"]), int(r["wg"]), int(r["owner_dev"]) - 1, int(r["bytes"])
        if k in bal and w < len(bal[k]):
            tot += b; loc += b if bal[k][w] == o else 0
    tb = sum(float(k["bytes"] or 0) for k in ks) or 1
    ld = sum(float(k["bytes"]) * float(k["local_default"] or 0) for k in ks) / tb
    lm = sum(float(k["bytes"]) * float(k["local_map"] or 0) for k in ks) / tb
    print(wl, "bal-map local default %.2f bal %.2f datamap %.2f" % (ld, loc / max(tot, 1), lm))
