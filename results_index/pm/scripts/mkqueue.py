#!/usr/bin/env python3
"""pm campaign queue (page-migration baseline), format of sched.py:
  pair<TAB>est_rss_gb<TAB>exp_id<TAB>configs<TAB>bench args
Phase 1: 9 workloads x 9 configs on mi200 (observe, 4 variants, 4 ideal).
`mkqueue.py <phase>` with phase in {1, complete, sens} writes queue_<phase>_<host>.tsv
and appends to commands.tsv."""
import sys, collections
COMMIT = "c74ef96"
# item, host, args (scale = abl3c384 / m2w exactly)
W = [
 ("npbcg-W", "yss2", "-benchmark npbcg -cg-class W"),
 ("pr-g24", "yss3", "-benchmark pagerank-t16small -t16-wg-per-cu 24"),
 ("bfs-s18e2", "yss3", "-benchmark bfs -bfs-scale 18 -bfs-edge-factor 2"),
 ("sssp-s18e4-r1", "yss2", "-benchmark sssp -sssp-scale 18 -sssp-edge-factor 4 -sssp-max-rounds 1"),
 ("cfd-c172k-b128", "yss2", "-benchmark altis_cfd -cfd-cells 172032 -cfd-block 128"),
 ("ar-pc1", "yss3", "-benchmark allreduce -ar-per-cu 1"),
 ("mm-g12", "yss2", "-benchmark matrixmultiplication-t16small"),
 ("tc-s12e16", "yss3", "-benchmark graphtc -tc-scale 12 -tc-edge-factor 16"),
 ("atax-2688x8192-r64", "yss2", "-benchmark w2-atax -atax-nx 2688 -atax-ny 8192 -atax-rows-per-chunk 64"),
]
# user 10-21: the other 16 workloads of abl3c384 (args = v3c384/queue.tsv mi200 rows)
EXT = [
 ("moe-t64", "yss2", "-benchmark moe -moe-tokens=64 -moe-hidden=1024 -moe-ffn=704 -moe-topk=6 -moe-skew=1.75"),
 ("mg-W", "yss3", "-benchmark npb_mg -mg-class W"),
 ("lud-n1024", "yss3", "-benchmark rodinia_lud -lud-n 1024"),
 ("pa-b14s7p32", "yss2", "-benchmark pagedattn -pa-batch=14 -pa-seed=7 -pa-part-tokens=32"),
 ("gups-L18m8", "yss3", "-benchmark gups -gups-log-table 18 -gups-update-mult 8"),
 ("fw-n2048-p4", "yss2", "-benchmark floydwarshall-p12small -p12-fw-nodes=2048 -p12-fw-passes=4"),
 ("fft2d-n1024", "yss3", "-benchmark w2-fft2d -fft2d-n 1024"),
 ("bh-n86k", "yss2", "-benchmark lonestar_bh -bh-bodies 86016"),
 ("ivf-n8k-q328", "yss3", "-benchmark ivfflat -ivf-n 8192 -ivf-queries 328"),
 ("km-g1", "yss2", "-benchmark kmeans-t16small -t16-wg-per-cu 1"),
 ("mt-g4", "yss3", "-benchmark matrixtranspose-t16small"),
 ("i2c-g64", "yss2", "-benchmark im2col-t16small -t16-wg-per-cu 64"),
 ("shocsort-n688k", "yss3", "-benchmark w2-shocsort -shocsort-n 688128"),
 ("npbis-n1m-i1", "yss2", "-benchmark w2-npbis -npbis-n 1048576 -npbis-iterations 1"),
 ("embbag-b512", "yss3", "-benchmark embbag -eb-batch 512"),
 ("spmv-g32", "yss2", "-benchmark spmv-t16small -t16-wg-per-cu 32"),
]
EST = "5"  # measured peak RSS of phase-1 runs ~5.1 GB (sched reserves 1.3x)
NET = "-bandwidth=24 -switch-buffer-flits=16"
STATS = "-report-cupath-v3-stats"
V = [  # tag, config overlays
 ("pmobs", "pm_observe"),
 ("pmf4k", "pm_fault_4k"), ("pmf2m", "pm_fault_2m"),
 ("pmc4k", "pm_counter_4k"), ("pmc2m", "pm_counter_2m"),
 ("pmf4kideal", "pm_fault_4k,pm_ideal"), ("pmf2mideal", "pm_fault_2m,pm_ideal"),
 ("pmc4kideal", "pm_counter_4k,pm_ideal"), ("pmc2mideal", "pm_counter_2m,pm_ideal"),
]
phase = sys.argv[1] if len(sys.argv) > 1 else "1"
rows = []  # (host, base, base_nm, tag, overlays, extra)
if phase in ("1", "ext"):
    for item, host, args in (W if phase == "1" else EXT):
        for tag, ov in V:
            rows.append((host, item, args, "mi200", "mi200", tag, ov, ""))
elif phase == "complete":
    best = sys.argv[2:]  # e.g. pmc2m pm_counter_2m
    tag, ov = best[0], best[1]
    for item, host, args in W + EXT:
        rows.append((host, item, args, "v3_complete", "v3complete", tag, ov, ""))
elif phase == "sens":
    # sys.argv[2:]: tag overlays extra... given as tag|overlays|extra triples
    for spec in sys.argv[2:]:
        tag, ov, extra = spec.split("|")
        for item, host, args in W + EXT:
            rows.append((host, item, args, "mi200", "mi200", tag, ov, extra))
q = collections.defaultdict(list); cmds = []
for host, item, args, base, bnm, tag, ov, extra in rows:
    exp = f"1021_abl_pm-{item}_{bnm}+{tag}_{COMMIT}"
    cfg = f"{base},{ov}"
    a = " ".join(x for x in (args, extra, NET, STATS) if x)
    q[host].append("\t".join([exp, EST, exp, cfg, a]))
    cmds.append("\t".join([phase, item, tag, host, exp,
                 f"akkalat/isca27_run.sh {exp} {COMMIT} PM {cfg} {a}"]))
for h, r in q.items():
    open(f"queue_{phase}_{h}.tsv", "w").write("\n".join(r) + "\n")
    print(h, len(r))
new = not __import__("os").path.exists("commands.tsv")
with open("commands.tsv", "a") as f:
    if new:
        f.write("phase\titem\ttag\thost\texp\tcommand\n")
    f.write("\n".join(cmds) + "\n")
