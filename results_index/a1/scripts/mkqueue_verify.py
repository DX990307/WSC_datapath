#!/usr/bin/env python3
"""Phase-1 (pre-rebase) A1 checks: bit-exactness with A1 off, -verify runs."""
import sys
NEW, BASE = "0c39bab", "ad0dfe3"
MAIN = "-bandwidth=24 -switch-buffer-flits=16 -rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512"
CFG = {"mi200": "mi200", "v4": "v4_complete", "a1": "v4_complete_a1"}
NAME = {"mi200": "mi200", "v4": "mi200+v4complete", "a1": "mi200+v4complete+a1"}
rows = {"yss2": [], "yss3": []}

def add(host, item, cfg, args, commit, est, variant="", extra=""):
    name = NAME[cfg] + (("+" + variant) if variant else "")
    exp = f"1021_diag_{item}_{name}_{commit}"
    rows[host].append(f"{item}-{cfg}{variant}\t{est}\t{exp}\t{CFG[cfg]}\t{args} {extra}".rstrip() + f"\t{commit}")

# 1. bit-exact with A1 off (fir, MM): old vs new binary, mi200 and v4_complete
for item, args, est, host in [("a1bx-fir", "-max-wg=4096 -benchmark fir", 6, "yss2"),
                              ("a1bx-mm", "-max-wg=4096 -benchmark matrixmultiplication", 8, "yss3")]:
    for cfg in ("mi200", "v4"):
        for commit in (BASE, NEW):
            add(host, item, cfg, args, commit, est)
# 2. -verify at the main configuration, v4_complete vs v4_complete_a1
V = [
    ("a1v-a1check", "-benchmark a1check -verify", 2, "yss2"),
    ("a1v-bfs12", "-benchmark bfs -bfs-scale 12 -verify", 3, "yss2"),
    ("a1v-sssp12", "-benchmark sssp -sssp-scale 12 -sssp-max-rounds 4 -verify", 3, "yss2"),
    ("a1v-npbcgS", "-benchmark npbcg -cg-class S -verify", 3, "yss2"),
    ("a1v-mg", "-benchmark npb_mg -verify", 3, "yss2"),
    ("a1v-cfd", "-benchmark altis_cfd -verify", 3, "yss3"),
    ("a1v-lud", "-benchmark rodinia_lud -verify", 3, "yss3"),
    ("a1v-fft2d", "-benchmark w2-fft2d -verify", 3, "yss3"),
    ("a1v-mm12", "-benchmark matrixmultiplication-t16small -verify -magic-copy-coherent", 4, "yss3"),
    ("a1v-shocsort64k", "-benchmark w2-shocsort -shocsort-n 65536 -verify", 3, "yss3"),
    ("a1v-npbis2", "-benchmark w2-npbis -npbis-iterations 2 -verify", 3, "yss3"),
]
for item, args, est, host in V:
    for cfg in ("v4", "a1"):
        add(host, item, cfg, args + " " + MAIN, NEW, est)
# 3. A1 variants (first-touch admission, Bloom signature, 64 B regions)
for item, args, est, host in V[:5]:
    add(host, item, "a1", args + " " + MAIN, NEW, est, "ft2", "-a1-first-touch-admission=2")
    add(host, item, "a1", args + " " + MAIN, NEW, est, "sig1k", "-a1-signature-bits=1024")
for item, args, est, host in V[5:]:
    add(host, item, "a1", args + " " + MAIN, NEW, est, "ft1", "-a1-first-touch-admission=1")
for h, rs in rows.items():
    open(f"{sys.argv[1]}/queue_{h}.tsv", "w").write("\n".join(rs) + "\n")
    print(h, len(rs))
