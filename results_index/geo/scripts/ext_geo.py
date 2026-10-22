#!/usr/bin/env python3
"""ext_geo.py RESULT_DIR... -> one JSON line per run (run on the server)."""
import csv, json, os, sys, collections

SUM = ["remote_wire_lines", "remote_demand_wire_lines", "remote_prefetch_wire_lines",
       "remote_logical_reads", "remote_l2_probe_hits", "remote_logical_read_latency_total_ns",
       "remote_logical_read_latency_samples", "t18_req_sent", "t18_v3_window_full_ns",
       "remote_network_response_bytes", "remote_network_request_bytes"]
HOT = {"0->7": ("Mesh.SW[0][0][0].Bottom", "Mesh.SW[0][1][0].Top"),
       "48->41": ("Mesh.SW[6][6][0].Top", "Mesh.SW[6][5][0].Bottom")}

def one(d):
    r = {"exp": os.path.basename(d.rstrip("/"))}
    try:
        r["exit"] = json.load(open(f"{d}/meta.json"))["exit_code"]
    except Exception:
        r["exit"] = None
    try:
        for row in csv.reader(open(f"{d}/summary.csv")):
            if row[0] == "total_time":
                r["total_time"] = float(row[2])
    except Exception:
        pass
    sums = collections.Counter()
    try:
        with open(f"{d}/stats/metrics.csv") as f:
            next(f)
            for line in f:
                p = [x.strip() for x in line.split(",")]
                if len(p) < 4:
                    continue
                name = p[2]
                try:
                    v = float(p[3])
                except ValueError:
                    continue
                if name.startswith("geo_push") or name.startswith("geo_bound_") or name in SUM:
                    sums[name] += v
    except Exception:
        pass
    r.update(sums)
    lk = f"{d}/stats/metrics_obs_links.csv"
    if os.path.exists(lk):
        tot = 0.0; mx = 0.0; mxw = 0.0; mxl = ""
        for row in csv.DictReader(open(lk)):
            b = float(row["bytes"]); tot += b
            u = float(row["util"])
            if u > mx:
                mx = u; mxl = row["link_src"] + ">" + row["link_dst"]
            mxw = max(mxw, float(row["max_window_util"]))
            for k, (s, t) in HOT.items():
                if row["link_src"] == s and row["link_dst"] == t:
                    r[f"link_{k}_util"] = u
                    r[f"link_{k}_bytes"] = b
        r.update(mesh_bytes=tot, link_max_util=mx, link_max=mxl, link_max_window_util=mxw)
    return r

for d in sys.argv[1:]:
    print(json.dumps(one(d)))
