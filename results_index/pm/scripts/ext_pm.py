#!/usr/bin/env python3
"""Per-run extraction for the pm campaign (run on the server).
usage: ext_pm.py RESULT_DIR... -> one JSON object per line.
Only meta.json exit_code, summary.csv / stats/metrics.csv are read."""
import csv, json, os, sys, collections


def f(x):
    try:
        return float(x)
    except Exception:
        return None


for R in sys.argv[1:]:
    e = os.path.basename(R.rstrip("/"))
    o = dict(exp=e)
    try:
        m = json.load(open(R + "/meta.json"))
        o["exit"] = m.get("exit_code")
        o["host"] = m.get("host")
        o["start"], o["end"] = m.get("start"), m.get("end")
    except Exception:
        o["exit"] = None
    S = collections.defaultdict(float)
    served = {}
    try:
        for r in csv.reader(open(R + "/stats/metrics.csv")):
            if len(r) < 4:
                continue
            wh, k, v = r[1].strip(), r[2].strip(), f(r[3])
            if v is None:
                continue
            if wh == "Driver" and k in ("total_time", "kernel_time"):
                o[k] = v
            elif wh == "PageMigration":
                o[k] = v
            elif k == "pm_remote_reads_served":
                served[wh] = v
            else:
                S[k] += v
    except Exception as ex:
        o["err"] = str(ex)
        print(json.dumps(o))
        continue
    o["inst"] = S.get("cu_inst_count", 0.0)
    o["wgs"] = S.get("cu_wg_count", 0.0)
    o["rdma_observed_remote_reads"] = S.get("rdma_observed_remote_reads", 0.0)
    tot = sum(served.values())
    if tot:
        top = sorted(served.items(), key=lambda x: -x[1])
        o["owner_top1"] = top[0][0]
        o["owner_top1_share"] = top[0][1] / tot
        o["owner_top3_share"] = sum(v for _, v in top[:3]) / tot
        o["owner_n_served"] = len(served)
    print(json.dumps(o))
