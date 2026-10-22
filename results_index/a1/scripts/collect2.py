#!/usr/bin/env python3
"""Pull all A1 main-condition results (384 GB/s, W=256) and the pgcls references;
write a1.csv (one row per workload x config). Numbers only from exit-0 runs.
usage: collect2.py <out.csv>"""
import csv, io, math, subprocess, sys
OUT = sys.argv[1]
CFG = {"mi200": "baseline", "mi200+v4complete": "v4", "mi200+v4complete+a1": "v4+A1",
       "mi200+v4complete+a1+ft1": "v4+A1+ft1", "mi200+v4complete+a1+ft2": "v4+A1+ft2",
       "mi200+v4complete+ft2": "v4+ft2", "mi200+v4complete+a1u": "v4+A1+reset",
       "mi200+v4complete+a1u+ft2": "v4+A1+reset+ft2"}
def ext(host, d, pat):
    r = subprocess.run(["ssh", host, f"cd ~/akita-exp-test/{d} && python3 ~/akita-exp-test/a1/ext_a1.py {pat}"],
                       capture_output=True, text=True, check=True).stdout
    return list(csv.DictReader(io.StringIO(r)))
runs = []
for h in ("yss2", "yss3"):
    for d, pat in (("a1", "1022_sens_a1-w256b384"), ("pgcls", "1022_diag_pgcls-w256b384")):
        for r in ext(h, d, pat):
            _, _, item, cfg, commit = r["exp"].split("_")
            item = item.replace("a1-w256b384-", "").replace("pgcls-w256b384-", "")
            if "-or-" in item or cfg not in CFG:
                continue
            r.update(host=h, workload=item, config=CFG[cfg], commit=commit, source=d)
            runs.append(r)
def num(x):
    try: return float(x)
    except (TypeError, ValueError): return float("nan")
by = {}
for r in runs:
    by.setdefault(r["workload"], {})[r["config"]] = r
cols = ["workload", "config", "exit", "total_time", "speedup_vs_baseline", "speedup_vs_v4",
        "remote_logical_reads", "remote_wire_lines", "wire_lines_vs_v4", "remote_replica_probe_hits",
        "remote_installed_fills", "remote_first_touch_admissions", "a1_lines_invalidated", "a1_lines_retained",
        "a1_retained_hits", "a1_retained_lines_used", "a1_retained_lines_unused", "a1_epochs",
        "a1_region_epoch_writes", "a1_max_regions_owner_epoch", "a1_max_regions_epoch",
        "maxrss_kb", "wall", "host", "source", "commit", "exp"]
rows = []
for w in sorted(by):
    d = by[w]
    ok = lambda c: c in d and d[c]["exit"] == "0" and d[c]["total_time"]
    bt = num(d["baseline"]["total_time"]) if ok("baseline") else float("nan")
    vt = num(d["v4"]["total_time"]) if ok("v4") else float("nan")
    vw = num(d["v4"]["remote_wire_lines"]) if ok("v4") else float("nan")
    for c in CFG.values():
        if c not in d:
            continue
        r = d[c]
        t = num(r["total_time"]) if ok(c) else float("nan")
        row = {k: r.get(k, "") for k in cols}
        row.update(config=c, total_time=t, speedup_vs_baseline=bt / t, speedup_vs_v4=vt / t,
                   wire_lines_vs_v4=num(r["remote_wire_lines"]) / vw if ok(c) and vw else float("nan"))
        rows.append(row)
with open(OUT, "w", newline="") as fh:
    w = csv.DictWriter(fh, fieldnames=cols); w.writeheader(); w.writerows(rows)
def gm(xs):
    xs = [x for x in xs if x == x]
    return (math.exp(sum(map(math.log, xs)) / len(xs)), len(xs)) if xs else (float("nan"), 0)
for c in CFG.values():
    sp = {r["workload"]: r["speedup_vs_baseline"] for r in rows if r["config"] == c}
    sv = {r["workload"]: r["speedup_vs_v4"] for r in rows if r["config"] == c}
    print(f"{c:12s} vs baseline GMEAN {gm(sp.values())[0]:.3f} (n={gm(sp.values())[1]})  vs v4 {gm(sv.values())[0]:.4f} (n={gm(sv.values())[1]})")
