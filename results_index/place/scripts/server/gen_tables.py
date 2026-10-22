#!/usr/bin/env python3
"""P20: watch done.tsv; when a pass-1 baseline run (..._mi200_<COMMIT>, exit 0) finishes,
classify its pages (pgcls_page.py) and write the pass-2 placement tables (mktable.py) to
lists/. Pass-2 queue rows name a table file in their 7th column, so the scheduler starts
them once the file exists. Exits when every listed workload has tables."""
import os, subprocess, sys, time
D = os.path.expanduser("~/akita-exp-test/place")
COMMIT = "20f323e"
WLS = sys.argv[1:]
while True:
    done = {}
    if os.path.exists(f"{D}/done.tsv"):
        for l in open(f"{D}/done.tsv"):
            p = l.split("\t")
            done[p[0]] = p[1]
    left = 0
    for wl in WLS:
        if os.path.exists(f"{D}/lists/{wl}_tables.json"):
            continue
        left += 1
        exp = f"1022_abl_place-w128b384-{wl}_mi200_{COMMIT}"
        if done.get(exp) != "0":
            continue
        pre = f"{D}/src/results/{exp}/stats/metrics_pgcls"
        try:
            subprocess.run(["python3", "-I", f"{D}/scripts/pgcls_page.py", pre, wl, f"{D}/analysis"],
                           check=True)
            subprocess.run(["python3", "-I", f"{D}/scripts/mktable.py", pre,
                            f"{D}/analysis/{wl}_pages.csv.gz", f"{D}/lists", wl], check=True)
            with open(f"{D}/gen_tables.log", "a") as f:
                f.write(time.strftime("%Y-%m-%dT%H:%M:%S ") + f"tables {wl}\n")
        except Exception as ex:
            with open(f"{D}/gen_tables.log", "a") as f:
                f.write(time.strftime("%Y-%m-%dT%H:%M:%S ") + f"FAILED {wl}: {ex}\n")
            WLS.remove(wl)
    if left == 0:
        break
    time.sleep(120)
