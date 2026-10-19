#!/usr/bin/env python3
"""P12 (T19): WG footprint affinity and history stability.

Reads one run's -report-wg-footprint output (stats/metrics_wg_footprint.csv.gz,
stats/metrics_wg_kernels.csv) and the run's stdout.log (PA work lists and MoE
tiles from -t17-print-groups) and writes aggregated CSVs:

    python3 akkalat/p12_footprint.py <run_dir> <workload> <out_dir>

<out_dir>/<workload>_footprint_size.csv   per kernel: WGs, pages per WG, truncation
<out_dir>/<workload>_affinity.csv         per kernel x pair class: Jaccard and
                                          containment (|A&B| / |B|) of page sets
<out_dir>/<workload>_stability.csv        per launch pair of the same kernel (lag 1
                                          and 2): same-WG-index (and, for PA, same
                                          work item) page-set overlap

Pair classes: adjacent (WG i, i+1), same-group pairs (workload-specific, see
GROUPS below; consecutive members of a group), and random pairs from the same
kernel (the baseline). At most MAX_PAIRS pairs per class (random sample, fixed
seed). Pages are 4 KB virtual pages of vector loads.
"""

import csv
import gzip
import os
import random
import re
import statistics
import sys
from collections import defaultdict

MAX_PAIRS = 20000
SEED = 12


def read_kernels(path):
    out = {}
    with open(path) as f:
        for r in csv.DictReader(f):
            out[int(r["kernel"])] = r
    return out


def decode_pages(s):
    pages, cur = [], 0
    if not s:
        return pages
    for i, tok in enumerate(s.split(";")):
        v = int(tok, 16)
        cur = v if i == 0 else cur + v
        pages.append(cur)
    return pages


def read_footprints(path):
    """kernel -> {wg: (frozenset pages, row)}"""
    fp = defaultdict(dict)
    with gzip.open(path, "rt") as f:
        for r in csv.DictReader(f):
            k = int(r["kernel"])
            fp[k][int(r["wg"])] = (frozenset(decode_pages(r["page_list"])), r)
    return fp


def overlap(a, b):
    inter = len(a & b)
    union = len(a) + len(b) - inter
    j = inter / union if union else float("nan")
    cont = inter / len(b) if b else float("nan")
    return j, cont


