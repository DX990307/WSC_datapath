# workloads: item, est wall h (baseline @384 guess), host, args
W = [
 ("mg-W", 2.0, "yss2", "-benchmark npb_mg -mg-class W"),
 ("npbcg-W", 2.0, "yss3", "-benchmark npbcg -cg-class W"),
 ("lud-n1024", 1.3, "yss2", "-benchmark rodinia_lud -lud-n 1024"),
 ("bh-n86k", 1.0, "yss3", "-benchmark lonestar_bh -bh-bodies 86016"),
 ("mm-g12", 1.1, "yss2", "-benchmark matrixmultiplication-t16small"),
 ("pr-g24", 1.0, "yss3", "-benchmark pagerank-t16small -t16-wg-per-cu 24"),
 ("gups-L18m8", 0.9, "yss2", "-benchmark gups -gups-log-table 18 -gups-update-mult 8"),
 ("fw-n2048-p4", 0.9, "yss3", "-benchmark floydwarshall-p12small -p12-fw-nodes=2048 -p12-fw-passes=4"),
 ("tc-s12e16", 0.9, "yss2", "-benchmark graphtc -tc-scale 12 -tc-edge-factor 16"),
 ("bfs-s18e2", 0.9, "yss3", "-benchmark bfs -bfs-scale 18 -bfs-edge-factor 2"),
 ("mt-g4", 0.8, "yss2", "-benchmark matrixtranspose-t16small"),
 ("spmv-g32", 0.7, "yss3", "-benchmark spmv-t16small -t16-wg-per-cu 32"),
 ("i2c-g64", 0.7, "yss2", "-benchmark im2col-t16small -t16-wg-per-cu 64"),
 ("km-g1", 0.7, "yss3", "-benchmark kmeans-t16small -t16-wg-per-cu 1"),
 ("atax-2688x8192-r64", 0.8, "yss2", "-benchmark w2-atax -atax-nx 2688 -atax-ny 8192 -atax-rows-per-chunk 64"),
 ("pa-b14s7p32", 0.8, "yss3", "-benchmark pagedattn -pa-batch=14 -pa-seed=7 -pa-part-tokens=32"),
 ("fft2d-n1024", 0.7, "yss2", "-benchmark w2-fft2d -fft2d-n 1024"),
 ("cfd-c172k-b128", 0.7, "yss3", "-benchmark altis_cfd -cfd-cells 172032 -cfd-block 128"),
 ("ivf-n8k-q328", 0.7, "yss2", "-benchmark ivfflat -ivf-n 8192 -ivf-queries 328"),
 ("npbis-n1m-i1", 0.6, "yss3", "-benchmark w2-npbis -npbis-n 1048576 -npbis-iterations 1"),
 ("embbag-b512", 0.6, "yss2", "-benchmark embbag -eb-batch 512"),
 ("ar-pc1", 0.6, "yss3", "-benchmark allreduce -ar-per-cu 1"),
 ("sssp-s18e4-r1", 0.5, "yss2", "-benchmark sssp -sssp-scale 18 -sssp-edge-factor 4 -sssp-max-rounds 1"),
 ("shocsort-n688k", 0.5, "yss3", "-benchmark w2-shocsort -shocsort-n 688128"),
 ("moe-t64", 0.5, "yss3", "-benchmark moe -moe-tokens=64 -moe-hidden=1024 -moe-ffn=704 -moe-topk=6 -moe-skew=1.75"),
]
NET = "-bandwidth=24 -switch-buffer-flits=16"
FULL = "-report-cupath-v3-stats -report-obs-all -report-m1-sharing -report-gpm-trace"
CHEAP = "-report-cupath-v3-stats"
CFG = [  # (config file, name suffix, phase)
 ("mi200", "mi200", 0), ("v3_complete", "mi200+v3complete", 0),
 ("v3_m1", "mi200+v3m1", 1), ("v3_m1m2", "mi200+v3m1m2", 1), ("v3_m1m3", "mi200+v3m1m3", 1),
 ("m3", "mi200+m3", 2),
 ("v3_complete_nomerge", "mi200+v3c-nomerge", 3), ("v3_complete_nopack", "mi200+v3c-nopack", 3),
 ("v3_complete_alloc_shared", "mi200+v3c-allocshared", 3), ("v3_complete_alloc_near", "mi200+v3c-allocnear", 3),
]
import collections
q = collections.defaultdict(list)
for phase in range(4):
    for item, wall, host, args in sorted(W, key=lambda w: -w[1]):
        for cf, nm, ph in CFG:
            if ph != phase: continue
            full = cf in ("mi200", "v3_complete")
            est = 8 if full else 6
            exp = f"1021_abl_v3c-{item}_{nm}_4235991"
            pair = f"{item}-p0" if phase == 0 else exp
            q[host].append("\t".join([pair, str(est), exp, cf, f"{args} {NET} {FULL if full else CHEAP}"]))
for h, rows in q.items():
    open(f"queue_{h}.tsv", "w").write("\n".join(rows) + "\n")
    print(h, len(rows))
