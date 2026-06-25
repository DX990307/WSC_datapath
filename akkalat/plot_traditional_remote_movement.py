#!/usr/bin/env python3
"""Plot remote-data movement patterns for traditional benchmark traces."""

import argparse
import csv
import os
from collections import Counter
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")

import matplotlib.pyplot as plt
from matplotlib import colors
from matplotlib.patches import FancyArrowPatch, Rectangle


GRID_WIDTH = 7
GRID_HEIGHT = 7
CENTER = (GRID_WIDTH // 2, GRID_HEIGHT // 2)

BENCHMARK_ORDER = [
    "bitonicsort",
    "matrixmultiplication",
    "matrixtranspose",
    "kmeans",
    "spmv",
    "aes",
    "floydwarshall",
    "pagerank",
    "simpleconvolution",
    "fastwalshtransform",
    "fft",
    "fir",
    "im2col",
    "relu",
]

PLOT_NAMES = {
    "aes": "AES",
    "bitonicsort": "Bitonic",
    "fastwalshtransform": "FWT",
    "fft": "FFT",
    "fir": "FIR",
    "floydwarshall": "Floyd",
    "im2col": "Im2Col",
    "kmeans": "KMeans",
    "matrixmultiplication": "MatMul",
    "matrixtranspose": "Transpose",
    "pagerank": "PageRank",
    "relu": "ReLU",
    "simpleconvolution": "Conv",
    "spmv": "SpMV",
}

COLORS = {
    "local": "#2F80ED",
    "remote": "#EB5757",
    "shared": "#FFB000",
    "private": "#7B61FF",
    "distance": "#F28E2B",
    "grid": "#D8DEE9",
}


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "results_dir",
        type=Path,
        help="Directory containing traditional benchmark sharing CSV outputs.",
    )
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=None,
        help="Directory for figures. Defaults to results_dir/figures/traditional_remote_movement.",
    )
    parser.add_argument(
        "--top-flows",
        type=int,
        default=0,
        help="Fixed number of requester-owner flows to draw. 0 uses --flow-coverage.",
    )
    parser.add_argument(
        "--flow-coverage",
        type=float,
        default=0.90,
        help="Remote-byte coverage target when --top-flows=0. Use 1.0 for all flows.",
    )
    return parser.parse_args()


def device_coords():
    coords = {}
    dev = 1
    for y in range(GRID_HEIGHT):
        for x in range(GRID_WIDTH):
            if (x, y) == CENTER:
                continue
            coords[dev] = (x, y)
            dev += 1
    return coords


COORDS = device_coords()


def delta(requester, owner):
    rx, ry = COORDS[requester]
    ox, oy = COORDS[owner]
    return ox - rx, oy - ry


def benchmark_from_summary(path):
    name = path.name
    prefix = "baseline_"
    suffix = "_baseline_sharing_summary_metrics.csv"
    if name.startswith(prefix) and name.endswith(suffix):
        return name[len(prefix) : -len(suffix)]
    return name.replace("_sharing_summary_metrics.csv", "")


def pair_path_for(results_dir, benchmark):
    path = results_dir / f"baseline_{benchmark}_baseline_sharing_pair_bytes.csv"
    return path if path.exists() else None


def read_summary(path):
    metrics = {}
    with open(path, newline="") as f:
        for row in csv.DictReader(f):
            value = row["value"]
            metrics[row["metric"]] = float(value) if value else 0.0
    return metrics


def read_pairs(path):
    pair_counts = Counter()
    delta_counts = Counter()
    total_remote = 0.0
    with open(path, newline="") as f:
        for row in csv.DictReader(f):
            requester = int(row["requester"])
            owner = int(row["owner"])
            if requester == owner or requester not in COORDS or owner not in COORDS:
                continue
            weight = float(row["bytes"])
            pair_counts[(requester, owner)] += weight
            delta_counts[delta(requester, owner)] += weight
            total_remote += weight
    return pair_counts, delta_counts, total_remote


def load_rows(results_dir):
    rows = []
    for path in sorted(results_dir.glob("*_sharing_summary_metrics.csv")):
        benchmark = benchmark_from_summary(path)
        if benchmark not in BENCHMARK_ORDER:
            continue
        metrics = read_summary(path)
        pair_path = pair_path_for(results_dir, benchmark)
        if not pair_path:
            continue
        pair_counts, delta_counts, total_remote = read_pairs(pair_path)
        rows.append(
            {
                "benchmark": benchmark,
                "name": PLOT_NAMES.get(benchmark, benchmark),
                "metrics": metrics,
                "pair_counts": pair_counts,
                "delta_counts": delta_counts,
                "pair_remote_bytes": total_remote,
            }
        )

    order = {name: index for index, name in enumerate(BENCHMARK_ORDER)}
    rows.sort(key=lambda row: order[row["benchmark"]])
    return rows


