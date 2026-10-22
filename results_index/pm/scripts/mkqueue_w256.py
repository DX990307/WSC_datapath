#!/usr/bin/env python3
"""pm on the new baseline (c08698b merged: 15beab2), 384 GB/s, W=256.
25 workloads x {c4k, f4k, f4kideal, c4k thr 64, v4_complete+c4k, v4_complete+f4k}.
Writes queue_w256_<host>.tsv and commands_w256.tsv (format of sched.py)."""
import collections, re
src = open("mkqueue.py").read()
ns = {}
exec(src[src.index("W = ["):src.index("EST =")], ns)   # W and EXT lists
W = ns["W"] + ns["EXT"]
COMMIT = "15beab2"
NET = "-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=256"
V4W = "-t18-window=256 -t18-compose-table-entries=512"
CFG = [  # tag, base, base name, overlays, extra, est
    ("pmc4k", "mi200", "mi200", "pm_counter_4k", "", 5),
    ("pmf4k", "mi200", "mi200", "pm_fault_4k", "", 5),
    ("pmf4kideal", "mi200", "mi200", "pm_fault_4k,pm_ideal", "", 5),
    ("pmc4kt64", "mi200", "mi200", "pm_counter_4k", "-pm-threshold 64", 5),
    ("pmc4k", "v4_complete", "v4complete", "pm_counter_4k", V4W, 8),
    ("pmf4k", "v4_complete", "v4complete", "pm_fault_4k", V4W, 8),
]
q = collections.defaultdict(list); cmds = []
for tag, base, bnm, ov, extra, est in CFG:
    for item, host, args in W:
        exp = f"1022_abl_pm-w256b384-{item}_{bnm}+{tag}_{COMMIT}"
        cfg = f"{base},{ov}"
        a = " ".join(x for x in (args, NET, extra) if x)
        q[host].append("\t".join([exp, str(est), exp, cfg, a]))
        cmds.append("\t".join([item, bnm, tag, host, exp,
                     f"akkalat/isca27_run.sh {exp} {COMMIT} PM {cfg} {a}"]))
for h, r in q.items():
    open(f"queue_w256_{h}.tsv", "w").write("\n".join(r) + "\n")
    print(h, len(r))
open("commands_w256.tsv", "w").write("item\tbase\ttag\thost\texp\tcommand\n" + "\n".join(cmds) + "\n")
