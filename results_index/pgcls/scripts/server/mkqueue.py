#!/usr/bin/env python3
"""pgcls queue: 25 workloads x {baseline (mi200), v4_complete}, 384 GB/s, W=256, -report-pgcls.
usage: mkqueue.py COMMIT  -> queue_yss2.tsv, queue_yss3.tsv, commands.tsv (format of sched.py)."""
import sys
C = sys.argv[1]
# item, host, est_rss_gb, args (= v3c384 queue.tsv mi200 rows)
W = [
 ("mg-W", "yss2", 12, "-benchmark npb_mg -mg-class W"),
 ("lud-n1024", "yss2", 6, "-benchmark rodinia_lud -lud-n 1024"),
 ("mm-g12", "yss2", 6, "-benchmark matrixmultiplication-t16small"),
 ("gups-L18m8", "yss2", 6, "-benchmark gups -gups-log-table 18 -gups-update-mult 8"),
 ("tc-s12e16", "yss2", 6, "-benchmark graphtc -tc-scale 12 -tc-edge-factor 16"),
 ("mt-g4", "yss2", 6, "-benchmark matrixtranspose-t16small"),
 ("atax-2688x8192-r64", "yss2", 6, "-benchmark w2-atax -atax-nx 2688 -atax-ny 8192 -atax-rows-per-chunk 64"),
 ("i2c-g64", "yss2", 6, "-benchmark im2col-t16small -t16-wg-per-cu 64"),
 ("fft2d-n1024", "yss2", 6, "-benchmark w2-fft2d -fft2d-n 1024"),
 ("ivf-n8k-q328", "yss2", 6, "-benchmark ivfflat -ivf-n 8192 -ivf-queries 328"),
 ("embbag-b512", "yss2", 6, "-benchmark embbag -eb-batch 512"),
 ("sssp-s18e4-r1", "yss2", 10, "-benchmark sssp -sssp-scale 18 -sssp-edge-factor 4 -sssp-max-rounds 1"),
 ("npbcg-W", "yss3", 6, "-benchmark npbcg -cg-class W"),
 ("bh-n86k", "yss3", 6, "-benchmark lonestar_bh -bh-bodies 86016"),
 ("pr-g24", "yss3", 6, "-benchmark pagerank-t16small -t16-wg-per-cu 24"),
 ("fw-n2048-p4", "yss3", 6, "-benchmark floydwarshall-p12small -p12-fw-nodes=2048 -p12-fw-passes=4"),
 ("bfs-s18e2", "yss3", 10, "-benchmark bfs -bfs-scale 18 -bfs-edge-factor 2"),
 ("pa-b14s7p32", "yss3", 6, "-benchmark pagedattn -pa-batch=14 -pa-seed=7 -pa-part-tokens=32"),
 ("spmv-g32", "yss3", 6, "-benchmark spmv-t16small -t16-wg-per-cu 32"),
 ("km-g1", "yss3", 6, "-benchmark kmeans-t16small -t16-wg-per-cu 1"),
 ("cfd-c172k-b128", "yss3", 6, "-benchmark altis_cfd -cfd-cells 172032 -cfd-block 128"),
 ("npbis-n1m-i1", "yss3", 6, "-benchmark w2-npbis -npbis-n 1048576 -npbis-iterations 1"),
 ("ar-pc1", "yss3", 6, "-benchmark allreduce -ar-per-cu 1"),
 ("shocsort-n688k", "yss3", 6, "-benchmark w2-shocsort -shocsort-n 688128"),
 ("moe-t64", "yss3", 6, "-benchmark moe -moe-tokens=64 -moe-hidden=1024 -moe-ffn=704 -moe-topk=6 -moe-skew=1.75"),
]
NET = "-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=256"
STATS = "-report-pgcls -pgcls-sample 16"
CFG = [("mi200", "mi200", ""), ("v4_complete", "mi200+v4complete",
        "-t18-window=256 -t18-compose-table-entries=512")]
q = {"yss2": [], "yss3": []}; cmds = []
for item, host, est, args in W:
    for cf, nm, extra in CFG:
        exp = f"1022_diag_pgcls-w256b384-{item}_{nm}_{C}"
        a = " ".join(x for x in (args, NET, extra, STATS) if x)
        e = est if cf == "mi200" else est + 2
        q[host].append("\t".join([f"{item}-p0", str(e), exp, cf, a]))
        cmds.append("\t".join([item, cf, host, exp, f"akkalat/isca27_run.sh {exp} {C} PGCLS {cf} {a}"]))
for h, rows in q.items():
    open(f"queue_{h}.tsv", "w").write("\n".join(rows) + "\n")
open("commands.tsv", "w").write("\n".join(cmds) + "\n")