def save(fig, out_dir, stem, tight=True):
    out_dir.mkdir(parents=True, exist_ok=True)
    png = out_dir / f"{stem}.png"
    pdf = out_dir / f"{stem}.pdf"
    if tight:
        fig.tight_layout(pad=0.55)
    fig.savefig(png, dpi=240)
    fig.savefig(pdf)
    plt.close(fig)
    return png, pdf


def style_axis(ax):
    ax.grid(axis="y", color=COLORS["grid"], linewidth=0.8, alpha=0.85)
    ax.set_axisbelow(True)
    ax.spines["top"].set_visible(False)
    ax.spines["right"].set_visible(False)


def plot_dashboard(rows, out_dir):
    names = [row["name"] for row in rows]
    x = list(range(len(rows)))

    fig, axes = plt.subplots(2, 2, figsize=(14.8, 8.6))

    ax = axes[0][0]
    local = [row["metrics"].get("LocalAccessRatio", 0.0) for row in rows]
    remote = [row["metrics"].get("RemoteAccessRatio", 0.0) for row in rows]
    ax.bar(x, local, color=COLORS["local"], label="Local")
    ax.bar(x, remote, bottom=local, color=COLORS["remote"], label="Remote")
    ax.set_xticks(x, names, rotation=30, ha="right")
    ax.set_ylim(0, 1)
    ax.set_ylabel("Access ratio")
    ax.set_title("Remote accesses vary widely across traditional workloads")
    ax.yaxis.set_major_formatter(lambda value, _pos: f"{100 * value:.0f}%")
    ax.legend(frameon=False, ncols=2, loc="lower right")
    style_axis(ax)

    ax = axes[0][1]
    private = [1.0 - row["metrics"].get("SharedByteRatio", 0.0) for row in rows]
    shared = [row["metrics"].get("SharedByteRatio", 0.0) for row in rows]
    ax.bar(x, private, color=COLORS["private"], label="Private bytes")
    ax.bar(x, shared, bottom=private, color=COLORS["shared"], label="Shared bytes")
    ax.set_xticks(x, names, rotation=30, ha="right")
    ax.set_ylim(0, 1)
    ax.set_ylabel("Byte ratio")
    ax.set_title("Remote movement can be private, shared, or both")
    ax.yaxis.set_major_formatter(lambda value, _pos: f"{100 * value:.0f}%")
    ax.legend(frameon=False, loc="lower right")
    style_axis(ax)

    ax = axes[1][0]
    distance = [row["metrics"].get("WeightedAccessDistance", 0.0) for row in rows]
    ax.bar(x, distance, color=COLORS["distance"])
    ax.set_xticks(x, names, rotation=30, ha="right")
    ax.set_ylabel("Average hop per access")
    ax.set_title("Hop cost separates near-neighbor and long-distance traffic")
    style_axis(ax)

    ax = axes[1][1]
    remote_ratio = [row["metrics"].get("RemoteAccessRatio", 0.0) for row in rows]
    shared_ratio = [row["metrics"].get("SharedByteRatio", 0.0) for row in rows]
    pair_entropy = [row["metrics"].get("PairEntropyNorm", 0.0) for row in rows]
    sizes = [60 + 380 * value for value in pair_entropy]
    ax.scatter(shared_ratio, remote_ratio, s=sizes, color="#37474F", alpha=0.80)
    for row, sx, ry in zip(rows, shared_ratio, remote_ratio):
        ax.annotate(row["name"], (sx, ry), xytext=(5, 4), textcoords="offset points", fontsize=8)
    ax.set_xlim(-0.04, 1.04)
    ax.set_ylim(-0.04, 1.04)
    ax.set_xlabel("Shared byte ratio")
    ax.set_ylabel("Remote access ratio")
    ax.set_title("Bubble size = requester-owner dispersion")
    ax.xaxis.set_major_formatter(lambda value, _pos: f"{100 * value:.0f}%")
    ax.yaxis.set_major_formatter(lambda value, _pos: f"{100 * value:.0f}%")
    style_axis(ax)

    return save(fig, out_dir, "traditional_remote_dashboard")


def delta_matrix(delta_counts):
    values = range(-(GRID_WIDTH - 1), GRID_WIDTH)
    matrix = [[0 for _ in values] for _ in values]
    for (dx, dy), count in delta_counts.items():
        matrix[dy + GRID_HEIGHT - 1][dx + GRID_WIDTH - 1] = count
    return matrix


