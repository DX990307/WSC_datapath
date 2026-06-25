#!/usr/bin/env python3
"""Draw chord-style diagrams for requester-owner traffic pairs."""

import argparse
import csv
import math
import os
import re
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")

import matplotlib.pyplot as plt
from matplotlib.path import Path as MplPath
from matplotlib.patches import FancyArrowPatch, PathPatch


BENCHMARK_ORDER = [
    "bitonicsort",
    "relu",
    "matrixmultiplication",
    "matrixtranspose",
    "kmeans",
    "spmv",
    "im2col",
    "aes",
    "fft",
    "floydwarshall",
    "pagerank",
    "simpleconvolution",
    "fastwalshtransform",
    "fir",
]

DISPLAY_NAMES = {
    "aes": "AES",
    "bitonicsort": "Bitonic Sort",
    "fastwalshtransform": "Fast Walsh",
    "fft": "FFT",
    "fir": "FIR",
    "floydwarshall": "Floyd Warshall",
    "im2col": "Im2Col",
    "kmeans": "KMeans",
    "matrixmultiplication": "Matrix Mul.",
    "matrixtranspose": "Matrix Trans.",
    "pagerank": "PageRank",
    "relu": "ReLU",
    "simpleconvolution": "Simple Conv.",
    "spmv": "SpMV",
}

LLM_OP_PATTERN = re.compile(
    r"^baseline_llmop_(?P<model>[^_]+)_(?P<profile>[^_]+)_"
    r"(?P<index>\d{3})_(?P<label>.+)_(?P<config>llm_mixed|baseline)_"
    r"sharing_pair_bytes\.csv$"
)

LLM_DISPLAY_NAMES = {
    "embedding": "Embedding",
    "layer00_norm1": "Norm1",
    "layer00_attn_q": "Attn Q",
    "layer00_attn_k": "Attn K",
    "layer00_attn_v": "Attn V",
    "layer00_attn_score": "Attn Score",
    "layer00_causal_mask": "Causal Mask",
    "layer00_attn_softmax": "Attn Softmax",
    "layer00_attn_value": "Attn Value",
    "layer00_attn_out": "Attn Out",
    "layer00_attn_residual": "Attn Residual",
    "layer00_norm2": "Norm2",
    "layer00_mlp_fc1": "MLP FC1",
    "layer00_mlp_gelu": "MLP GELU",
    "layer00_mlp_fc2": "MLP FC2",
    "layer00_mlp_residual": "MLP Residual",
}


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "results_dir",
        type=Path,
        help="Directory containing *_sharing_pair_bytes.csv files.",
    )
    parser.add_argument(
        "--out-dir",
        type=Path,
        default=None,
        help="Directory for chord diagrams. Defaults to results_dir/figures/chord_diagrams.",
    )
    parser.add_argument(
        "--top-pairs",
        type=int,
        default=40,
        help="Draw only the top N remote requester-owner pairs. Ignored when --coverage is set.",
    )
    parser.add_argument(
        "--coverage",
        type=float,
        default=None,
        help="Draw enough remote pairs to cover this fraction of --coverage-metric.",
    )
    parser.add_argument(
        "--coverage-metric",
        choices=("bytes", "accesses"),
        default="bytes",
        help="Metric used by --coverage and pair ranking.",
    )
    parser.add_argument(
        "--figsize",
        type=float,
        default=8.0,
        help="Square figure size in inches.",
    )
    parser.add_argument(
        "--layout",
        choices=("circle", "row"),
        default="circle",
        help="Use circular chords or a row-based arc diagram.",
    )
    parser.add_argument(
        "--no-arrows",
        action="store_true",
        help="Hide requester -> owner arrowheads on chords.",
    )
    return parser.parse_args()


def benchmark_from_path(path):
    name = path.name
    llm_match = LLM_OP_PATTERN.match(name)
    if llm_match:
        return f"llm_{llm_match.group('index')}_{llm_match.group('label')}"

    prefix = "baseline_"
    suffix = "_baseline_sharing_pair_bytes.csv"
    if name.startswith(prefix) and name.endswith(suffix):
        return name[len(prefix) : -len(suffix)]
    return name.replace("_sharing_pair_bytes.csv", "")


def display_name_for_benchmark(benchmark):
    if benchmark.startswith("llm_"):
        parts = benchmark.split("_", 2)
        if len(parts) == 3:
            index = int(parts[1])
            label = parts[2]
            return f"{index:02d} {LLM_DISPLAY_NAMES.get(label, label)}"
    return DISPLAY_NAMES.get(benchmark, benchmark)