def summarize(vals):
    vals = [v for v in vals if v == v]
    if not vals:
        return {"n": 0}
    vals.sort()
    n = len(vals)
    return {
        "n": n,
        "mean": sum(vals) / n,
        "median": vals[n // 2],
        "p10": vals[int(0.1 * (n - 1))],
        "p90": vals[int(0.9 * (n - 1))],
        "frac_ge_0.5": sum(v >= 0.5 for v in vals) / n,
        "frac_ge_0.9": sum(v >= 0.9 for v in vals) / n,
        "frac_zero": sum(v == 0 for v in vals) / n,
    }


def sample(pairs, rng):
    if len(pairs) > MAX_PAIRS:
        return rng.sample(pairs, MAX_PAIRS)
    return pairs


# ---------------------------------------------------------------------------
# workload-specific groups: kernel name -> function(wg, launch_index) -> group key

def parse_stdout(run_dir):
    pa_work, moe = [], None
    path = os.path.join(run_dir, "stdout.log")
    if not os.path.exists(path):
        return pa_work, moe
    with open(path, errors="replace") as f:
        for line in f:
            if line.startswith("PAGEDATTN,work,"):
                m = re.match(r"PAGEDATTN,work,step=(\d+),kv_heads=(\d+),items=(.*)", line.strip())
                items = [tuple(map(int, x.split(":"))) for x in m.group(3).split(";") if x]
                pa_work.append((int(m.group(2)), items))
            elif line.startswith("MOE,tiles,"):
                m = re.match(r"MOE,tiles,up_ntiles=(\d+),down_ntiles=(\d+),tiles=(.*)", line.strip())
                tiles = [tuple(map(int, x.split(":"))) for x in m.group(3).split(";") if x]
                moe = (int(m.group(1)), int(m.group(2)), tiles)
    return pa_work, moe


def group_functions(workload, kname, launch_idx, pa_work, moe):
    """Returns {class_name: f(wg) -> key or None}."""
    g = {}
    if workload.startswith("pagedattn") and kname == "pa_partial" and launch_idx < len(pa_work):
        kvh, items = pa_work[launch_idx]

        def seq(w, kvh=kvh, items=items):
            i = w // kvh
            return items[i][0] if i < len(items) else None

        def seq_head(w, kvh=kvh, items=items):
            i = w // kvh
            return (items[i][0], w % kvh) if i < len(items) else None
        g["same_seq"] = seq
        g["same_seq_head"] = seq_head
    if workload.startswith("moe") and moe and kname in ("moe_up", "moe_down"):
        up, down, tiles = moe
        ntn = up if kname == "moe_up" else down

        def expert(w, ntn=ntn):
            t = w // ntn
            return tiles[t][0] if t < len(tiles) else None

        def expert_col(w, ntn=ntn):
            t = w // ntn
            return (tiles[t][0], w % ntn) if t < len(tiles) else None
        g["same_expert"] = expert
        g["same_expert_col"] = expert_col
    return g


def pa_item(w, launch_idx, pa_work):
    """(seq, partition, head) of pa_partial WG w in decode step launch_idx."""
    kvh, items = pa_work[launch_idx]
    i = w // kvh
    if i >= len(items):
        return None
    return items[i] + (w % kvh,)


def main():
    run_dir, workload, out_dir = sys.argv[1:4]
    os.makedirs(out_dir, exist_ok=True)
    stats = os.path.join(run_dir, "stats")
    kernels = read_kernels(os.path.join(stats, "metrics_wg_kernels.csv"))
    fp = read_footprints(os.path.join(stats, "metrics_wg_footprint.csv.gz"))
    pa_work, moe = parse_stdout(run_dir)
    rng = random.Random(SEED)

    # launch index of each kernel among the kernels with the same name
    by_name = defaultdict(list)
    for k in sorted(kernels):
        by_name[kernels[k]["name"]].append(k)
    launch_idx = {k: i for ks in by_name.values() for i, k in enumerate(ks)}

    size_rows, aff_rows, stab_rows = [], [], []
    for k in sorted(fp):
        wgs = fp[k]
        name = kernels.get(k, {}).get("name", "?")
        sizes = [len(p) for p, _ in wgs.values()]
        reads = [int(r["reads"]) for _, r in wgs.values()]
        trunc = sum(r["truncated"] == "1" for _, r in wgs.values())
        size_rows.append({
            "workload": workload, "kernel": k, "name": name, "launch": launch_idx.get(k, 0),
            "wgs": len(wgs), "total_wg": kernels.get(k, {}).get("total_wg", ""),
            "pages_mean": sum(sizes) / len(sizes), "pages_median": statistics.median(sizes),
            "pages_max": max(sizes), "reads_mean": sum(reads) / len(reads),
            "truncated_wgs": trunc,
            "distinct_pages_kernel": len(frozenset().union(*[p for p, _ in wgs.values()])),
        })
        ids = sorted(wgs)
        classes = {}
        classes["adjacent"] = [(a, a + 1) for a in ids if a + 1 in wgs]
        if len(ids) > 1:
            classes["random"] = [tuple(rng.sample(ids, 2)) for _ in range(min(MAX_PAIRS, len(ids) * 4))]
        for cname, f in group_functions(workload, name, launch_idx.get(k, 0), pa_work, moe).items():
            members = defaultdict(list)
            for w in ids:
                key = f(w)
                if key is not None:
                    members[key].append(w)
            pairs = []
            for ms in members.values():
                pairs += [(ms[i], ms[i + 1]) for i in range(len(ms) - 1)]
            classes[cname] = pairs
            classes[cname + "_groups"] = len(members)
        for cname, pairs in classes.items():
            if cname.endswith("_groups"):
                continue
            pairs = sample(pairs, rng)
            js, cs = [], []
            for a, b in pairs:
                j, c = overlap(wgs[a][0], wgs[b][0])
                js.append(j)
                cs.append(c)
            sj, sc = summarize(js), summarize(cs)
            row = {"workload": workload, "kernel": k, "name": name, "launch": launch_idx.get(k, 0),
                   "class": cname, "groups": classes.get(cname + "_groups", ""), "pairs": sj["n"]}
            for key in ("mean", "median", "p10", "p90", "frac_ge_0.5", "frac_ge_0.9", "frac_zero"):
                row["jaccard_" + key] = sj.get(key, "")
            for key in ("mean", "median", "frac_ge_0.5", "frac_ge_0.9"):
                row["contain_" + key] = sc.get(key, "")
            aff_rows.append(row)

    for name, ks in by_name.items():
        lagged = [(ks[i], ks[i + lag], lag) for lag in (1, 2) for i in range(len(ks) - lag)]
        for k1, k2, lag in lagged:
            if k1 not in fp or k2 not in fp:
                continue
            a, b = fp[k1], fp[k2]
            modes = {"same_wg_index": [(w, w) for w in a if w in b]}
            if workload.startswith("pagedattn") and name == "pa_partial" and pa_work:
                i1, i2 = launch_idx[k1], launch_idx[k2]
                if i2 < len(pa_work):
                    pos2 = {}
                    for w in b:
                        it = pa_item(w, i2, pa_work)
                        if it:
                            pos2[it] = w
                    modes["same_work_item"] = [(w, pos2[pa_item(w, i1, pa_work)]) for w in a
                                               if pa_item(w, i1, pa_work) in pos2]
                    modes["same_wg_index_item_moved"] = [
                        (w, w) for w in a if w in b and pa_item(w, i1, pa_work) != pa_item(w, i2, pa_work)]
            for mode, pairs in modes.items():
                js, cs, same_gpm = [], [], 0
                for w1, w2 in pairs:
                    j, c = overlap(a[w1][0], b[w2][0])
                    js.append(j)
                    cs.append(c)
                    same_gpm += a[w1][1]["gpm"] == b[w2][1]["gpm"]
                sj, sc = summarize(js), summarize(cs)
                row = {"workload": workload, "name": name, "kernel_a": k1, "kernel_b": k2,
                       "lag": lag, "mode": mode, "wgs_a": len(a), "wgs_b": len(b), "pairs": sj["n"],
                       "same_gpm_frac": same_gpm / len(pairs) if pairs else ""}
                for key in ("mean", "median", "p10", "p90", "frac_ge_0.5", "frac_ge_0.9", "frac_zero"):
                    row["jaccard_" + key] = sj.get(key, "")
                for key in ("mean", "median", "frac_ge_0.9"):
                    row["contain_" + key] = sc.get(key, "")
                stab_rows.append(row)

    for fname, rows in (("footprint_size", size_rows), ("affinity", aff_rows), ("stability", stab_rows)):
        path = os.path.join(out_dir, f"{workload}_{fname}.csv")
        with open(path, "w", newline="") as f:
            if not rows:
                f.write("none\n")
                continue
            keys = []
            for r in rows:
                for key in r:
                    if key not in keys:
                        keys.append(key)
            w = csv.DictWriter(f, keys)
            w.writeheader()
            for r in rows:
                w.writerow({k: (f"{v:.4f}" if isinstance(v, float) else v) for k, v in r.items()})
        print("wrote", path)


if __name__ == "__main__":
    main()
