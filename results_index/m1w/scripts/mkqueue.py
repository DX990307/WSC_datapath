#!/usr/bin/env python3
"""mkqueue.py <commit> <out_prefix>: m1w queues (bound + prototypes), split over yss2 / yss3.
Scenarios: w256b384 (main), w256b48m. Bound runs: baseline + v4complete (w256b384), v4complete (w256b48m),
all with -report-m1w-bound (statistics only, timing-neutral). Prototype runs: hubturn, hubq2, hold100,
holdrtt25, hubturn+holdrtt25 in both scenarios. Repeats: bfs / sssp control and hubturn."""
import sys
C, out = sys.argv[1], sys.argv[2]
wl = {
 "pr-g24": "-benchmark pagerank-t16small -t16-wg-per-cu 24",
 "npbcg-W": "-benchmark npbcg -cg-class W",
 "mm-g12": "-benchmark matrixmultiplication-t16small",
 "tc-s12e16": "-benchmark graphtc -tc-scale 12 -tc-edge-factor 16",
 "sssp-s18e4-r1": "-benchmark sssp -sssp-scale 18 -sssp-edge-factor 4 -sssp-max-rounds 1",
 "bfs-s18e2": "-benchmark bfs -bfs-scale 18 -bfs-edge-factor 2",
 "cfd-c172k-b128": "-benchmark altis_cfd -cfd-cells 172032 -cfd-block 128",
 "ar-pc1": "-benchmark allreduce -ar-per-cu 1",
}
w256 = "-rdma-max-outstanding=256 -t18-window=256 -t18-compose-table-entries=512"
sc = {"w256b384": f"-bandwidth=24 -switch-buffer-flits=16 {w256}",
      "w256b48m": f"-bandwidth=3 -switch-buffer-flits=16 {w256}"}
stat = "-report-cupath-v3-stats"
runs = []  # (pair, exp, cfg, args)
for w, a in wl.items():
    for s in sc:
        cfgs = [("v4_complete", "mi200+v4complete")]
        if s == "w256b384":
            cfgs.insert(0, ("mi200", "mi200"))
        for cfg, tag in cfgs:
            runs.append((f"b-{s}-{w}", f"1022_abl_m1wb-{s}-{w}_{tag}_{C}", cfg, f"{a} {sc[s]} {stat} -report-m1w-bound"))
        for cfg, tag in (("v4_complete_hubturn", "mi200+v4hubturn"), ("v4_complete_hubq2", "mi200+v4hubq2"),
                         ("v4_complete_hold100", "mi200+v4hold100"), ("v4_complete_holdrtt25", "mi200+v4holdrtt25"),
                         ("v4_complete_hubturn,v4_complete_holdrtt25", "mi200+v4hubturnhold")):
            runs.append((f"p-{s}-{w}", f"1022_abl_m1w-{s}-{w}_{tag}_{C}", cfg, f"{a} {sc[s]} {stat}"))
        if w in ("bfs-s18e2", "sssp-s18e4-r1"):
            for cfg, tag in (("v4_complete", "mi200+v4complete-rep2"), ("v4_complete_hubturn", "mi200+v4hubturn-rep2")):
                runs.append((f"r-{s}-{w}", f"1022_abl_m1w-{s}-{w}_{tag}_{C}", cfg, f"{a} {sc[s]} {stat}"))
q = {"yss2": [], "yss3": []}
pairs = []
for r in runs:
    if not pairs or pairs[-1][0][0] != r[0]:
        pairs.append([])
    pairs[-1].append(r)
for p in pairs:
    h = min(q, key=lambda k: len(q[k]))
    q[h] += [f"{r[0]}\t7.0\t{r[1]}\t{r[2]}\t{r[3]}" for r in p]
for h in q:
    open(f"{out}_{h}.tsv", "w").write("\n".join(q[h]) + "\n")
    print(h, len(q[h]))