def sort_key_for_path(path):
    benchmark = benchmark_from_path(path)
    if benchmark.startswith("llm_"):
        parts = benchmark.split("_", 2)
        if len(parts) >= 2 and parts[1].isdigit():
            return (0, int(parts[1]), benchmark)
    if benchmark in BENCHMARK_ORDER:
        return (1, BENCHMARK_ORDER.index(benchmark), benchmark)
    return (2, 10_000, benchmark)


def clean_stem(name):
    return name.replace("_baseline_sharing_pair_bytes.csv", "")


def summary_path_for_pair_path(path):
    return Path(str(path).replace("_sharing_pair_bytes.csv", "_sharing_summary_metrics.csv"))


def read_summary_metric(path, metric):
    summary_path = summary_path_for_pair_path(path)
    if not summary_path.exists():
        return None

    with open(summary_path, newline="") as f:
        for row in csv.DictReader(f):
            if row.get("metric") != metric:
                continue
            value = row.get("value", "")
            return float(value) if value else None
    return None


def read_remote_pairs(path):
    rows = []
    with open(path, newline="") as f:
        reader = csv.DictReader(f)
        for row in reader:
            requester = int(row["requester"])
            owner = int(row["owner"])
            if requester == owner:
                continue
            bytes_count = float(row["bytes"])
            accesses = int(float(row["accesses"]))
            rows.append(
                {
                    "requester": requester,
                    "owner": owner,
                    "bytes": bytes_count,
                    "accesses": accesses,
                }
            )
    return rows


def parse_gpu_set(value):
    value = value.strip()
    if not value:
        return set()
    if value == "all":
        return set(range(1, 49))

    gpus = set()
    for part in value.split(","):
        part = part.strip()
        if not part:
            continue
        if "-" in part:
            start, end = part.split("-", 1)
            gpus.update(range(int(start), int(end) + 1))
        else:
            gpus.add(int(part))
    return gpus


def read_used_gpu_map(results_dir):
    placement_path = results_dir / "llm_decomposed_placement.csv"
    if not placement_path.exists():
        return {}

    used_by_benchmark = {}
    with open(placement_path, newline="") as f:
        for row in csv.DictReader(f):
            benchmark = f"llm_{int(row['op_index']):03d}_{row['label']}"
            used_by_benchmark[benchmark] = parse_gpu_set(row.get("compute_gpus", ""))
    return used_by_benchmark


def pair_sort_key(row, metric):
    return (-row[metric], row["requester"], row["owner"])


def select_pairs(rows, top_pairs, coverage, metric):
    sorted_rows = sorted(rows, key=lambda row: pair_sort_key(row, metric))
    if coverage is None:
        return sorted_rows[:top_pairs]

    target = max(0.0, coverage) * sum(row[metric] for row in sorted_rows)
    selected = []
    running_total = 0.0
    for row in sorted_rows:
        selected.append(row)
        running_total += row[metric]
        if running_total >= target:
            break
    return selected


def node_positions(nodes):
    positions = {}
    count = len(nodes)
    for index, node in enumerate(nodes):
        angle = math.pi / 2 - 2 * math.pi * index / count
        positions[node] = (math.cos(angle), math.sin(angle), angle)
    return positions


def row_positions(nodes):
    return {node: (index, 0.0, 0.0) for index, node in enumerate(nodes)}


def chord_points(start, end):
    x1, y1, _ = start
    x2, y2, _ = end
    control_scale = 0.20
    return [
        (x1, y1),
        (x1 * control_scale, y1 * control_scale),
        (x2 * control_scale, y2 * control_scale),
        (x2, y2),
    ]


def chord_path(start, end):
    verts = chord_points(start, end)
    codes = [
        MplPath.MOVETO,
        MplPath.CURVE4,
        MplPath.CURVE4,
        MplPath.CURVE4,
    ]
    return MplPath(verts, codes)


def row_arc_points(start, end):
    x1, y1, _ = start
    x2, y2, _ = end
    span = abs(x2 - x1)
    direction = 1 if x2 >= x1 else -1
    height = direction * min(6.4, 0.34 + 0.13 * span)
    return [
        (x1, y1),
        (x1, height),
        (x2, height),
        (x2, y2),
    ]


def row_arc_path(start, end):
    verts = row_arc_points(start, end)
    codes = [
        MplPath.MOVETO,
        MplPath.CURVE4,
        MplPath.CURVE4,
        MplPath.CURVE4,
    ]
    return MplPath(verts, codes)


