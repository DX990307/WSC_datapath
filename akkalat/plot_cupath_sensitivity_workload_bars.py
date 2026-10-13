#!/usr/bin/env python3
"""Plot CuPath sensitivity as grouped bars for 14 workloads plus geomean."""

from __future__ import annotations

import argparse
import math
import os
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Patch

from plot_cupath_sensitivity_campaign import (
    DIMENSIONS,
    geomean,
    load_campaign,
    write_tables,
)


BENCHMARKS = (
    "aes",
    "bitonicsort",
    "fastwalshtransform",
    "fft",
    "fir",
    "floydwarshall",
    "im2col",
    "kmeans",
    "matrixmultiplication",
    "matrixtranspose",
    "pagerank",
    "relu",
    "simpleconvolution",
    "spmv",
)

LABELS = {
    "aes": "AES",
    "bitonicsort": "BT",
    "fastwalshtransform": "FWT",
    "fft": "FFT",
    "fir": "FIR",
    "floydwarshall": "FWS",
    "im2col": "I2C",
    "kmeans": "KM",
    "matrixmultiplication": "MM",
    "matrixtranspose": "MT",
    "pagerank": "PR",
    "relu": "RELU",
    "simpleconvolution": "SC",
    "spmv": "SPMV",
}

SERIES_COLORS = ("#83CEE2", "#31ADCE", "#1D687C", "#0A2329")
Y_LIMIT = 2.0
TOP_LABEL_Y = 2.025

# Explicit data-coordinate positions keep all clipped values on one row.
# FWS, KM, and MM are the only groups whose values exceed the 2x cap in the
# current sensitivity campaign.  Keeping the coordinates here makes the
# layout deliberate and auditable rather than dependent on bar geometry.
TOP_LABEL_X_COORDINATES = {
    "floydwarshall": (3.36, 3.74, 4.12, 4.50),
    "kmeans": (4.94, 5.32, 5.70, 6.08),
    "matrixmultiplication": (6.46, 6.84, 7.22, 7.60),
}


def build_grid(rows):
    grid = {}
    for row in rows:
        value = row["speedup"]
        grid[(row["dimension"], row["point"], row["benchmark"])] = (
            None if value == "" else float(value)
        )
    return grid


def point_colors(points):
    return SERIES_COLORS[:len(points)]


def verify_top_labels(fig, labels) -> float:
    """Verify that clipped-value labels occupy one non-overlapping row."""
    if len(labels) < 2:
        return float("inf")
    if max(label.get_position()[1] for label in labels) - min(
        label.get_position()[1] for label in labels
    ) > 1e-12:
        raise RuntimeError("clipped labels are not on one data-coordinate row")
    fig.canvas.draw()
    renderer = fig.canvas.get_renderer()
    boxes = sorted(
        (
            (label.get_window_extent(renderer=renderer), label.get_text())
            for label in labels
        ),
        key=lambda item: item[0].x0,
    )
    minimum_gap = float("inf")
    for (left, left_text), (right, right_text) in zip(boxes, boxes[1:]):
        gap = right.x0 - left.x1
        minimum_gap = min(minimum_gap, gap)
        if gap < 0:
            raise RuntimeError(
                f"top labels {left_text} and {right_text} overlap "
                f"by {-gap:.2f} pixels"
            )
    return minimum_gap


def legend_label(dimension: str, point) -> str:
    suffix = {
        "filter-size": "K buckets",
        "l2-size": " MiB",
        "row-locality": " lines",
    }[dimension]
    if dimension == "filter-size":
        return f"{point.label}{suffix[1:]}"
    return f"{point.label}{suffix}"


def series(grid, dimension: str, point):
    values = [grid.get((dimension, point.name, benchmark)) for benchmark in BENCHMARKS]
    complete = all(value is not None for value in values)
    return values + [geomean(values) if complete else None]


def style_axis(ax, labels, xs, show_xlabel: bool) -> None:
    geomean_x = xs[-1]
    ax.axvspan(geomean_x - 0.36, geomean_x + 0.36, color="#F5F5F5", zorder=0)
    ax.axhline(1.0, color="#333333", linestyle=(0, (4, 2)), linewidth=0.9, zorder=2)
    ax.axvline(geomean_x - 0.41, color="#888888", linestyle=":", linewidth=0.7)
    ax.set_ylim(0, Y_LIMIT)
    ax.set_yticks((0, 1, 2))
    ax.set_ylabel("Speedup", fontsize=7.0)
    ax.set_xticks(xs)
    ax.set_xticklabels([])
    ax.set_xlim(xs[0] - 0.36, xs[-1] + 0.36)
    ax.grid(axis="y", color="#DDDDDD", linewidth=0.45, zorder=1)
    ax.tick_params(axis="both", labelsize=6.0, length=2.4, width=0.7)
    if show_xlabel:
        for x, label in zip(xs, labels):
            ax.text(
                x,
                -0.08,
                label,
                transform=ax.get_xaxis_transform(),
                ha="center",
                va="top",
                rotation=30,
                rotation_mode="anchor",
                fontsize=6.0,
                clip_on=False,
            )
    for spine in ax.spines.values():
        spine.set_visible(True)
        spine.set_linewidth(0.7)


