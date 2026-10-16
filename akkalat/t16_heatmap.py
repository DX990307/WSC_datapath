#!/usr/bin/env python3
"""T16: 7x7 heatmap of per-GPM finish times from a -report-gpm-trace CSV.

Usage:
  python3 t16_heatmap.py <run>_gpm_trace.csv [--kernel K] [--metric M] [--out fig.png]

--kernel  kernel / phase index (default: the last one with work). "list" prints
          a one-line summary of every kernel.
--metric  finish   : gpm_kernel_end_ns - kernel_launch_ns (default; needs the
                     GPM's LaunchKernelRsp, i.e. a kernel that completed)
          lastwg   : last_wg_end_ns - kernel_launch_ns (also for truncated runs)
          rate     : completed WGs per us between first WG start and last WG end
          remote   : l1v_read_remote_frac
          remotelat: l1v_read_remote_mean_lat_ns

Always prints a text grid (tile x = column, y = row; the centre tile is the
driver/IOMMU) and a summary: max, mean, min, max/mean, max/min, and the
Spearman rank correlation with mean hops to the other GPMs and with hops to
the centre tile. Writes a PNG only when matplotlib is available and --out is
given. GPMs without work are shown as "--" and excluded from the summary.
"""
import argparse
import csv
import math
import sys

W = 7
CENTER = (3, 3)


def ffloat(s):
    try:
        return float(s)
    except (TypeError, ValueError):
        return None


def load(path):
    with open(path, newline="") as f:
        return list(csv.DictReader(f))


def ranks(xs):
    order = sorted(range(len(xs)), key=lambda i: xs[i])
    r = [0.0] * len(xs)
    i = 0
    while i < len(order):
        j = i
        while j + 1 < len(order) and xs[order[j + 1]] == xs[order[i]]:
            j += 1
        for k in range(i, j + 1):
            r[order[k]] = (i + j) / 2.0 + 1
        i = j + 1
    return r


def spearman(xs, ys):
    if len(xs) < 3:
        return float("nan")
    rx, ry = ranks(xs), ranks(ys)
    mx, my = sum(rx) / len(rx), sum(ry) / len(ry)
    sxy = sum((a - mx) * (b - my) for a, b in zip(rx, ry))
    sxx = sum((a - mx) ** 2 for a in rx)
    syy = sum((b - my) ** 2 for b in ry)
    if sxx == 0 or syy == 0:
        return float("nan")
    return sxy / math.sqrt(sxx * syy)


def value(row, metric):
    launch = ffloat(row["kernel_launch_ns"])
    if metric == "finish":
        end = ffloat(row["gpm_kernel_end_ns"])
        return None if end is None or launch is None else end - launch
    if metric == "lastwg":
        end = ffloat(row["last_wg_end_ns"])
        return None if end is None or launch is None else end - launch
    if metric == "rate":
        a, b = ffloat(row["first_wg_start_ns"]), ffloat(row["last_wg_end_ns"])
        n = ffloat(row["wg_completed"])
        if a is None or b is None or not n or b <= a:
            return None
        return n / ((b - a) / 1000.0)
    if metric == "remote":
        return ffloat(row["l1v_read_remote_frac"])
    if metric == "remotelat":
        return ffloat(row["l1v_read_remote_mean_lat_ns"])
    raise SystemExit(f"unknown metric {metric}")


def summarize(rows, metric):
    pts = []
    for r in rows:
        if int(r["wg_assigned"]) == 0:
            continue
        v = value(r, metric)
        if v is not None:
            pts.append((r, v))
    return pts


def text_grid(rows, metric, out=sys.stdout):
    grid = {}
    for r in rows:
        v = value(r, metric) if int(r["wg_assigned"]) > 0 else None
        grid[(int(r["x"]), int(r["y"]))] = v
    vals = [v for v in grid.values() if v is not None]
    big = max(vals) if vals else 0
    fmt = "{:8.3f}" if big < 10 else "{:8.1f}" if big < 1e5 else "{:8.3g}"
    print("      " + "".join(f"   x={x}   " for x in range(W)), file=out)
    for y in range(W):
        cells = []
        for x in range(W):
            if (x, y) == CENTER:
                cells.append("  [IOMMU] ")
                continue
            v = grid.get((x, y))
            cells.append("     --   " if v is None else " " + fmt.format(v) + " ")
        print(f"y={y}  " + "".join(cells), file=out)