def bezier_point(points, t):
    p0, p1, p2, p3 = points
    one_minus = 1.0 - t
    x = (
        one_minus**3 * p0[0]
        + 3 * one_minus**2 * t * p1[0]
        + 3 * one_minus * t**2 * p2[0]
        + t**3 * p3[0]
    )
    y = (
        one_minus**3 * p0[1]
        + 3 * one_minus**2 * t * p1[1]
        + 3 * one_minus * t**2 * p2[1]
        + t**3 * p3[1]
    )
    return (x, y)


def add_direction_arrow(ax, start, end, color, weight):
    points = chord_points(start, end)
    tail = bezier_point(points, 0.72)
    head = bezier_point(points, 0.83)
    arrow = FancyArrowPatch(
        tail,
        head,
        arrowstyle="-|>",
        mutation_scale=8 + 13 * weight,
        linewidth=0,
        color=color,
        alpha=0.40 + 0.42 * weight,
        zorder=4,
    )
    ax.add_patch(arrow)


def add_row_direction_arrow(ax, start, end, color, weight):
    points = row_arc_points(start, end)
    tail = bezier_point(points, 0.66)
    head = bezier_point(points, 0.75)
    arrow = FancyArrowPatch(
        tail,
        head,
        arrowstyle="-|>",
        mutation_scale=7 + 10 * weight,
        linewidth=0,
        color=color,
        alpha=0.38 + 0.40 * weight,
        zorder=4,
    )
    ax.add_patch(arrow)


def subtitle_text(
    coverage,
    coverage_metric,
    selected_metric_coverage,
    access_coverage,
    byte_coverage,
    remote_access_ratio,
):
    shown_total_access_ratio = (
        remote_access_ratio * access_coverage
        if remote_access_ratio is not None
        else None
    )
    remote = (
        f"remote/all={remote_access_ratio * 100:.1f}%; "
        if remote_access_ratio is not None
        else ""
    )
    shown_total = (
        f"shown/all={shown_total_access_ratio * 100:.1f}%; "
        if shown_total_access_ratio is not None
        else ""
    )
    if coverage is None:
        return (
            f"{remote}{shown_total}shown/remote={access_coverage * 100:.1f}%, "
            f"byte coverage={byte_coverage * 100:.1f}%"
        )
    return (
        f"{remote}{shown_total}{coverage_metric}/remote={selected_metric_coverage * 100:.1f}% "
        f"(access={access_coverage * 100:.1f}%, byte={byte_coverage * 100:.1f}%)"
    )


