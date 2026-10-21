#!/usr/bin/env python3
"""Run on yss2/yss3 (read-only): python3 - RESULT_DIR... < srv_extract.py
Prints one JSON line per run with the per-GPM / per-link / per-hop statistics
that dm/ needs. Reads only; writes nothing on the server."""
import csv, json, os, sys, re, collections
RD = re.compile(r"GPU\[(\d+)\]\.(RDMA|L2\[\d+\])$")
L2K = {"remote_requester_l2_hits", "remote_useful_two_touch_fills", "remote_l2_two_touch_installed_fills",
       "remote_unused_two_touch_retirements", "remote_installed_fills", "read-hit", "read-miss", "read-mshr-hit",
       "remote_requester_l2_unused_fills", "remote_reuse_boundary_lines_invalidated", "remote_tracked_evictions"}
def fl(x):
    try: return float(x)
    except Exception: return None
def rows(p):
    try: return list(csv.DictReader(open(p)))
    except Exception: return None
for R in sys.argv[1:]:
    R = os.path.expanduser(R)
    S = R + "/stats/"
    o = dict(exp=os.path.basename(R.rstrip("/")))
    try: o["exit"] = json.load(open(R + "/meta.json"))["exit_code"]
    except Exception: o["exit"] = None
    try:
        for r in csv.reader(open(R + "/summary.csv")):
            if r[0] == "total_time" and r[1] == "Driver": o["total_time"] = float(r[2])
    except Exception: pass
    g = collections.defaultdict(lambda: collections.defaultdict(float))
    try:
        for r in csv.reader(open(S + "metrics.csv")):
            if len(r) < 4: continue
            wh = r[1].strip(); k = r[2].strip()
            if wh == "Driver" and k == "total_time": o["total_time_m"] = fl(r[3])
            m = RD.match(wh)
            if not m: continue
            t = int(m[1]); v = fl(r[3])
            if not v: continue
            if m[2] == "RDMA":
                if k.startswith(("t18_", "remote_", "rdma_", "incoming_", "outgoing_")): g[t][k] += v
            elif k in L2K: g[t]["L2:" + k] += v
    except Exception as ex: o["err"] = str(ex)
    o["gpm"] = {t: dict(d) for t, d in g.items()}
    o["links"] = rows(S + "metrics_obs_links.csv")
    o["latency"] = rows(S + "metrics_obs_latency.csv")
    o["occupancy"] = rows(S + "metrics_obs_occupancy.csv")
    o["pairs"] = rows(S + "metrics_obs_pairs.csv")
    o["summary"] = {r["metric"]: fl(r["value"]) for r in (rows(S + "metrics_obs_summary.csv") or [])}
    o["m1_windows"] = rows(S + "metrics_m1_windows.csv")
    o["m1_pairhops"] = rows(S + "metrics_m1_pairhops.csv")
    o["m1_inflight"] = {r["metric"]: fl(r["value"]) for r in (rows(S + "metrics_m1_inflight.csv") or [])}
    gt = rows(S + "metrics_gpm_trace.csv")
    if gt:
        P = collections.defaultdict(lambda: collections.defaultdict(float))
        for r in gt:
            t = int(r["tile"])
            for k in ("l1v_read_local", "l1v_read_remote", "l1v_write_local", "l1v_write_remote", "wg_completed", "l2tlb_miss_remote"):
                P[t][k] += fl(r[k]) or 0
            P[t]["x"] = int(r["x"]); P[t]["y"] = int(r["y"]); P[t]["gpm_index"] = int(r["gpm_index"]); P[t]["driver_id"] = int(r["driver_id"])
        o["gpm_trace"] = {t: dict(d) for t, d in P.items()}
    print(json.dumps(o))
