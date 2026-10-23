#!/usr/bin/env python3
"""Compare a pass-2 run with its pass-1 (baseline) run: totals and WG spread."""
import sys, os, re, collections, glob
R = os.path.expanduser("~/akita-exp-test/wgsched/src/results")
def load(e):
    tot = collections.Counter(); gpm_wg = collections.Counter(); cu_wg = []; kt = {}
    for l in open(f"{R}/{e}/stats/metrics.csv"):
        p = l.rstrip("\n").split(", ")
        if len(p) < 4: continue
        where, what = p[1], p[2]
        try: v = float(p[3])
        except: continue
        if what in ("cu_inst_count", "cu_wg_count", "rdma_observed_remote_reads",
                    "rdma_observed_remote_writes", "local_req_count", "read_trans_count",
                    "dram_frontend_read_requests"):
            tot[what] += v
        if what == "cu_wg_count":
            m = re.search(r"GPU\[(\d+)\]", where)
            gpm_wg[m.group(1) if m else where] += v; cu_wg.append(v)
        if what == "kernel_time" and where != "Driver":
            kt[where] = v
        if what == "total_time": tot["total_time"] = v
    return tot, gpm_wg, cu_wg, kt
for wl in sys.argv[1:]:
    p1 = f"1022_place_wgs-p1-{wl}_mi200+wgaff_a739ac3"
    if not os.path.exists(f"{R}/{p1}/meta.json"): continue
    base = load(p1)
    for e in sorted(glob.glob(f"{R}/1022_place_wgs-{wl}_mi200+*")):
        e = os.path.basename(e)
        if not os.path.exists(f"{R}/{e}/meta.json"): continue
        x = load(e)
        print(f"== {wl} {e.split('_')[3]} vs pass1")
        for k in ["total_time", "cu_inst_count", "cu_wg_count", "rdma_observed_remote_reads", "rdma_observed_remote_writes", "local_req_count", "dram_frontend_read_requests"]:
            a, b = base[0][k], x[0][k]
            print(f"  {k:30s} {a:14.6g} {b:14.6g}  ratio {b/a if a else float('nan'):.3f}")
        for name, t in (("p1", base), ("p2", x)):
            g = list(t[1].values()); c = t[2]; kt = list(t[3].values())
            ag = sum(g)/len(g); ac = sum(c)/len(c)
            print(f"  {name}: GPM WG max/avg {max(g)/ag:.2f} min/avg {min(g)/ag:.2f} (n={len(g)}); CU WG max/avg {max(c)/ac:.2f} idleCU {sum(1 for v in c if v==0)}; GPM kernel_time max/mean {max(kt)/(sum(kt)/len(kt)) if kt and sum(kt) else 0:.2f}")
