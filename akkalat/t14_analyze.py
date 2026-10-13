"""Summarize T14 platbench results (and T13 stage 3 calibration).

    python3 akkalat/t14_analyze.py --out <dir> results/1013_t14-*

Writes <dir>/<test>.csv (all rows of that item, both configurations),
<dir>/pattern_links.csv (per-link load estimated from YX routing) and
<dir>/summary.json, and prints a readable summary.
"""

import argparse
import csv
import glob
import json
import math
import os
from collections import defaultdict

LINK_B_PER_CYCLE = 48 * 16  # -bandwidth=48 (x16 GB/s) at 1 GHz, per direction


def load(dirs):
    rows = []
    for d in dirs:
        p = os.path.join(d, "platform.csv")
        if not os.path.exists(p):
            continue
        with open(p) as f:
            for r in csv.DictReader(f):
                r["_dir"] = d
                rows.append(r)
    return rows


def num(r, k):
    try:
        return float(r[k])
    except (KeyError, ValueError, TypeError):
        return None


def write_csv(path, rows, columns):
    with open(path, "w", newline="") as f:
        w = csv.DictWriter(f, fieldnames=columns, extrasaction="ignore")
        w.writeheader()
        w.writerows(rows)


def fit_line(xs, ys):
    n = len(xs)
    mx, my = sum(xs) / n, sum(ys) / n
    sxx = sum((x - mx) ** 2 for x in xs)
    b = sum((x - mx) * (y - my) for x, y in zip(xs, ys)) / sxx
    return my - b * mx, b


# ------------------------------------------------------------ mesh helpers

def tile_xy(tile):
    return tile % 7, tile // 7


def yx_route(src_tile, dst_tile):
    """Directed links (from_tile, to_tile) on the Y-then-X route."""
    x, y = tile_xy(src_tile)
    dx, dy = tile_xy(dst_tile)
    links = []
    while y != dy:
        ny = y + (1 if dy > y else -1)
        links.append((y * 7 + x, ny * 7 + x))
        y = ny
    while x != dx:
        nx = x + (1 if dx > x else -1)
        links.append((y * 7 + x, y * 7 + nx))
        x = nx
    return links


def tile_of(gpu):
    return gpu - 1 if gpu <= 24 else gpu


def gpu_at(x, y):
    t = y * 7 + x
    return 0 if t == 24 else (t + 1 if t < 24 else t)


def pattern_sources(pattern, gpu, n=48):
    x, y = tile_xy(tile_of(gpu))
    if pattern == "alltoall":
        return [g for g in range(1, n + 1) if g != gpu]
    if pattern == "neighbor":
        out = []
        for ddx, ddy in ((1, 0), (-1, 0), (0, 1), (0, -1)):
            nx, ny = x + ddx, y + ddy
            if 0 <= nx < 7 and 0 <= ny < 7 and gpu_at(nx, ny):
                out.append(gpu_at(nx, ny))
        return out
    if pattern == "transpose":
        t = gpu_at(y, x)
        return [t] if t and t != gpu else []
    if pattern == "ring":
        return [gpu % n + 1]
    if pattern == "snake":
        order = []
        for yy in range(7):
            xs = range(7) if yy % 2 == 0 else range(6, -1, -1)
            order += [gpu_at(xx, yy) for xx in xs if gpu_at(xx, yy)]
        return [order[(order.index(gpu) + 1) % len(order)]]
    raise ValueError(pattern)


# ------------------------------------------------------------ summaries