def plot_chord(
    path,
    out_dir,
    top_pairs,
    coverage,
    coverage_metric,
    figsize,
    layout,
    show_arrows,
    used_gpu_map,
):
    benchmark = benchmark_from_path(path)
    display_name = display_name_for_benchmark(benchmark)
    used_gpus = used_gpu_map.get(benchmark, set())
    rows = read_remote_pairs(path)
    remote_access_ratio = read_summary_metric(path, "RemoteAccessRatio")
    total_remote = sum(row["bytes"] for row in rows)
    total_accesses = sum(row["accesses"] for row in rows)
    selected = select_pairs(rows, top_pairs, coverage, coverage_metric)
    selected_total = sum(row["bytes"] for row in selected)
    selected_accesses = sum(row["accesses"] for row in selected)
    byte_coverage = selected_total / total_remote if total_remote else 0
    access_coverage = selected_accesses / total_accesses if total_accesses else 0
    selected_metric_coverage = (
        access_coverage if coverage_metric == "accesses" else byte_coverage
    )

    selected_nodes = {value for row in selected for value in (row["requester"], row["owner"])}
    nodes = sorted(selected_nodes | used_gpus)
    if not nodes:
        return None

    node_list = list(range(1, 49)) if layout == "row" else nodes
    positions = row_positions(node_list) if layout == "row" else node_positions(node_list)
    node_weight = {node: 0.0 for node in node_list}
    for row in selected:
        node_weight[row["requester"]] += row[coverage_metric]
        node_weight[row["owner"]] += row[coverage_metric]

    max_pair = max(row[coverage_metric] for row in selected)
    max_node = max(node_weight.values())
    palette = plt.get_cmap("tab20", max(20, len(nodes)))
    color_for_node = {
        node: palette(index % palette.N)
        for index, node in enumerate(node_list)
    }

    if layout == "row":
        fig_width = max(16.0, figsize * 2.2)
        fig_height = max(5.4, figsize * 0.78)
        fig, ax = plt.subplots(figsize=(fig_width, fig_height))
        ax.set_aspect("auto")
        ax.axis("off")
        ax.plot(
            [-0.7, len(node_list) - 0.3],
            [0, 0],
            color="#AAB2C0",
            linewidth=1.0,
            zorder=1,
        )
    else:
        fig, ax = plt.subplots(figsize=(figsize, figsize))
        ax.set_aspect("equal")
        ax.axis("off")

        circle = plt.Circle(
            (0, 0),
            1.0,
            edgecolor="#AAB2C0",
            facecolor="none",
            linewidth=1.0,
        )
        ax.add_patch(circle)

    if layout != "row":
        for row in sorted(selected, key=lambda item: item[coverage_metric]):
            start = positions[row["requester"]]
            end = positions[row["owner"]]
            weight = math.sqrt(row[coverage_metric] / max_pair)
            patch = PathPatch(
                chord_path(start, end),
                facecolor="none",
                edgecolor=color_for_node[row["requester"]],
                linewidth=0.35 + 5.0 * weight,
                alpha=0.18 + 0.42 * weight,
                capstyle="round",
                joinstyle="round",
            )
            ax.add_patch(patch)
            if show_arrows:
                add_direction_arrow(
                    ax,
                    start,
                    end,
                    color_for_node[row["requester"]],
                    weight,
                )
    else:
        for row in sorted(selected, key=lambda item: item[coverage_metric]):
            start = positions[row["requester"]]
            end = positions[row["owner"]]
            weight = math.sqrt(row[coverage_metric] / max_pair)
            patch = PathPatch(
                row_arc_path(start, end),
                facecolor="none",
                edgecolor=color_for_node[row["requester"]],
                linewidth=0.25 + 3.5 * weight,
                alpha=0.14 + 0.42 * weight,
                capstyle="round",
                joinstyle="round",
            )
            ax.add_patch(patch)
            if show_arrows:
                add_row_direction_arrow(
                    ax,
                    start,
                    end,
                    color_for_node[row["requester"]],
                    weight,
                )

    if layout != "row":
        for node in node_list:
            x, y, angle = positions[node]
            size = 80 + 520 * math.sqrt(node_weight[node] / max_node)
            ax.scatter(
                [x],
                [y],
                s=size,
                color=color_for_node[node],
                edgecolors="white",
                linewidths=1.0,
                zorder=5,
            )
            if node in used_gpus:
                ax.scatter(
                    [x],
                    [y],
                    s=size + 165,
                    facecolors="none",
                    edgecolors="#D62728",
                    linewidths=2.0,
                    zorder=6,
                )
            label_radius = 1.13
            ha = "left" if x >= 0 else "right"
            ax.text(
                x * label_radius,
                y * label_radius,
                str(node),
                ha=ha,
                va="center",
                fontsize=8,
            )

        ax.set_xlim(-1.28, 1.28)
        ax.set_ylim(-1.42, 1.30)
        title_y = 1.28
        subtitle_y = 1.18
    else:
        for node in node_list:
            x, y, _ = positions[node]
            weight = math.sqrt(node_weight[node] / max_node) if max_node else 0.0
            size = 30 + 240 * weight
            ax.scatter(
                [x],
                [y],
                s=size,
                color=color_for_node[node],
                edgecolors="white",
                linewidths=0.8,
                zorder=5,
            )
            if node in used_gpus:
                ax.scatter(
                    [x],
                    [y],
                    s=size + 105,
                    facecolors="none",
                    edgecolors="#D62728",
                    linewidths=1.6,
                    zorder=6,
                )
            ax.text(x, -0.52, str(node), ha="center", va="top", fontsize=7)

        ax.set_xlim(-1.2, len(node_list) - 0.1)
        ax.set_ylim(-7.2, 7.9)
        title_y = 7.55
        subtitle_y = 6.85

    title = f"{display_name}: Top {len(selected)} Remote Pairs"
    subtitle = subtitle_text(
        coverage,
        coverage_metric,
        selected_metric_coverage,
        access_coverage,
        byte_coverage,
        remote_access_ratio,
    )
    title_x = 0 if layout != "row" else (len(node_list) - 1) / 2
    ax.text(title_x, title_y, title, ha="center", va="bottom", fontsize=14)
    ax.text(title_x, subtitle_y, subtitle, ha="center", va="bottom", fontsize=10)
    if used_gpus:
        legend_y = -1.36 if layout != "row" else subtitle_y - 0.55
        ax.text(
            title_x,
            legend_y,
            "red ring = used compute GPU",
            ha="center",
            va="bottom",
            fontsize=9,
            color="#D62728",
        )

    out_dir.mkdir(parents=True, exist_ok=True)
    suffix = "row" if layout == "row" else "chord"
    stem = f"{benchmark}_remote_pair_{suffix}"
    png = out_dir / f"{stem}.png"
    pdf = out_dir / f"{stem}.pdf"
    fig.savefig(png, dpi=220)
    fig.savefig(pdf)
    plt.close(fig)

    return {
        "benchmark": benchmark,
        "name": display_name,
        "remote_pairs": len(rows),
        "plotted_pairs": len(selected),
        "nodes": len(selected_nodes),
        "used_gpu_count": len(used_gpus),
        "total_remote_pair_bytes": total_remote,
        "plotted_remote_pair_bytes": selected_total,
        "total_remote_pair_accesses": total_accesses,
        "plotted_remote_pair_accesses": selected_accesses,
        "coverage_metric": coverage_metric,
        "coverage": selected_metric_coverage,
        "byte_coverage": byte_coverage,
        "access_coverage": access_coverage,
        "remote_access_ratio": remote_access_ratio,
        "shown_total_access_ratio": (
            remote_access_ratio * access_coverage
            if remote_access_ratio is not None
            else None
        ),
        "png": png,
        "pdf": pdf,
    }