def plot_offset_gallery(rows, out_dir):
    cols = min(4, len(rows))
    num_rows = (len(rows) + cols - 1) // cols if rows else 1
    fig, axes = plt.subplots(
        num_rows,
        cols,
        figsize=(3.7 * cols, 3.25 * num_rows),
        sharex=True,
        sharey=True,
        squeeze=False,
    )
    max_count = max((max(row["delta_counts"].values()) for row in rows if row["delta_counts"]), default=1)
    norm = colors.LogNorm(vmin=1, vmax=max_count)
    cmap = plt.get_cmap("viridis")
    image = None

    for ax, row in zip(axes.flat, rows):
        matrix = delta_matrix(row["delta_counts"])
        image = ax.imshow(
            matrix,
            origin="lower",
            cmap=cmap,
            norm=norm,
            extent=[-6.5, 6.5, -6.5, 6.5],
        )
        ax.axhline(0, color="white", linewidth=0.8, alpha=0.65)
        ax.axvline(0, color="white", linewidth=0.8, alpha=0.65)
        top = row["delta_counts"].most_common(3)
        coverage = (
            sum(count for _, count in top) / row["pair_remote_bytes"]
            if row["pair_remote_bytes"]
            else 0.0
        )
        label = ", ".join(str(delta_value) for delta_value, _ in top[:2])
        ax.set_title(f"{row['name']}\nTop3 {100 * coverage:.0f}%: {label}", fontsize=9, pad=4)
        ax.set_xticks([-6, -3, 0, 3, 6])
        ax.set_yticks([-6, -3, 0, 3, 6])
        ax.tick_params(length=0)

    for ax in axes.flat[len(rows):]:
        ax.axis("off")

    for ax in axes[-1, :]:
        ax.set_xlabel("owner_x - requester_x")
    for ax in axes[:, 0]:
        ax.set_ylabel("owner_y - requester_y")

    fig.subplots_adjust(left=0.06, right=0.90, bottom=0.08, top=0.89, wspace=0.18, hspace=0.44)
    if image:
        cax = fig.add_axes([0.925, 0.18, 0.018, 0.58])
        cbar = fig.colorbar(image, cax=cax)
        cbar.set_label("Remote bytes")
    fig.suptitle("Traditional Benchmark Remote Offset Gallery", fontsize=15, y=0.965)
    return save(fig, out_dir, "traditional_offset_gallery", tight=False)


def draw_wafer_grid(ax):
    for y in range(GRID_HEIGHT):
        for x in range(GRID_WIDTH):
            if (x, y) == CENTER:
                rect = Rectangle((x - 0.44, y - 0.44), 0.88, 0.88, facecolor="#ECEFF1", edgecolor="#B0BEC5")
                ax.add_patch(rect)
                ax.text(x, y, "C", ha="center", va="center", fontsize=8, color="#78909C")
                continue
            rect = Rectangle((x - 0.44, y - 0.44), 0.88, 0.88, facecolor="white", edgecolor="#CFD8DC")
            ax.add_patch(rect)
    for dev, (x, y) in COORDS.items():
        ax.text(x, y, str(dev), ha="center", va="center", fontsize=6.5, color="#455A64")
    ax.set_xlim(-0.8, GRID_WIDTH - 0.2)
    ax.set_ylim(GRID_HEIGHT - 0.2, -0.8)
    ax.set_aspect("equal")
    ax.axis("off")


def add_flow(ax, requester, owner, weight, max_weight, color):
    rx, ry = COORDS[requester]
    ox, oy = COORDS[owner]
    strength = weight / max_weight if max_weight else 0.0
    arrow = FancyArrowPatch(
        (rx, ry),
        (ox, oy),
        connectionstyle="arc3,rad=0.10",
        arrowstyle="-|>",
        mutation_scale=7 + 14 * strength,
        linewidth=0.7 + 5.0 * strength,
        color=color,
        alpha=0.16 + 0.58 * strength,
        zorder=5,
    )
    ax.add_patch(arrow)


def select_flows(pair_counts, top_flows, flow_coverage):
    pairs = pair_counts.most_common()
    if top_flows > 0:
        return pairs[:top_flows]

    if flow_coverage >= 1.0:
        return pairs

    target = max(0.0, flow_coverage) * sum(weight for _, weight in pairs)
    selected = []
    running_total = 0.0
    for pair, weight in pairs:
        selected.append((pair, weight))
        running_total += weight
        if running_total >= target:
            break
    return selected


