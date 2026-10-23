#!/usr/bin/env python3
"""Guarded map: drop kernels whose predicted byte-hop ratio > G (keep default
dispatch). Prints <wl> <kept> <total> <status>."""
import csv, glob, os, sys
G = float(sys.argv[1]) if len(sys.argv) > 1 else 0.8
M = os.path.expanduser("~/akita-exp-test/wgsched/maps")
for p in sorted(glob.glob(f"{M}/*.map")):
    if p.count(".") > 1:
        continue
    wl = os.path.basename(p)[:-4]
    keep = set()
    ks = list(csv.DictReader(open(p + ".summary.csv")))
    for k in ks:
        r = k["byte_hops_ratio"]
        if r != "" and float(r) <= G:
            keep.add(k["kernel"])
    lines = [l for l in open(p) if not l.startswith("#")]
    kept = [l for l in lines if l.split()[0] in keep]
    st = "same" if len(kept) == len(lines) else ("none" if not kept else "diff")
    if st == "diff":
        with open(f"{M}/{wl}.g.map.tmp", "w") as f:
            f.write("# guarded map\n" + "".join(kept))
        os.replace(f"{M}/{wl}.g.map.tmp", f"{M}/{wl}.g.map")
    print(wl, len(kept), len(lines), st)
