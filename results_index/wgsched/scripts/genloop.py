#!/usr/bin/env python3
"""Generate pass-2 WG maps as pass-1 runs finish (wg-to-data)."""
import os, subprocess, time, json
D = os.path.expanduser("~/akita-exp-test/wgsched")
while True:
    pend = 0
    for l in open(f"{D}/pass1.tsv"):
        wl, exp = l.split()
        out = f"{D}/maps/{wl}.map"
        if os.path.exists(out):
            continue
        meta = f"{D}/src/results/{exp}/meta.json"
        if not os.path.exists(meta):
            pend += 1
            continue
        if not os.path.exists(f"{D}/src/results/{exp}/stats/metrics_wgaff.csv"):
            if not os.path.exists(out + ".failed"):
                open(out + ".failed", "w").write("no wgaff\n")
            continue
        if os.path.exists(out + ".failed"):
            continue
        os.makedirs(f"{D}/maps", exist_ok=True)
        r = subprocess.run(["nice", "-n", "10", "python3", f"{D}/scripts/gen_maps.py",
                            f"{D}/src/results/{exp}", out], capture_output=True, text=True)
        with open(f"{D}/genloop.log", "a") as f:
            f.write(f"{time.strftime('%H:%M:%S')} {wl} rc={r.returncode} {r.stderr[-500:]}\n")
        if r.returncode != 0:
            open(out + ".failed", "w").write(r.stderr)
    if pend == 0:
        break
    time.sleep(60)
