"""Summarize T11 probe results.

Usage: python3 akkalat/t11_analyze_probe.py results/<exp_id> [...]

For each experiment prints the per-pass simulated time, where L2 data came
from (local/remote x l2_cache/l2_mshr/dram), remote-access latency by hop
count, RDMA transaction counts, DRAM transactions per GPM, and which GPMs
executed instructions. GPM numbers are mesh tiles (GPU[t]); driver GPU ID g
maps to t = g-1 for g <= 24 and t = g for g >= 25.
"""

import collections
import csv
import json
import os
import re
import sys

GPU_RE = re.compile(r"GPU\[(\d+)\]")


def read_metrics(path):
    rows = []
    with open(path) as f:
        next(f)
        for line in f:
            parts = [p.strip() for p in line.split(",")]
            if len(parts) >= 4:
                rows.append((parts[1], parts[2], float(parts[3])))
    return rows


def gpm_of(name):
    m = GPU_RE.search(name)
    return int(m.group(1)) if m else None


def summarize(exp_dir):
    stats = os.path.join(exp_dir, "stats")
    out = collections.OrderedDict()
    meta = json.load(open(os.path.join(exp_dir, "meta.json")))
    out["exp_id"] = meta["exp_id"]
    out["exit_code"] = meta["exit_code"]

    with open(os.path.join(exp_dir, "summary.csv")) as f:
        out["passes_ns"] = [float(r["elapsed_ns"]) for r in csv.DictReader(f)]

    sources = collections.Counter()
    with open(os.path.join(stats, "l2_data_source.csv")) as f:
        for r in csv.DictReader(f):
            key = (r["source"], r["access_type"])
            sources[key] += int(r["accesses"])
    out["l2_sources"] = {f"{s}/{op}": n for (s, op), n in sorted(sources.items())}

    by_hops = collections.defaultdict(lambda: [0, 0.0])
    with open(os.path.join(stats, "l2_remote_matrix.csv")) as f:
        for r in csv.DictReader(f):
            n = int(r["accesses"])
            acc = by_hops[(int(r["hops"]), r["access_type"])]
            acc[0] += n
            acc[1] += n * float(r["avg_latency_ns"])
    out["remote_by_hops"] = {
        f"{h}hop/{op}": {"accesses": n, "avg_latency_ns": round(s / n, 1)}
        for (h, op), (n, s) in sorted(by_hops.items()) if n
    }

    rdma = collections.Counter()
    dram = collections.defaultdict(collections.Counter)
    insts = collections.Counter()
    for where, what, value in read_metrics(os.path.join(stats, "metrics.csv")):
        if ".RDMA" in where and what.endswith("_trans_count"):
            rdma[what] += value
        elif ".DRAM" in where and what in ("read_trans_count", "write_trans_count"):
            dram[gpm_of(where)][what] += value
        elif what == "cu_inst_count" and value > 0:
            insts[gpm_of(where)] += value
    out["rdma"] = dict(rdma)
    out["dram_by_gpm"] = {g: dict(c) for g, c in sorted(dram.items())
                          if sum(c.values()) > 0}
    out["active_gpms"] = sorted(insts)
    out["inst_count_by_gpm"] = dict(sorted(insts.items()))
    return out


def main():
    for exp_dir in sys.argv[1:]:
        print(json.dumps(summarize(exp_dir.rstrip("/")), indent=1))


if __name__ == "__main__":
    main()