def plot_wafer_flows(rows, out_dir, top_flows, flow_coverage):
    selected = rows
    if not selected:
        return []

    cols = min(4, len(selected))
    num_rows = (len(selected) + cols - 1) // cols
    fig, axes = plt.subplots(
        num_rows,
        cols,
        figsize=(4.6 * cols, 4.2 * num_rows),
        squeeze=False,
    )
    palette = plt.get_cmap("tab20").colors

    for index, (ax, row) in enumerate(zip(axes.flat, selected)):
        color = palette[index % len(palette)]
        draw_wafer_grid(ax)
        pairs = select_flows(row["pair_counts"], top_flows, flow_coverage)
        max_weight = pairs[0][1] if pairs else 1.0
        for (requester, owner), weight in reversed(pairs):
            add_flow(ax, requester, owner, weight, max_weight, color)
        coverage = (
            sum(weight for _, weight in pairs) / row["pair_remote_bytes"]
            if row["pair_remote_bytes"]
            else 0.0
        )
        flow_label = (
            f"All {len(pairs)} flows"
            if len(pairs) == len(row["pair_counts"])
            else f"Top {len(pairs)} flows"
        )
        ax.set_title(
            f"{row['name']}\n{flow_label} cover {100 * coverage:.0f}%",
            fontsize=10,
            pad=6,
        )

    for ax in axes.flat[len(selected):]:
        ax.axis("off")

    fig.subplots_adjust(left=0.025, right=0.99, bottom=0.04, top=0.91, wspace=0.12, hspace=0.34)
    fig.suptitle("Traditional Wafer Flow Maps: requester -> owner", fontsize=15, y=0.965)
    return save(fig, out_dir, "traditional_wafer_flow_maps", tight=False)


def write_values(rows, out_dir):
    path = out_dir / "traditional_remote_values.csv"
    fields = [
        "benchmark",
        "name",
        "remote_access_ratio",
        "shared_byte_ratio",
        "weighted_access_distance",
        "pair_entropy_norm",
        "top_delta",
        "top_delta_coverage",
        "top3_delta_coverage",
        "top_pair_coverage",
    ]
    with open(path, "w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        for row in rows:
            top_delta = row["delta_counts"].most_common(3)
            top_pairs = row["pair_counts"].most_common(3)
            remote_bytes = row["pair_remote_bytes"]
            metrics = row["metrics"]
            writer.writerow(
                {
                    "benchmark": row["benchmark"],
                    "name": row["name"],
                    "remote_access_ratio": metrics.get("RemoteAccessRatio", 0.0),
                    "shared_byte_ratio": metrics.get("SharedByteRatio", 0.0),
                    "weighted_access_distance": metrics.get("WeightedAccessDistance", 0.0),
                    "pair_entropy_norm": metrics.get("PairEntropyNorm", 0.0),
                    "top_delta": top_delta[0][0] if top_delta else "",
                    "top_delta_coverage": top_delta[0][1] / remote_bytes if remote_bytes and top_delta else 0.0,
                    "top3_delta_coverage": sum(count for _, count in top_delta) / remote_bytes if remote_bytes else 0.0,
                    "top_pair_coverage": sum(count for _, count in top_pairs) / remote_bytes if remote_bytes else 0.0,
                }
            )

    readme = out_dir / "README.md"
    with open(readme, "w") as f:
        f.write("# Traditional Remote Movement Figures\n\n")
        f.write("Generated from `*_sharing_summary_metrics.csv` and `*_sharing_pair_bytes.csv`.\n\n")
        f.write("- `traditional_remote_dashboard`: remote/local, shared/private, hop distance, and remote-vs-shared scatter.\n")
        f.write("- `traditional_offset_gallery`: remote requester-owner offset heatmaps.\n")
        f.write("- `traditional_wafer_flow_maps`: remote flows over the 7x7 wafer layout, selected by byte coverage or fixed count.\n")
    return path, readme


def main():
    args = parse_args()
    out_dir = args.out_dir or args.results_dir / "figures" / "traditional_remote_movement"
    rows = load_rows(args.results_dir)
    if not rows:
        raise SystemExit("No traditional sharing summaries/pairs found.")

    generated = []
    generated.extend(plot_dashboard(rows, out_dir))
    generated.extend(plot_offset_gallery(rows, out_dir))
    generated.extend(plot_wafer_flows(rows, out_dir, args.top_flows, args.flow_coverage))
    values, readme = write_values(rows, out_dir)

    print(f"Generated {len(generated)} figure files.")
    print(f"Output directory: {out_dir}")
    print(values)
    print(readme)


if __name__ == "__main__":
    main()
