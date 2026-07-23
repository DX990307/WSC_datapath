#!/usr/bin/env python3
"""Render the audited CuPath ablation using the PASTA paper style."""

from __future__ import annotations

import argparse
import math
import os
from pathlib import Path

os.environ.setdefault("MPLCONFIGDIR", "/tmp/matplotlib")
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
from matplotlib.patches import Patch

from plot_audited_cupath_ablation import (
    WORKLOADS,
    geomean,
    load_campaign,
    read_binary_digest,
    verify_equal_work,
)


CONFIGS = (
    ("baseline", "Baseline", "#D6EFF5"),
    ("m1", "Local Pairing", "#ADDEEB"),
    ("m2", "Remote Aggregation", "#5ABED8"),
    ("m3", "Remote Reuse", "#278BA5"),
    ("complete", "Complete", "#F18541"),
)


# PASTA prints every value above the 2x cap on one row. These per-label x
# offsets spread the dense FWS, KM, and MM clusters without changing y.
CLIPPED_LABEL_X_OFFSETS = {
    ("floydwarshall", "m2"): -0.8000,
    ("floydwarshall", "complete"): -0.6992,
    ("kmeans", "m2"): -0.5000,
    ("kmeans", "complete"): -0.3992,
    ("matrixmultiplication", "m2"): -0.2000,
    ("matrixmultiplication", "m3"): 0.1304,
    ("matrixmultiplication", "complete"): 0.4608,
}


def verify_clipped_labels(fig, labels) -> float:
    """Prove that capped-value labels share one row and do not overlap."""
    if len(labels) < 2:
        return float("inf")

    y_positions = [label.get_position()[1] for label in labels]
    if max(y_positions) - min(y_positions) > 1e-12:
        raise RuntimeError("clipped value labels are not on one row")

    fig.canvas.draw()
    renderer = fig.canvas.get_renderer()
    boxes = sorted(
        (label.get_window_extent(renderer=renderer) for label in labels),
        key=lambda box: box.x0,
    )
    min_gap = float("inf")
    for left, right in zip(boxes, boxes[1:]):
        gap = right.x0 - left.x1
        min_gap = min(min_gap, gap)
        if gap < 0:
            raise RuntimeError(
                f"clipped value labels overlap by {-gap:.2f} pixels"
            )
    return min_gap


def plot(output: Path, speedups) -> None:
    plt.rcParams.update(
        {
            "font.family": "DejaVu Sans",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
            "axes.linewidth": 0.8,
        }
    )

    labels = [label for _, label in WORKLOADS] + ["GMEAN"]
    x_step = 0.82
    xs = np.arange(len(labels), dtype=float) * x_step
    group_width = 0.56
    bar_width = group_width / max(len(CONFIGS), 3)
    y_limit = 2.0
    fig, ax = plt.subplots(figsize=(3.45, 1.58))

    gm_x = xs[-1]
    ax.axvspan(gm_x - 0.36, gm_x + 0.36, color="#F5F5F5", zorder=0)
    clipped_labels = []

    for config_index, (config, legend, color) in enumerate(CONFIGS):
        values = [speedups[(benchmark, config)] for benchmark, _ in WORKLOADS]
        values.append(geomean(values))
        series_width = bar_width * len(CONFIGS)
        offset = (
            -series_width / 2
            + bar_width / 2
            + config_index * bar_width
        )
        bars = ax.bar(
            xs + offset,
            np.minimum(values, y_limit),
            width=bar_width * 0.94,
            color=color,
            edgecolor="none",
            linewidth=0,
            zorder=3,
        )

        for benchmark_index, (bar, value) in enumerate(zip(bars, values)):
            if not math.isfinite(value) or value <= y_limit:
                continue
            benchmark = (
                WORKLOADS[benchmark_index][0]
                if benchmark_index < len(WORKLOADS)
                else "gmean"
            )
            x_nudge = (
                config_index - (len(CONFIGS) - 1) / 2
            ) * bar_width * 1.05
            x_nudge += CLIPPED_LABEL_X_OFFSETS.get((benchmark, config), 0.0)
            clipped_labels.append(
                ax.text(
                    bar.get_x() + bar.get_width() / 2 + x_nudge,
                    y_limit + 0.025,
                    f"{value:.1f}",
                    ha="center",
                    va="bottom",
                    rotation=0,
                    fontsize=4.9,
                    fontweight="bold",
                    color="#BE520E",
                    clip_on=False,
                )
            )

    ax.axhline(
        1.0,
        color="#333333",
        linestyle=(0, (4, 2)),
        linewidth=0.9,
        zorder=2,
    )
    ax.axvline(
        gm_x - x_step / 2,
        color="#888888",
        linestyle=":",
        linewidth=0.7,
    )
    ax.set_ylim(0, y_limit)
    ax.set_ylabel("Speedup", fontsize=7.0)
    ax.set_yticks([0, 1.0, 2.0])
    ax.set_yticklabels(["0", "1", "2"])
    ax.set_xticks(xs)
    ax.set_xticklabels([])
    ax.set_xlim(xs[0] - group_width * 0.62, xs[-1] + group_width * 0.62)
    ax.grid(axis="y", color="#DDDDDD", linewidth=0.45, zorder=1)
    ax.tick_params(axis="both", labelsize=6.0, length=2.4, width=0.7)

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

    handles = [
        Patch(facecolor=color, edgecolor="none", linewidth=0, label=legend)
        for _, legend, color in CONFIGS
    ]
    ax.legend(
        handles=handles,
        loc="lower left",
        bbox_to_anchor=(0.02, 1.06, 0.96, 0.16),
        ncol=3,
        mode="expand",
        frameon=False,
        columnspacing=0.9,
        handlelength=1.0,
        handletextpad=0.45,
        fontsize=5.7,
    )

    fig.tight_layout(pad=0.25)
    min_gap = verify_clipped_labels(fig, clipped_labels)
    print(
        f"verified {len(clipped_labels)} clipped labels on one row; "
        f"minimum horizontal gap = {min_gap:.2f} px"
    )
    fig.savefig(output.with_suffix(".png"), dpi=300, bbox_inches="tight")
    fig.savefig(output.with_suffix(".pdf"), bbox_inches="tight")
    plt.close(fig)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("baseline_results", type=Path)
    parser.add_argument("ablation_results", type=Path)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()

    repo = Path(__file__).resolve().parent.parent
    baseline_root = args.baseline_results.resolve()
    ablation_root = args.ablation_results.resolve()
    output_dir = args.output_dir.resolve()
    output_dir.mkdir(parents=True, exist_ok=True)

    if read_binary_digest(baseline_root) != read_binary_digest(ablation_root):
        raise ValueError("baseline and ablation use different frozen binaries")
    verify_equal_work(baseline_root, ablation_root)
    _, campaign, _, speedups = load_campaign(repo, baseline_root, ablation_root)
    if len(campaign) != len(WORKLOADS) * len(CONFIGS):
        raise ValueError(f"incomplete campaign: {len(campaign)}/70 cells")

    output = output_dir / "cupath_audited_ablation_pasta_style"
    plot(output, speedups)
    print(output.with_suffix(".png"))
    print(output.with_suffix(".pdf"))


if __name__ == "__main__":
    main()
