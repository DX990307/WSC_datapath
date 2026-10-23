import os, sys, re, collections
R = os.path.expanduser("~/akita-exp-test/wgsched/src/results")
def tot(e):
    t = collections.Counter(); kt = []
    for l in open(f"{R}/{e}/stats/metrics.csv"):
        p = l.rstrip("\n").split(", ")
        if len(p) < 4: continue
        try: v = float(p[3])
        except: continue
        if p[2] in ("rdma_observed_remote_reads", "rdma_observed_remote_writes"): t[p[2]] += v
        if p[2] == "kernel_time" and p[1] != "Driver": kt.append(v)
    t["ktm"] = max(kt) / (sum(kt) / len(kt)) if kt and sum(kt) else 0
    return t
for l in open(os.path.expanduser("~/akita-exp-test/wgsched/pass1.tsv")):
    wl, p1 = l.split()
    e = f"1022_place_wgs-{wl}_mi200+wgmap_a739ac3"
    if not os.path.exists(f"{R}/{e}/meta.json") or not os.path.exists(f"{R}/{p1}/meta.json"): continue
    a, b = tot(p1), tot(e)
    rr = b["rdma_observed_remote_reads"] / a["rdma_observed_remote_reads"] if a["rdma_observed_remote_reads"] else 0
    rw = b["rdma_observed_remote_writes"] / a["rdma_observed_remote_writes"] if a["rdma_observed_remote_writes"] else 0
    print(f"{wl}\t{rr:.2f}\t{rw:.2f}\t{a['ktm']:.2f}\t{b['ktm']:.2f}")
