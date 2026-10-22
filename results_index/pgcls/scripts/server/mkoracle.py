#!/usr/bin/env python3
"""Append the per-class oracle runs to queue.tsv (run on the server in ~/akita-exp-test/pgcls).
For every item whose baseline analysis exists (analysis/lists/<item>.txt: "<key> <share> <pages>"):
  v4_complete + -pgcls-oracle-pages lists/<item>__<key>.txt   for classes with share >= 5%
  v4_complete + -force-local-data-access                      (all pages local)
  mi200       + -force-local-data-access
Items/classes already in queue.tsv are skipped. usage: mkoracle.py COMMIT"""
import os, sys
C = sys.argv[1]
D = os.path.expanduser("~/akita-exp-test/pgcls")
A = f"{D}/analysis"
base = {}
for l in open(f"{D}/queue.tsv"):
    p = l.rstrip("\n").split("\t")
    if len(p) == 5 and "_mi200_" in p[2] and "-or-" not in p[2]:
        item = p[2].split("pgcls-w256b384-")[1].rsplit("_mi200_", 1)[0]
        base[item] = (float(p[1]), p[4])
have = {l.split("\t")[2] for l in open(f"{D}/queue.tsv") if l.strip()}
NET = "-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=256"
V4 = "-t18-window=256 -t18-compose-table-entries=512"
rows = []
for item, (est, args) in sorted(base.items()):
    lf = f"{A}/lists/{item}.txt"
    if not os.path.exists(lf):
        continue
    bench = args.split(" -bandwidth=")[0]
    jobs = []
    for l in open(lf):
        key, share, n = l.split()
        jobs.append((f"or-{key}", "v4_complete", "mi200+v4complete",
                     f"{V4} -pgcls-oracle-pages {A}/lists/{item}__{key}.txt"))
    jobs.append(("or-all", "v4_complete", "mi200+v4complete", f"{V4} -force-local-data-access"))
    jobs.append(("or-all", "mi200", "mi200", "-force-local-data-access"))
    for tag, cf, nm, extra in jobs:
        exp = f"1022_diag_pgcls-w256b384-{item}-{tag}_{nm}_{C}"
        if exp in have:
            continue
        a = f"{bench} {NET} {extra} -report-pgcls"
        rows.append("\t".join([exp, str(est + 2), exp, cf, a]))
with open(f"{D}/queue.tsv", "a") as f:
    for r in rows:
        f.write(r + "\n")
print(len(rows), "oracle rows appended")
