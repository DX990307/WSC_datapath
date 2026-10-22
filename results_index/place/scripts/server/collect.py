#!/usr/bin/env python3
"""P20: one row per run in ~/akita-exp-test/place/src/results -> runs tsv on stdout.
exp, exit, total_time_s, inst (sum cu_inst_count), wg (total_wg_count), md5 of the sorted
metrics.csv (regression), placement stats (l1_to_local, l1_to_remote, misrouted), pgcls
remote / local L1V line reads, verify status (stdout)."""
import os, json, hashlib, csv
D = os.path.expanduser("~/akita-exp-test/place/src/results")
print("\t".join(["exp", "exit", "total_time", "inst", "wg", "md5", "l1_local", "l1_remote",
                 "misrouted", "pg_remote_R", "pg_local_R", "verify"]))
for e in sorted(os.listdir(D)):
    d = os.path.join(D, e)
    try:
        rc = json.load(open(os.path.join(d, "meta.json")))["exit_code"]
    except Exception:
        continue
    m = os.path.join(d, "stats", "metrics.csv")
    tt = inst = wg = md5 = ""
    if os.path.exists(m):
        lines = open(m).read().splitlines()
        md5 = hashlib.md5("\n".join(sorted(l.split(",", 1)[1] for l in lines[1:] if "," in l)).encode()).hexdigest()[:12]
        ins = 0.0
        for l in lines[1:]:
            p = [x.strip() for x in l.split(",")]
            if len(p) < 4:
                continue
            if p[1] == "Driver" and p[2] == "total_time":
                tt = p[3]
            elif p[1] == "Driver" and p[2] == "total_wg_count":
                wg = str(int(float(p[3])))
            elif p[2] == "cu_inst_count":
                ins += float(p[3])
        inst = str(int(ins))
    ps = {}
    f = os.path.join(d, "stats", "metrics_place_stats.csv")
    if os.path.exists(f):
        ps = {r["key"]: r["value"] for r in csv.DictReader(open(f))}
    tot = {}
    f = os.path.join(d, "stats", "metrics_pgcls_totals.csv")
    if os.path.exists(f):
        tot = {r["key"]: r["value"] for r in csv.DictReader(open(f))}
    ver = ""
    so = os.path.join(d, "stdout.log")
    if "-verify" in open(os.path.join(d, "meta.json")).read():
        txt = open(so, errors="replace").read() + open(os.path.join(d, "stderr.log"), errors="replace").read()
        low = txt.lower()
        ver = "pass" if (rc == 0 and "passed!" in low) else ("FAIL" if rc != 0 or "panic" in low else "?")
    print("\t".join([e, str(rc), tt, inst, wg, md5, ps.get("l1_to_local", ""), ps.get("l1_to_remote", ""),
                     ps.get("misrouted", ""), tot.get("remote_R_v", ""), tot.get("local_R_v", ""), ver]))