def summarize(rows, out):
    by_test = defaultdict(list)
    for r in rows:
        by_test[r["test"]].append(r)
    summary = {}

    for test, rs in by_test.items():
        cols = [c for c in rs[0].keys() if c != "_dir"]
        write_csv(os.path.join(out, f"{test}.csv"), rs, cols)

    # 1. latency ladder
    lat = defaultdict(dict)
    for r in by_test.get("latency", []):
        key = (r["config"], r["group"], "local" if r["src_driver"] == r["dst_driver"] else f"{r['hops']}hop")
        lat[key][int(r["footprint_bytes"])] = num(r, "latency_ns")
    summary["latency"] = {f"{c}|{g}|{w}": dict(sorted(v.items())) for (c, g, w), v in lat.items()}

    # 2. bandwidth vs CUs
    bw = defaultdict(list)
    for r in by_test.get("bandwidth", []):
        bw[(r["config"], r["group"], r["mode"])].append((int(r["active_cus"]), num(r, "bandwidth_B_per_cycle")))
    bw_sum = {}
    for k, pts in bw.items():
        pts.sort()
        peak = max(v for _, v in pts)
        sat = next(c for c, v in pts if v >= 0.95 * peak)
        bw_sum["|".join(k)] = {"curve": pts, "peak_B_per_cycle": peak, "cus_at_95pct": sat}
    summary["bandwidth"] = bw_sum

    # 3. hop matrix
    hop = defaultdict(list)
    for r in by_test.get("hopmatrix", []):
        hop[r["config"]].append((int(r["hops"]), num(r, "latency_ns"), int(r["src_tile"]), int(r["dst_tile"])))
    hop_sum = {}
    for c, pts in hop.items():
        a, b = fit_line([p[0] for p in pts], [p[1] for p in pts])
        res = [(p[1] - (a + b * p[0])) for p in pts]
        worst = sorted(zip(res, pts), key=lambda x: -abs(x[0]))[:10]
        per_hop = defaultdict(list)
        for h, l, *_ in pts:
            per_hop[h].append(l)
        hop_sum[c] = {
            "pairs": len(pts), "fixed_ns": a, "ns_per_hop": b,
            "max_abs_residual_ns": max(abs(x) for x in res),
            "rms_residual_ns": math.sqrt(sum(x * x for x in res) / len(res)),
            "spread_by_hops": {h: [min(v), max(v)] for h, v in sorted(per_hop.items())},
            "largest_residuals": [{"src_tile": p[2], "dst_tile": p[3], "hops": p[0],
                                   "latency_ns": p[1], "residual_ns": r} for r, p in worst],
        }
    summary["hopmatrix"] = hop_sum

    # 4. remote bandwidth vs work-groups (Little's law: in flight = BW x latency / 64 B)
    rb = defaultdict(list)
    for r in by_test.get("remotebw", []):
        rb[(r["config"], r["group"], r["hops"])].append((int(r["wgs"]), num(r, "bandwidth_B_per_cycle")))
    summary["remotebw"] = {"|".join(k): sorted(v) for k, v in rb.items()}

    # 5. incast
    inc = defaultdict(list)
    for r in by_test.get("incast", []):
        inc[(r["config"], r["mode"])].append((int(r["k"]), num(r, "bandwidth_B_per_cycle"), num(r, "cycles")))
    summary["incast"] = {"|".join(k): sorted(v) for k, v in inc.items()}

    # 6. patterns (+ link loads from YX routing; data flows owner -> reader)
    pat_sum, link_rows = {}, []
    for r in by_test.get("patterns", []):
        p, c = r["pattern"], r["config"]
        chunk, cycles = int(r["footprint_bytes"]), num(r, "cycles")
        load = defaultdict(int)
        for g in range(1, 49):
            for s in pattern_sources(p, g):
                for link in yx_route(tile_of(s), tile_of(g)):
                    load[link] += chunk
        utils = {l: v / cycles / LINK_B_PER_CYCLE for l, v in load.items()}
        for (a, b2), u in sorted(utils.items()):
            link_rows.append({"config": c, "pattern": p, "from_tile": a, "to_tile": b2,
                              "bytes": load[(a, b2)], "utilization_est": f"{u:.4f}"})
        busiest = max(utils.items(), key=lambda x: x[1]) if utils else ((None, None), 0)
        pat_sum[f"{c}|{p}"] = {
            "aggregate_B_per_cycle": num(r, "bandwidth_B_per_cycle"), "cycles": cycles,
            "bytes_moved": int(r["bytes_moved"]), "avg_hops": num(r, "hops"),
            "links_used": len(utils), "max_link_util_est": busiest[1],
            "busiest_link": busiest[0],
            "mean_link_util_est": sum(utils.values()) / len(utils) if utils else 0,
        }
    write_csv(os.path.join(out, "pattern_links.csv"), link_rows,
              ["config", "pattern", "from_tile", "to_tile", "bytes", "utilization_est"])
    summary["patterns"] = pat_sum

    # 7. kernel
    summary["kernel"] = {f"{r['config']}|{r['pattern']}": {"launch_to_done_ns": num(r, "cycles"),
                                                          "interval_ns": num(r, "latency_ns")}
                         for r in by_test.get("kernel", [])}

    with open(os.path.join(out, "summary.json"), "w") as f:
        json.dump(summary, f, indent=1, default=str)
    return summary


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    ap.add_argument("dirs", nargs="+")
    args = ap.parse_args()
    dirs = [d for pat in args.dirs for d in glob.glob(pat)]
    os.makedirs(args.out, exist_ok=True)
    rows = load(dirs)
    s = summarize(rows, args.out)
    print(json.dumps({k: (v if k != "latency" else {kk: len(vv) for kk, vv in v.items()})
                      for k, v in s.items()}, indent=1, default=str)[:6000])


if __name__ == "__main__":
    main()
