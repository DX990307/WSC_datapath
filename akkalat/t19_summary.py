"""T19 run summary: total time, steals, per-GPM finish spread.

    python3 akkalat/t19_summary.py RESULT_DIR [RESULT_DIR ...]

For every results/<exp_id>/ it prints Driver total_time, the T19 metrics
(when the run had -t19-steal) and, from the T16 per-GPM trace
(stats/metrics_gpm_trace.csv, needs -report-gpm-trace), per unified kernel
the GPMs that ran a WG and the finish spread: for each GPM, finish =
last_wg_end_ns - kernel_launch_ns; spread = max / mean over GPMs that ran a
WG. "weighted" averages the spread over kernels, weighted by kernel time.
Truncated runs (-max-wg) also report the completed WGs.
"""

import csv
import math
import os
import sys


def metrics(path):
    out = {}
    with open(path) as f:
        next(f, None)
        for line in f:
            p = [x.strip() for x in line.split(",")]
            if len(p) >= 4:
                try:
                    out[(p[1], p[2])] = float(p[3])
                except ValueError:
                    pass
    return out


def num(s):
    try:
        return float(s)
    except (TypeError, ValueError):
        return math.nan


def spread(trace):
    kernels = {}
    with open(trace) as f:
        for r in csv.DictReader(f):
            if r["kernel_kind"] != "unified":
                continue
            kernels.setdefault(int(r["kernel"]), []).append(r)
    rows = []
    for k, rs in sorted(kernels.items()):
        launch = num(rs[0]["kernel_launch_ns"])
        end = num(rs[0]["kernel_end_ns"])
        fin = [num(r["last_wg_end_ns"]) - launch for r in rs if int(r["wg_completed"]) > 0]
        fin = [x for x in fin if not math.isnan(x)]
        if not fin:
            continue
        mean = sum(fin) / len(fin)
        rows.append({"kernel": k, "wgs": int(rs[0]["kernel_total_wg"]), "gpms": len(fin),
                     "dur_ns": end - launch if not math.isnan(end) else max(fin),
                     "max_ns": max(fin), "mean_ns": mean,
                     "spread": max(fin) / mean if mean > 0 else math.nan})
    return rows


def main():
    for d in sys.argv[1:]:
        name = os.path.basename(d.rstrip("/"))
        m = metrics(os.path.join(d, "stats", "metrics.csv"))
        tt = m.get(("Driver", "total_time"), math.nan)
        wg = m.get(("Driver", "total_wg_count"), math.nan)
        print(f"== {name}: total_time {tt * 1e6:.3f} us, WGs completed {wg:.0f}")
        t19 = {k[1]: v for k, v in m.items() if k[0] == "T19"}
        if t19:
            keys = ["t19_steals_ok", "t19_steals_nack", "t19_wgs_stolen", "t19_near_steals",
                    "t19_far_steals", "t19_steal_latency_avg_ns", "t19_steal_hops_avg",
                    "t19_finish_max_over_mean_weighted", "t19_filter_decided_steals",
                    "t19_affinity_decided_steals", "t19_overlap_score_avg",
                    "t19_overlap_exact_avg", "t19_rpf_lookups", "t19_rpf_hits",
                    "t19_rpf_false_pos", "t19_groups_recorded", "t19_groups_replaced"]
            print("   " + ", ".join(f"{k[4:]}={t19[k]:.4g}" for k in keys if k in t19))
        trace = os.path.join(d, "stats", "metrics_gpm_trace.csv")
        if os.path.exists(trace):
            rows = spread(trace)
            w = sum(r["dur_ns"] for r in rows)
            ws = sum(r["dur_ns"] * r["spread"] for r in rows) / w if w else math.nan
            print(f"   kernels {len(rows)}, finish spread (time-weighted) {ws:.3f}")
            for r in rows:
                print(f"   k{r['kernel']}: WGs {r['wgs']}, GPMs used {r['gpms']}, "
                      f"time {r['dur_ns'] / 1e3:.2f} us, spread {r['spread']:.3f}")


if __name__ == "__main__":
    main()
