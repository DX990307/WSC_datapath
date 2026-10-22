#!/usr/bin/env python3
"""Phase-2 checks on the merged tree c4082ae (new baseline c08698b):
A1-off identity vs c08698b, and -verify with A1 on. Only 384 GB/s, W=256."""
import sys
NEW, BASE = "c4082ae", "c08698b"
MAIN = "-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512"
CFG = {"mi200": "mi200", "v4": "v4_complete", "a1": "v4_complete_a1"}
NAME = {"mi200": "mi200", "v4": "mi200+v4complete", "a1": "mi200+v4complete+a1"}
rows = {"yss2": [], "yss3": []}
def add(host, item, cfg, args, commit, est):
    exp = f"1022_diag_{item}_{NAME[cfg]}_{commit}"
    rows[host].append(f"{item}-{cfg}\t{est}\t{exp}\t{CFG[cfg]}\t{args}\t{commit}")
for item, args, est, host in [("a1bx2-fir", "-max-wg=4096 -benchmark fir", 6, "yss2"),
                              ("a1bx2-npbcgS", "-benchmark npbcg -cg-class S", 3, "yss2"),
                              ("a1bx2-mg", "-benchmark npb_mg -verify", 3, "yss3")]:
    for cfg in ("mi200", "v4"):
        for commit in (BASE, NEW):
            add(host, item, cfg, args + " " + MAIN, commit, est)
V = [("a1v2-a1check", "-benchmark a1check -verify", 2, "yss2"),
     ("a1v2-bfs12", "-benchmark bfs -bfs-scale 12 -verify", 3, "yss2"),
     ("a1v2-sssp12", "-benchmark sssp -sssp-scale 12 -sssp-max-rounds 4 -verify", 3, "yss2"),
     ("a1v2-npbcgS", "-benchmark npbcg -cg-class S -verify", 3, "yss2"),
     ("a1v2-mg", "-benchmark npb_mg -verify", 3, "yss3"),
     ("a1v2-cfd", "-benchmark altis_cfd -verify", 3, "yss3"),
     ("a1v2-lud", "-benchmark rodinia_lud -verify", 3, "yss3"),
     ("a1v2-fft2d", "-benchmark w2-fft2d -verify", 3, "yss3"),
     ("a1v2-mm12", "-benchmark matrixmultiplication-t16small -verify -magic-copy-coherent", 4, "yss3"),
     ("a1v2-shocsort64k", "-benchmark w2-shocsort -shocsort-n 65536 -verify", 3, "yss2"),
     ("a1v2-npbis2", "-benchmark w2-npbis -npbis-iterations 2 -verify", 3, "yss2")]
for item, args, est, host in V:
    add(host, item, "a1", args + " " + MAIN, NEW, est)
for h, rs in rows.items():
    open(f"{sys.argv[1]}/queue2_{h}.tsv", "w").write("\n".join(rs) + "\n")
    print(h, len(rs))
