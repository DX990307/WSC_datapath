#!/usr/bin/env python3
"""speedup.py <commit> [<commit> ...]: collect exit-0 w256b384 runs of <commit> -> runs.csv (one row per run) and
speedup.csv (per workload: T_v4complete / T_variant, and T_baseline / T_variant). Repeats (-rep2) are
listed apart and give the run-to-run noise."""
import csv, glob, json, math, os, sys
CS = sys.argv[1:]  # later commits override earlier ones for the same (workload, config)
base = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
runs = []
for meta in glob.glob(f"{base}/raw/*/*/meta.json"):
    m = json.load(open(meta))
    e = m["exp_id"]
    c = e.rsplit("_", 1)[1]
    if c not in CS or "w256b384" not in e:
        continue
    d = os.path.dirname(meta)
    s = {a: b for a, _, b in csv.reader(open(f"{d}/summary.csv")) if a != "metric"}
    wl = e.split("w256b384-")[1].split("_")[0]
    cfg = e.split("_")[-2].replace("mi200+", "")
    t = float(s["total_time"]) * 1e6 if m["exit_code"] == 0 and "total_time" in s else None
    runs.append(dict(exp=e, host=d.split("/")[-2], commit=c, workload=wl, config=cfg,
                     exit=m["exit_code"], total_time_us=t))
runs.sort(key=lambda r: (r["workload"], r["config"], CS.index(r["commit"])))
with open(f"{base}/runs.csv", "w", newline="") as f:
    w = csv.DictWriter(f, fieldnames=list(runs[0].keys()))
    w.writeheader(); w.writerows(runs)
T = {}
for r in runs:  # sorted by commit order: later commits override
    if r["total_time_us"]:
        T[(r["workload"], r["config"])] = r["total_time_us"]
wls = sorted({r["workload"] for r in runs})
cfgs = ["v4hubturn", "v4hubq2", "v4hubturna", "v4hold100", "v4holdrtt25", "v4hold100o", "v4hubturnhold", "v4hubturnahold"]
rows = []
for wl in wls:
    v4 = T.get((wl, "v4complete")); b = T.get((wl, "mi200"))
    row = dict(workload=wl, baseline_us=b, v4complete_us=v4,
               v4_over_baseline=(b / v4 if b and v4 else None),
               v4_rep2_ratio=(T[(wl, "v4complete-rep2")] / v4 if v4 and (wl, "v4complete-rep2") in T else None))
    for c in cfgs:
        t = T.get((wl, c))
        row[c] = v4 / t if v4 and t else None
    if (wl, "v4hubturn-rep2") in T and v4:
        row["v4hubturn_rep2"] = v4 / T[(wl, "v4hubturn-rep2")]
    rows.append(row)
def gm(xs):
    xs = [x for x in xs if x]
    return math.exp(sum(map(math.log, xs)) / len(xs)) if xs else None
g = dict(workload="GMEAN", v4_over_baseline=gm([r["v4_over_baseline"] for r in rows]))
for c in cfgs:
    g[c] = gm([r[c] for r in rows])
rows.append(g)
keys = ["workload", "baseline_us", "v4complete_us", "v4_over_baseline", "v4_rep2_ratio"] + cfgs + ["v4hubturn_rep2"]
with open(f"{base}/speedup.csv", "w", newline="") as f:
    w = csv.DictWriter(f, fieldnames=keys)
    w.writeheader()
    for r in rows:
        w.writerow({k: (f"{r[k]:.4f}" if isinstance(r.get(k), float) else r.get(k, "")) for k in keys})
fmt = lambda v: f"{v:.3f}" if isinstance(v, float) else ("-" if v is None else str(v))
print(" ".join(f"{k:>13}" for k in keys))
for r in rows:
    print(" ".join(f"{fmt(r.get(k)):>13}" for k in keys))
