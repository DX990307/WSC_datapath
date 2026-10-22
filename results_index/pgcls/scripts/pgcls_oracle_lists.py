#!/usr/bin/env python3
"""Page lists for the per-class oracle (-pgcls-oracle-pages).

usage: pgcls_oracle_lists.py <label>_pages.csv.gz <out_dir> <label> [min_share=0.05]
Writes <out_dir>/<label>__<key>.txt (one page per line) for every class with
>= min_share of the baseline remote reads, and prints "<key> <share> <pages>".
"""
import csv, gzip, os, sys, collections

KEY = {"热点广播": "hotb", "轮转广播": "rotb", "亲和/区域": "aff", "随机共享": "rand",
       "私有放错": "misp", "自己反复读": "self", "流式": "strm", "写共享": "wsh",
       "owner为主": "own"}
src, out, label = sys.argv[1:4]
th = float(sys.argv[4]) if len(sys.argv) > 4 else 0.05
pages = collections.defaultdict(list)
reads = collections.Counter()
with gzip.open(src, "rt") as f:
    for r in csv.DictReader(f):
        pages[r["cls"]].append(r["page"])
        reads[r["cls"]] += int(r["rd_remote"])
tot = sum(reads.values()) or 1
os.makedirs(out, exist_ok=True)
for c, n in sorted(reads.items(), key=lambda x: -x[1]):
    if n / tot >= th:
        with open(os.path.join(out, f"{label}__{KEY[c]}.txt"), "w") as f:
            f.write("\n".join(pages[c]) + "\n")
        print(KEY[c], f"{n / tot:.3f}", len(pages[c]))
