#!/usr/bin/env python3
"""Data-blind control: per kernel, a random permutation of WGs cut into 48
even blocks (balanced counts, data location ignored, contiguity destroyed).
Usage: gen_shuf.py wl... ; also adds baseline+wgshuf rows at the queue head."""
import csv, os, random, sys
D = os.path.expanduser("~/akita-exp-test/wgsched")
p1 = dict(l.split() for l in open(f"{D}/pass1.tsv"))
rows = [l.rstrip("\n").split("\t") for l in open(f"{D}/queue.tsv") if l.strip()]
have = {r[0] for r in rows}; new = []
for wl in sys.argv[1:]:
    if wl not in p1: continue
    rd = f"{D}/src/results/{p1[wl]}/stats"
    out = []
    for r in csv.DictReader(open(f"{rd}/metrics_wglaunch.csv")):
        k, n = int(r["kernel"]), int(r["total_wg"])
        perm = list(range(n)); random.Random(k * 7919 + 1).shuffle(perm)
        a = [0] * n
        for i, w in enumerate(perm): a[w] = i * 48 // n
        out.append(f"{k} {n} " + " ".join(map(str, a)))
    m = f"{D}/maps/{wl}.shuf.map"
    open(m + ".tmp", "w").write("# shuffle control\n" + "\n".join(out) + "\n"); os.replace(m + ".tmp", m)
    src = next(r for r in rows if r[0].startswith(f"1022_place_wgs-{wl}_mi200+wgmap_"))
    ne = src[0].replace("+wgmap_", "+wgshuf_")
    if ne in have: continue
    nr = list(src); nr[0] = nr[2] = ne; nr[4] = src[4].replace(f"maps/{wl}.map", f"maps/{wl}.shuf.map"); nr[6] = m
    new.append(nr)
open(f"{D}/queue.tsv.new", "w").write("\n".join("\t".join(r) for r in new + rows) + "\n")
os.replace(f"{D}/queue.tsv.new", f"{D}/queue.tsv")
print([r[0] for r in new])