def write_summary(rows, out_dir):
    path = out_dir / "chord_diagram_summary.csv"
    fieldnames = [
        "benchmark",
        "name",
        "remote_pairs",
        "plotted_pairs",
        "nodes",
        "used_gpu_count",
        "total_remote_pair_bytes",
        "plotted_remote_pair_bytes",
        "total_remote_pair_accesses",
        "plotted_remote_pair_accesses",
        "coverage_metric",
        "coverage",
        "byte_coverage",
        "access_coverage",
        "remote_access_ratio",
        "shown_total_access_ratio",
        "png",
        "pdf",
    ]
    with open(path, "w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)
    return path


def write_readme(
    out_dir,
    top_pairs,
    coverage,
    coverage_metric,
    layout,
    show_arrows,
    marks_used_gpus,
):
    path = out_dir / "README.md"
    with open(path, "w") as f:
        f.write("# Remote Pair Chord Diagrams\n\n")
        f.write("Each diagram visualizes requester-owner traffic pairs from `*_sharing_pair_bytes.csv`.\n\n")
        f.write("Each title reports total remote-access ratio plus plotted-pair coverage.\n\n")
        if coverage is None:
            f.write(f"Only the top {top_pairs} remote pairs are drawn to keep the figure readable. ")
        else:
            f.write(
                f"Each diagram draws enough remote pairs to cover at least "
                f"{coverage * 100:.1f}% of remote pair {coverage_metric}. "
            )
        f.write("The title reports both access and byte coverage.\n\n")
        f.write("- Nodes are requester/owner tile IDs.\n")
        if layout == "row":
            f.write("- Nodes are laid out in tile-ID order from left to right.\n")
            f.write("- Arcs above the row go to the right; arcs below the row go to the left.\n")
        f.write("- Each chord/arc is one remote requester -> owner pair.\n")
        if show_arrows:
            f.write("- Arrowheads show access direction: requester -> owner.\n")
        if marks_used_gpus:
            f.write("- Red rings mark compute GPUs used by the op from `llm_decomposed_placement.csv`.\n")
        f.write("- Chord width is proportional to pair traffic.\n")
        f.write("- Chord color follows the requester tile.\n")
    return path


def main():
    args = parse_args()
    results_dir = args.results_dir
    out_dir = args.out_dir or results_dir / "figures" / "chord_diagrams"
    used_gpu_map = read_used_gpu_map(results_dir)
    paths = sorted(
        results_dir.glob("*_sharing_pair_bytes.csv"),
        key=sort_key_for_path,
    )
    summaries = []
    show_arrows = not args.no_arrows
    for path in paths:
        summary = plot_chord(
            path,
            out_dir,
            args.top_pairs,
            args.coverage,
            args.coverage_metric,
            args.figsize,
            args.layout,
            show_arrows,
            used_gpu_map,
        )
        if summary:
            summaries.append(summary)

    summary_path = write_summary(summaries, out_dir)
    readme_path = write_readme(
        out_dir,
        args.top_pairs,
        args.coverage,
        args.coverage_metric,
        args.layout,
        show_arrows,
        bool(used_gpu_map),
    )

    print(f"Generated {len(summaries)} chord diagrams.")
    print(f"Output directory: {out_dir}")
    print(summary_path)
    print(readme_path)


if __name__ == "__main__":
    main()