def main():
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("csv")
    ap.add_argument("--kernel", default=None)
    ap.add_argument("--metric", default="finish",
                    choices=["finish", "lastwg", "rate", "remote", "remotelat"])
    ap.add_argument("--out", default=None)
    args = ap.parse_args()

    rows = load(args.csv)
    kernels = sorted({int(r["kernel"]) for r in rows})
    if not kernels:
        raise SystemExit("no kernels in the CSV")

    if args.kernel == "list":
        for k in kernels:
            kr = [r for r in rows if int(r["kernel"]) == k]
            busy = sum(1 for r in kr if int(r["wg_assigned"]) > 0)
            done = sum(int(r["wg_completed"]) for r in kr)
            print(f"kernel {k} {kr[0]['kernel_kind']} total_wg={kr[0]['kernel_total_wg']} "
                  f"busy_gpms={busy} completed_wg={done} "
                  f"launch={kr[0]['kernel_launch_ns']} end={kr[0]['kernel_end_ns'] or '-'}")
        return

    if args.kernel is None:
        k = max(k for k in kernels
                if any(int(r["wg_assigned"]) > 0 for r in rows if int(r["kernel"]) == k))
    else:
        k = int(args.kernel)
    kr = [r for r in rows if int(r["kernel"]) == k]

    print(f"{args.csv}: kernel {k} ({kr[0]['kernel_kind']}), metric {args.metric}")
    text_grid(kr, args.metric)

    pts = summarize(kr, args.metric)
    idle = sum(1 for r in kr if int(r["wg_assigned"]) == 0)
    if pts:
        vs = [v for _, v in pts]
        mx, mn, mean = max(vs), min(vs), sum(vs) / len(vs)
        hops = [float(r["mean_hops_to_others"]) for r, _ in pts]
        center = [float(r["hops_to_center"]) for r, _ in pts]
        print(f"GPMs with a value: {len(pts)}  without work: {idle}")
        print(f"max {mx:.3f}  mean {mean:.3f}  min {mn:.3f}  "
              f"max/mean {mx / mean if mean else float('nan'):.3f}  "
              f"max/min {mx / mn if mn else float('nan'):.3f}")
        print(f"spearman vs mean hops to others {spearman(hops, vs):+.3f}  "
              f"vs hops to centre {spearman(center, vs):+.3f}")
    else:
        print(f"no GPM has a value for metric {args.metric} "
              f"(truncated run? try --metric lastwg or rate)")

    if args.out:
        try:
            import matplotlib
            matplotlib.use("Agg")
            import matplotlib.pyplot as plt
        except ImportError:
            print("matplotlib not available; text grid only")
            return
        grid = [[float("nan")] * W for _ in range(W)]
        for r in kr:
            v = value(r, args.metric) if int(r["wg_assigned"]) > 0 else None
            if v is not None:
                grid[int(r["y"])][int(r["x"])] = v
        fig, ax = plt.subplots(figsize=(5.2, 4.6))
        im = ax.imshow(grid, cmap="viridis", origin="upper")
        for y in range(W):
            for x in range(W):
                v = grid[y][x]
                label = "IOMMU" if (x, y) == CENTER else ("" if math.isnan(v) else f"{v:.3g}")
                ax.text(x, y, label, ha="center", va="center", fontsize=7,
                        color="white" if (x, y) != CENTER else "black")
        ax.set_xticks(range(W))
        ax.set_yticks(range(W))
        ax.set_xlabel("x")
        ax.set_ylabel("y")
        unit = {"finish": "ns", "lastwg": "ns", "rate": "WG/us",
                "remote": "fraction", "remotelat": "ns"}[args.metric]
        ax.set_title(f"kernel {k}: {args.metric} ({unit})", fontsize=9)
        fig.colorbar(im, ax=ax, shrink=0.8)
        fig.tight_layout()
        fig.savefig(args.out, dpi=150)
        print(f"wrote {args.out}")


if __name__ == "__main__":
    main()
