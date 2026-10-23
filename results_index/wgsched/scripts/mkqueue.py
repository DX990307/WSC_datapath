import sys
C = "a739ac3"
D = "/home/daoxuanxu/akita-exp-test/wgsched"
NET = "-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=128"
T18 = "-t18-window=128 -t18-compose-table-entries=128"
WL = [  # name, est_gb, wall_h, args
 ("moe-t64", 5, 2.2, "-benchmark moe -moe-tokens=64 -moe-hidden=1024 -moe-ffn=704 -moe-topk=6 -moe-skew=1.75"),
 ("npbcg-W", 6, 1.3, "-benchmark npbcg -cg-class W"),
 ("lud-n1024", 5, 0.67, "-benchmark rodinia_lud -lud-n 1024"),
 ("mt-g4", 5, 0.4, "-benchmark matrixtranspose-t16small"),
 ("spmv-g32", 5, 0.28, "-benchmark spmv-t16small -t16-wg-per-cu 32"),
 ("mg-W", 14, 1.23, "-benchmark npb_mg -mg-class W"),
 ("bfs-s18e2", 12, 0.84, "-benchmark bfs -bfs-scale 18 -bfs-edge-factor 2"),
 ("sssp-s18e4-r1", 12, 0.77, "-benchmark sssp -sssp-scale 18 -sssp-edge-factor 4 -sssp-max-rounds 1"),
 ("mm-g12", 5, 0.7, "-benchmark matrixmultiplication-t16small"),
 ("fw-n2048-p4", 5, 0.55, "-benchmark floydwarshall-p12small -p12-fw-nodes=2048 -p12-fw-passes=4"),
 ("pr-g24", 5, 0.54, "-benchmark pagerank-t16small -t16-wg-per-cu 24"),
 ("pa-b14s7p32", 5, 0.52, "-benchmark pagedattn -pa-batch=14 -pa-seed=7 -pa-part-tokens=32"),
 ("tc-s12e16", 5, 0.51, "-benchmark graphtc -tc-scale 12 -tc-edge-factor 16"),
 ("bh-n86k", 5, 0.49, "-benchmark lonestar_bh -bh-bodies 86016"),
 ("gups-L18m8", 5, 0.45, "-benchmark gups -gups-log-table 18 -gups-update-mult 8"),
 ("fft2d-n1024", 5, 0.44, "-benchmark w2-fft2d -fft2d-n 1024"),
 ("cfd-c172k-b128", 5, 0.44, "-benchmark altis_cfd -cfd-cells 172032 -cfd-block 128"),
 ("ivf-n8k-q328", 5, 0.4, "-benchmark ivfflat -ivf-n 8192 -ivf-queries 328"),
 ("km-g1", 5, 0.39, "-benchmark kmeans-t16small -t16-wg-per-cu 1"),
 ("atax-2688x8192-r64", 5, 0.34, "-benchmark w2-atax -atax-nx 2688 -atax-ny 8192 -atax-rows-per-chunk 64"),
 ("i2c-g64", 5, 0.33, "-benchmark im2col-t16small -t16-wg-per-cu 64"),
 ("ar-pc1", 5, 0.31, "-benchmark allreduce -ar-per-cu 1"),
 ("npbis-n1m-i1", 5, 0.3, "-benchmark w2-npbis -npbis-n 1048576 -npbis-iterations 1"),
 ("shocsort-n688k", 5, 0.3, "-benchmark w2-shocsort -shocsort-n 688128"),
 ("embbag-b512", 5, 0.29, "-benchmark embbag -eb-batch 512"),
]
# assign hosts greedily by total wall (pass1 + 4 pass2 runs)
load = {"yss2": 0.0, "yss3": 0.0}
host = {}
for n, e, w, a in WL:
    h = min(load, key=load.get)
    host[n] = h
    load[h] += 5 * w
print(load, file=sys.stderr)
q = {"yss2": [], "yss3": []}
p1 = {"yss2": [], "yss3": []}
def row(h, exp, est, cfg, args, need=""):
    q[h].append("\t".join([exp, str(est), exp, cfg, args, C, need]))
for n, e, w, a in WL:
    exp = f"1022_place_wgs-p1-{n}_mi200+wgaff_{C}"
    row(host[n], exp, e, "mi200", f"{a} {NET} -report-wgaff")
    p1[host[n]].append(f"{n}\t{exp}")
for tier in (1, 2):
    for n, e, w, a in WL:
        h = host[n]; m = f"{D}/maps/{n}.map"
        if tier == 1:
            row(h, f"1022_place_wgs-{n}_mi200+wgmap_{C}", e, "mi200", f"{a} {NET} -wg-map {m}", m)
            row(h, f"1022_place_wgs-{n}_mi200+v4m3+wgmap_{C}", e + 2, "v4m3", f"{a} {NET} {T18} -wg-map {m}", m)
        else:
            row(h, f"1022_place_wgs-{n}_mi200+m3p+wgmap_{C}", e + 2, "m3p", f"{a} {NET} {T18} -wg-map {m}", m)
            row(h, f"1022_place_wgs-{n}_mi200+m3p_{C}", e + 2, "m3p", f"{a} {NET} {T18}")
for h in q:
    open(f"{sys.argv[1]}/queue_{h}.tsv", "w").write("\n".join(q[h]) + "\n")
    open(f"{sys.argv[1]}/pass1_{h}.tsv", "w").write("\n".join(p1[h]) + "\n")
