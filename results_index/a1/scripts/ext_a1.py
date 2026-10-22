#!/usr/bin/env python3
"""Run on a server in ~/akita-exp-test/a1: one CSV row per run in src/results
whose name matches argv[1] (substring). Sums the per-component metrics."""
import csv, glob, json, os, re, sys
SUM = ["remote_wire_lines", "remote_demand_wire_lines", "remote_logical_reads",
       "remote_installed_fills", "remote_replica_probe_hits",
       "remote_reuse_boundary_lines_invalidated", "remote_first_touch_admissions",
       "remote_network_request_bytes", "remote_network_response_bytes",
       "a1_lines_checked", "a1_lines_invalidated", "a1_lines_retained",
       "a1_retained_hits", "a1_retained_lines_used", "a1_retained_lines_unused",
       "a1_epochs", "a1_tracker_boundaries", "a1_write_notes", "a1_region_epoch_writes",
       "a1_max_regions_owner_epoch", "a1_max_regions_epoch", "a1_queries",
       "a1_exact_written", "a1_signature_false_positives"]
pat = sys.argv[1] if len(sys.argv) > 1 else ""
w = csv.writer(sys.stdout)
w.writerow(["exp", "exit", "total_time", "kernel_time", "verify", "maxrss_kb", "wall"] + SUM)
for d in sorted(glob.glob("src/results/*")):
    exp = os.path.basename(d)
    if pat not in exp:
        continue
    meta = os.path.join(d, "meta.json")
    rc = json.load(open(meta))["exit_code"] if os.path.exists(meta) else ""
    agg = {k: 0.0 for k in SUM}
    tt = kt = ""
    m = os.path.join(d, "stats", "metrics.csv")
    if os.path.exists(m):
        for r in csv.reader(open(m)):
            if len(r) < 4:
                continue
            k = r[2].strip()
            try:
                v = float(r[3])
            except ValueError:
                continue
            if r[1].strip() == "Driver" and k == "total_time":
                tt = v
            elif r[1].strip() == "Driver" and k == "kernel_time":
                kt = v
            elif k in agg:
                agg[k] = max(agg[k], v) if k.startswith("a1_max") else agg[k] + v
    ver = []
    for f in ("stdout.log", "stderr.log"):
        p = os.path.join(d, f)
        if os.path.exists(p):
            for l in open(p, errors="replace"):
                if ",verify," in l or "Passed!" in l or "verification failed" in l or "Failed" in l:
                    ver.append(l.strip().split(" ")[-1] if "Passed" in l else l.strip())
    rss = wall = ""
    lg = f"runs/{exp}.log"
    if os.path.exists(lg):
        t = open(lg, errors="replace").read()
        mm = re.search(r"Maximum resident set size \(kbytes\): (\d+)", t)
        rss = mm[1] if mm else ""
        mm = re.search(r"Elapsed \(wall clock\) time .*: (\S+)", t)
        wall = mm[1] if mm else ""
    w.writerow([exp, rc, tt, kt, " | ".join(ver)[:300], rss, wall] + [agg[k] for k in SUM])