def draw_dimension(ax, dimension: str, grid, show_xlabel: bool = True):
    spec = DIMENSIONS[dimension]
    points = spec["points"]
    colors = point_colors(points)
    labels = [LABELS[benchmark] for benchmark in BENCHMARKS] + ["GMEAN"]
    x_step = 0.82
    xs = [index * x_step for index in range(len(labels))]
    group_width = 0.58
    bar_width = group_width / len(points)

    top_labels = []
    for point_index, (point, color) in enumerate(zip(points, colors)):
        values = series(grid, dimension, point)
        offset = -group_width / 2 + bar_width / 2 + point_index * bar_width
        heights = [math.nan if value is None else min(value, Y_LIMIT) for value in values]
        bars = ax.bar(
            [x + offset for x in xs],
            heights,
            width=bar_width * 0.94,
            color=color,
            edgecolor="none",
            linewidth=0,
            zorder=3,
        )
        for group_index, (bar, value) in enumerate(zip(bars, values)):
            if value is None or value <= Y_LIMIT:
                continue
            benchmark = (
                BENCHMARKS[group_index]
                if group_index < len(BENCHMARKS)
                else "geomean"
            )
            coordinates = TOP_LABEL_X_COORDINATES.get(benchmark)
            if coordinates is None:
                label_x = xs[group_index] + (
                    point_index - (len(points) - 1) / 2
                ) * 0.18
            else:
                label_x = coordinates[point_index]
            top_labels.append(ax.text(
                label_x,
                TOP_LABEL_Y,
                f"{value:.1f}",
                ha="center",
                va="bottom",
                fontsize=3.6,
                color=color,
                clip_on=False,
            ))

    style_axis(ax, labels, xs, show_xlabel)
    handles = [
        Patch(facecolor=color, edgecolor="none", label=legend_label(dimension, point))
        for point, color in zip(points, colors)
    ]
    ax.legend(
        handles=handles,
        loc="lower center",
        bbox_to_anchor=(0.0, 1.03, 1.0, 0.10),
        ncol=len(points),
        mode="expand",
        frameon=False,
        borderaxespad=0,
        columnspacing=0.7,
        handlelength=0.9,
        handletextpad=0.35,
        fontsize=5.2,
    )
    return top_labels


def save(fig, base: Path, dpi: int) -> None:
    fig.savefig(base.with_suffix(".png"), dpi=dpi, bbox_inches="tight", pad_inches=0.025)
    fig.savefig(base.with_suffix(".pdf"), bbox_inches="tight", pad_inches=0.025)
    print(base.with_suffix(".png"))
    print(base.with_suffix(".pdf"))


def plot(root: Path, rows, dpi: int) -> None:
    grid = build_grid(rows)
    plt.rcParams.update(
        {
            "font.family": "DejaVu Sans",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
            "axes.linewidth": 0.8,
        }
    )

    for dimension, spec in DIMENSIONS.items():
        fig, ax = plt.subplots(figsize=(3.45, 1.58))
        top_labels = draw_dimension(ax, dimension, grid)
        fig.tight_layout(pad=0.25)
        gap = verify_top_labels(fig, top_labels)
        print(
            f"{dimension}: verified {len(top_labels)} top labels; "
            f"minimum gap={gap:.2f}px"
        )
        save(fig, root / spec["output"], dpi)
        plt.close(fig)

    fig, axes = plt.subplots(3, 1, figsize=(7.16, 4.70))
    label_groups = []
    for ax, dimension in zip(axes, DIMENSIONS):
        label_groups.append(
            (dimension, draw_dimension(ax, dimension, grid, show_xlabel=True))
        )
    fig.tight_layout(pad=0.35, h_pad=0.70)
    for dimension, top_labels in label_groups:
        gap = verify_top_labels(fig, top_labels)
        print(
            f"three-panel {dimension}: verified {len(top_labels)} top labels; "
            f"minimum gap={gap:.2f}px"
        )
    save(fig, root / "cupath_sensitivity_three_panel", dpi)
    plt.close(fig)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("campaign", type=Path)
    parser.add_argument("--dpi", type=int, default=300)
    args = parser.parse_args()
    root = args.campaign.resolve()
    _, rows, summaries = load_campaign(root)
    write_tables(root, rows, summaries)
    plot(root, rows, args.dpi)


if __name__ == "__main__":
    main()
