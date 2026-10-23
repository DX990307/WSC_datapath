#!/usr/bin/env python3
"""one row per run dir under each given results dir (only names matching a filter):
exp exit total_time inst wg pa_* stats (replica reads, replicas on, collapses)."""
import os, sys, json
KEYS = ["pa_replica_on", "pa_replica_reads", "pa_replica_collapse_write", "pa_replica_collapse_kernel",
        "pa_first_touch_pages", "pa_kernels", "pa_copy_bytes"]
filt = sys.argv[1]
print("\t".join(["exp", "exit", "total_time", "inst", "wg"] + KEYS))
for D in sys.argv[2:]:
    D = os.path.expanduser(D)
    for e in sorted(os.listdir(D)):
        if filt not in e:
            continue
        d = os.path.join(D, e)
        try:
            rc = json.load(open(os.path.join(d, "meta.json")))["exit_code"]
        except Exception:
            continue
        m = os.path.join(d, "stats", "metrics.csv")
        tt = wg = ""; ins = 0.0; pa = {}
        if os.path.exists(m):
            for l in open(m):
                p = [x.strip() for x in l.split(",")]
                if len(p) < 4:
                    continue
                if p[1] == "Driver" and p[2] == "total_time":
                    tt = p[3]
                elif p[1] == "Driver" and p[2] == "total_wg_count":
                    wg = str(int(float(p[3])))
                elif p[2] == "cu_inst_count":
                    ins += float(p[3])
                elif p[1] == "PageMigration" and p[2] in KEYS:
                    pa[p[2]] = p[3]
        print("\t".join([e, str(rc), tt, str(int(ins)), wg] + [pa.get(k, "") for k in KEYS]))
