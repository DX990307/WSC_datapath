#!/usr/bin/env python3
"""A1 main runs on the new baseline: v4_complete_a1 for the 25 workloads at
384 GB/s, W=256, with exactly the arguments of the pgcls v4_complete rows
(minus the pgcls statistics flags), so that the pgcls baseline / v4_complete
results (binary 149dff0 = c08698b + statistics, timing-identical) are the
reference. usage: mkqueue_main2.py <pgcls_queue.tsv (host-prefixed)> <commit> <outdir>"""
import sys
src, commit, out = sys.argv[1:4]
q = {"yss2": [], "yss3": []}
for l in open(src):
    host, pair, est, exp, cfg, args = l.rstrip("\n").split("\t")[:6]
    if cfg != "v4_complete":
        continue
    a = args.split()
    keep, skip = [], False
    for x in a:
        if skip:
            skip = False
            continue
        if x == "-report-pgcls":
            continue
        if x == "-pgcls-sample":
            skip = True
            continue
        if x.startswith("-pgcls-sample="):
            continue
        keep.append(x)
    item = exp.split("_")[2].replace("pgcls-", "a1-")
    e = f"1022_sens_{item}_mi200+v4complete+a1_{commit}"
    q[host].append(f"{pair}\t{est}\t{e}\tv4_complete_a1\t{' '.join(keep)}\t{commit}")
for h, rs in q.items():
    open(f"{out}/queue_main_{h}.tsv", "w").write("\n".join(rs) + "\n")
    print(h, len(rs))
