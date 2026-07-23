#!/usr/bin/env python3
"""Plot Baseline and CuPath with the PASTA paper figure parameters."""

from __future__ import annotations

import argparse
import csv
import math
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import Patch


BENCHMARKS = [
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
]

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

CONFIGS = [
    ("baseline_speedup", "Baseline", "#83CEE2"),
    ("complete_speedup", "CuPath", "#F18541"),
]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Plot formal Baseline and CuPath speedups in the PASTA style."
    )
    parser.add_argument(
        "result_dir",
        type=Path,
        help="Directory containing cupath_baseline_complete_speedup.csv.",
    )
    parser.add_argument("--dpi", type=int, default=300)
    return parser.parse_args()


def load_speedups(csv_path: Path) -> dict[str, list[float]]:
    rows: dict[str, dict[str, str]] = {}
    with csv_path.open(newline="", encoding="utf-8") as handle:
        for row in csv.DictReader(handle):
            rows[row["benchmark"]] = row

    missing = [benchmark for benchmark in BENCHMARKS if benchmark not in rows]
    if missing:
        raise ValueError("missing benchmarks: " + ", ".join(missing))

    speedups: dict[str, list[float]] = {}
    for key, _, _ in CONFIGS:
        values = [float(rows[benchmark][key]) for benchmark in BENCHMARKS]
        geomean = math.exp(sum(math.log(value) for value in values) / len(values))
        speedups[key] = values + [geomean]
    return speedups


def annotate_clipped_bar(ax, bar, value: float, ylimit: float, x_nudge: float) -> None:
    if not math.isfinite(value) or value <= ylimit:
        return
    ax.text(
        bar.get_x() + bar.get_width() / 2 + x_nudge,
        ylimit + 0.025,
        f"{value:.1f}",
        ha="center",
        va="bottom",
        rotation=0,
        fontsize=4.9,
        fontweight="bold",
        color="#BE520E",
        clip_on=False,
    )


def plot(result_dir: Path, dpi: int) -> None:
    speedups = load_speedups(result_dir / "cupath_baseline_complete_speedup.csv")
    labels = [LABELS[benchmark] for benchmark in BENCHMARKS] + ["GMEAN"]
    x_step = 0.82
    xs = [index * x_step for index in range(len(labels))]
    group_width = 0.56
    bar_width = group_width / max(len(CONFIGS), 3)
    ylimit = 2.0

    plt.rcParams.update(
        {
            "font.family": "DejaVu Sans",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
            "axes.linewidth": 0.8,
        }
    )
    fig, ax = plt.subplots(figsize=(3.45, 1.58))

    geomean_x = xs[-1]
    ax.axvspan(geomean_x - 0.36, geomean_x + 0.36, color="#f5f5f5", zorder=0)

    for index, (key, legend, color) in enumerate(CONFIGS):
        series_width = bar_width * len(CONFIGS)
        offset = -series_width / 2 + bar_width / 2 + index * bar_width
        values = speedups[key]
        bars = ax.bar(
            [x + offset for x in xs],
            [min(value, ylimit) for value in values],
            width=bar_width * 0.94,
            label=legend,
            color=color,
            edgecolor="none",
            linewidth=0,
            zorder=3,
        )
        for bar, value in zip(bars, values):
            x_nudge = (index - (len(CONFIGS) - 1) / 2) * bar_width * 1.05
            annotate_clipped_bar(ax, bar, value, ylimit, x_nudge)

    ax.axhline(
        1.0,
        color="#333333",
        linestyle=(0, (4, 2)),
        linewidth=0.9,
        zorder=2,
    )
    ax.axvline(
        geomean_x - x_step / 2,
        color="#888888",
        linestyle=":",
        linewidth=0.7,
    )
    ax.set_ylim(0, ylimit)
    ax.set_ylabel("Speedup", fontsize=7.0)
    ax.set_yticks([0, 1.0, 2.0])
    ax.set_yticklabels(["0", "1", "2"])
    ax.set_xticks(xs)
    ax.set_xticklabels([])
    ax.set_xlim(xs[0] - group_width * 0.62, xs[-1] + group_width * 0.62)
    ax.grid(axis="y", color="#dddddd", linewidth=0.45, zorder=1)
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
        ncol=2,
        mode="expand",
        frameon=False,
        columnspacing=0.9,
        handlelength=1.0,
        handletextpad=0.45,
        fontsize=5.7,
    )

    fig.tight_layout(pad=0.25)
    for extension in ("png", "pdf"):
        output = result_dir / f"cupath_baseline_complete_speedup.{extension}"
        save_kwargs = {"bbox_inches": "tight"}
        if extension == "png":
            save_kwargs["dpi"] = dpi
        fig.savefig(output, **save_kwargs)
        print(output)
    plt.close(fig)


def main() -> None:
    args = parse_args()
    plot(args.result_dir.resolve(), args.dpi)


if __name__ == "__main__":
    main()
