#!/usr/bin/env python3
"""Per-class traffic of a v4_complete run, with the page classes of the baseline run.

usage: pgcls_v4.py <v4_prefix> <baseline_pages.csv.gz from pgcls_page.py> <label> <out_dir>
Writes <out_dir>/<label>_v4.json: by class (baseline class of the page; pages not
classified in the baseline -> "未分类"): logical remote reads (L1 -> RDMA), network
read lines (incl. prefetch), network line-hops, remote read latency total (ns).
"""
import csv, gzip, json, os, sys, collections

v4, base, label, out = sys.argv[1:5]
cls = {}
with gzip.open(base, "rt") as f:
    for r in csv.DictReader(f):
        cls[int(r["page"])] = r["cls"]
agg = {k: collections.Counter() for k in ("logical", "net", "net_hops", "lat", "pages")}
with gzip.open(v4 + "_pages.csv.gz", "rt") as f:
    for r in csv.DictReader(f):
        c = cls.get(int(r["page"]), "未分类")
        agg["logical"][c] += int(r["rd_remote"])
        agg["net"][c] += int(r["net_lines"])
        agg["net_hops"][c] += int(r["net_hops"])
        agg["lat"][c] += float(r["lat_sum_ns"])
        agg["pages"][c] += 1
tot = {}
with open(v4 + "_totals.csv") as f:
    for r in csv.DictReader(f):
        tot[r["key"]] = int(r["value"])
os.makedirs(out, exist_ok=True)
json.dump(dict(label=label, totals=tot, **{k: dict(v) for k, v in agg.items()}),
          open(os.path.join(out, label + "_v4.json"), "w"), ensure_ascii=False, indent=1)
