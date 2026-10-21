#!/usr/bin/env python3
"""v3m2w queue: 6 workloads x 3 new conditions x 4 configs = 72 runs;
`mkqueue.py w2048` adds (W=2048, 384 GB/s): 24 runs -> queue_add_<host>.tsv (appended).
Writes queue_yss2.tsv / queue_yss3.tsv (format of sched.py) and commands.tsv."""
import collections
# item, host, args  (scale = abl3c384 / v3c384 exactly)
W = [
 ("npbcg-W", "yss2", "-benchmark npbcg -cg-class W"),
 ("bfs-s18e2", "yss3", "-benchmark bfs -bfs-scale 18 -bfs-edge-factor 2"),
 ("sssp-s18e4-r1", "yss2", "-benchmark sssp -sssp-scale 18 -sssp-edge-factor 4 -sssp-max-rounds 1"),
 ("pr-g24", "yss3", "-benchmark pagerank-t16small -t16-wg-per-cu 24"),
 ("cfd-c172k-b128", "yss2", "-benchmark altis_cfd -cfd-cells 172032 -cfd-block 128"),
 ("ar-pc1", "yss3", "-benchmark allreduce -ar-per-cu 1"),
]
# condition tag, W, -bandwidth
import sys
ADD = len(sys.argv) > 1 and sys.argv[1] == "w2048"
COND = [("w2048b384", 2048, 24)] if ADD else [("w256b48", 256, 3), ("w64b48", 64, 3), ("w256b384", 256, 24)]
FULL = "-report-cupath-v3-stats -report-obs-all -report-m1-sharing -report-gpm-trace"
CHEAP = "-report-cupath-v3-stats"
CFG = [("mi200", "mi200", 0), ("v3_complete", "mi200+v3complete", 0),
       ("v3_m1m3", "mi200+v3m1m3", 1), ("v3_complete_alloc_shared", "mi200+v3c-allocshared", 1)]
q = collections.defaultdict(list); cmds = []
for phase in (0, 1):
    for tag, w, bw in COND:
        for item, host, args in W:
            for cf, nm, ph in CFG:
                if ph != phase: continue
                net = f"-bandwidth={bw} -switch-buffer-flits=16"
                win = ""
                if w != 64:
                    win = f"-rdma-max-outstanding={w}"
                    if cf != "mi200":
                        win += f" -t18-window={w} -t18-compose-table-entries={2*w}"
                full = cf in ("mi200", "v3_complete") or (
                    cf == "v3_m1m3" and item.split("-")[0] in ("sssp", "bfs"))
                stats = FULL if full else CHEAP
                est = 8 if full else 7
                exp = f"1021_sens_m2w-{tag}-{item}_{nm}_4235991"
                pair = f"{tag}-{item}-p0" if phase == 0 else exp
                a = " ".join(x for x in (args, win, net, stats) if x)
                q[host].append("\t".join([pair, str(est), exp, cf, a]))
                cmds.append("\t".join([tag, item, nm, host, exp,
                    f"akkalat/isca27_run.sh {exp} 4235991 M2W {cf} {a}"]))
for h, rows in q.items():
    open(f"queue{'_add' if ADD else ''}_{h}.tsv", "w").write("\n".join(rows) + "\n")
    print(h, len(rows))
if ADD:
    open("commands.tsv", "a").write("\n".join(cmds) + "\n")
else:
    open("commands.tsv", "w").write("cond\titem\tconfig\thost\texp\tcommand\n" + "\n".join(cmds) + "\n")
