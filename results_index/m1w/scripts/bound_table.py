#!/usr/bin/env python3
"""bound_table.py <commit> [cols...]: collect metrics_m1w_bound.csv of exit-0 m1wb runs -> bound.csv (one row per run, all metrics)
and print the key columns."""
import csv, glob, json, os, sys
base = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
rows = []
for meta in glob.glob(f"{base}/raw/*/*/meta.json"):
    d = os.path.dirname(meta)
    m = json.load(open(meta))
    if m["exit_code"] != 0 or "_m1wb-" not in m["exp_id"] or not m["exp_id"].endswith("_" + sys.argv[1]):
        continue
    f = f"{d}/stats/metrics_m1w_bound.csv"
    if not os.path.exists(f):
        continue
    exp = m["exp_id"]
    scen, wl = exp.split("_m1wb-")[1].split("_")[0].split("-", 1)
    cfg = exp.split("_")[-2]
    r = dict(exp=exp, scenario=scen, workload=wl, config=cfg)
    for k, v in csv.reader(open(f)):
        if k != "metric":
            r[k] = float(v)
    s = dict((a, b) for a, _, b in csv.reader(open(f"{d}/summary.csv")) if a != "metric")
    r["total_time_us"] = float(s.get("total_time", "nan")) * 1e6
    rows.append(r)
rows.sort(key=lambda r: (r["scenario"], r["config"], r["workload"]))
keys = sorted({k for r in rows for k in r} - {"exp", "scenario", "workload", "config"})
with open(f"{base}/bound.csv", "w", newline="") as f:
    w = csv.writer(f)
    w.writerow(["exp", "scenario", "workload", "config"] + keys)
    for r in rows:
        w.writerow([r["exp"], r["scenario"], r["workload"], r["config"]] + [r.get(k, "") for k in keys])
cols = sys.argv[2:] or ["total_time_us", "lines_per_txn", "oracle_r1_inflight_need_frac", "oracle_r2_inflight_need_frac",
        "oracle_any_inflight_need_frac", "a_q2_d2_ret0_covered_send_frac", "a_q3_d2_ret0_covered_send_frac",
        "a_turn_ret0_covered_send_frac", "a_turn_ret0_covered_verified_frac", "a_path_covered_send_frac", "a_path_covered_verified_frac", "a_q2_d2_ret0_slot_net_frac",
        "a_turn_ret0_slot_net_frac", "a_path_slot_net_frac"]
print("scen cfg wl " + " ".join(cols))
for r in rows:
    print(r["scenario"], r["config"], r["workload"], " ".join(f"{r.get(c, float('nan')):.3g}" for c in cols))
